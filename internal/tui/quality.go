package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/discover"
	"github.com/launch80/L80-CLI/internal/history"
	"github.com/launch80/L80-CLI/internal/models"
	"github.com/launch80/L80-CLI/internal/qualitybench"
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

// The QualityBench screen is one form whose rows depend on the choices above
// them: Custom adds the task checklist, Local lists the servers found on this
// machine and their models, Vast offers an engine and models from history,
// the local caches and a Hugging Face search. Nothing in the normal path is
// typed; "Other…" in each list opens a text field as the fallback.

type qsec int

const (
	qsScope qsec = iota
	qsTasks
	qsTarget
	qsEndpoint
	qsEndpointURL
	qsModel
	qsEngine
	qsSearch
	qsVastModel
	qsModelID
	qsThinking
	qsNotes
	qsKey
	qsSubmit
)

const (
	scopeFull = iota
	scopeQuick
	scopeCustom
)

const (
	targetLocal = iota
	targetVast
)

// other marks the "Other…" row of a picker.
type other struct{}

type qualityScreen struct {
	inited bool
	focus  qsec

	scope, target, thinking, engine widgets.Field
	epURL, modelID, search          widgets.Field
	notes, apikey                   widgets.Field
	tasks                           widgets.Checklist
	eps, mods, vmods                widgets.List

	// scan, sources and find are swapped out by tests.
	scan    func(context.Context) []discover.Endpoint
	sources func(c *Console) []models.Model
	find    func(context.Context, string) ([]models.Model, error)

	found     []discover.Endpoint
	scanning  bool
	scanned   bool
	modelsFor string // the endpoint mods was built for
	cands     []models.Model
	hits      []models.Model
	searching bool
	searched  string

	job     *job
	prog    *qbProgress
	result  *app.QualityResult
	summary []string
	url     string
	phase   int // 0 form, 1 running/log, 2 done
}

type (
	qbScanned  struct{ eps []discover.Endpoint }
	qbSources  struct{ ms []models.Model }
	qbSearched struct {
		query string
		ms    []models.Model
		err   error
	}
	qbPublished struct {
		art *api.Artifact
		err *api.Error
	}
)

func (s *qualityScreen) ensure() {
	if s.inited {
		return
	}
	s.inited = true
	s.scope = widgets.Field{Key: "scope", Label: "Scope", Kind: widgets.ChoiceField, Choices: []string{"Full", "Quick", "Custom"},
		Help: "Full runs the whole suite. Quick runs a fixed tenth of each task: a smoke test, and the page says so. Custom runs the tasks you tick, at full size."}
	s.target = widgets.Field{Key: "target", Label: "Target", Kind: widgets.ChoiceField, Choices: []string{"Local GPUs", "Rent via Vast"},
		Help: "Local runs against a server already running on this machine. Vast rents a GPU, serves the model there and tears the box down."}
	s.thinking = widgets.Field{Key: "thinking", Label: "Thinking", Kind: widgets.ChoiceField, Choices: []string{"default", "off", "on"},
		Help: "Force reasoning on or off for every request, or leave the server's default. QualityBench checks the server obeys before it starts."}
	s.engine = widgets.Field{Key: "engine", Label: "Engine", Kind: widgets.ChoiceField, Choices: []string{"vLLM", "llama.cpp"},
		Help: "The serving engine the rented box runs."}
	s.epURL = widgets.Field{Key: "url", Label: "URL", Help: "OpenAI-compatible base URL, e.g. http://127.0.0.1:8000/v1. It never leaves this machine.",
		Input: widgets.TextInput{Placeholder: "http://127.0.0.1:8000/v1"}}
	s.modelID = widgets.Field{Key: "model", Label: "Model id", Help: "The model id exactly as the server lists it (for a rental: a Hugging Face repo, org/name).",
		Input: widgets.TextInput{Placeholder: "org/name"}}
	s.search = widgets.Field{Key: "search", Label: "Search", Help: "Type part of a name and press enter to search Hugging Face; results join the list below.",
		Input: widgets.TextInput{Placeholder: "e.g. qwen3 8b"}}
	s.notes = widgets.Field{Key: "notes", Label: "Notes", Help: "Comma-separated KEY=VALUE recorded with the run, e.g. engine=vLLM 0.30, quant=int4.",
		Input: widgets.TextInput{Placeholder: "engine=vLLM 0.30, quant=int4"}}
	s.apikey = widgets.Field{Key: "apikey", Label: "API key", Help: "Only if the endpoint needs one. Passed to QualityBench by environment, never recorded or shown.",
		Input: widgets.TextInput{Mask: true, Placeholder: "(none)"}}
	if k := os.Getenv("L80_QUALITYBENCH_API_KEY"); k != "" {
		s.apikey.Input.Set(k)
	}
	for _, t := range qualitybench.Suite {
		mins := "—"
		if t.RefMinutes > 0 {
			mins = fmt.Sprintf("%dm", t.RefMinutes)
		}
		desc := fmt.Sprintf("%5d items  %4s  %s", t.Items, mins, t.Measures)
		s.tasks.Items = append(s.tasks.Items, widgets.Check{Title: t.Label, Desc: desc, On: t.Default, Value: t.ID})
	}
	if s.scan == nil {
		s.scan = func(ctx context.Context) []discover.Endpoint { return discover.Scan(ctx, discover.Options{}) }
	}
	if s.sources == nil {
		s.sources = localSources
	}
	if s.find == nil {
		s.find = func(ctx context.Context, q string) ([]models.Model, error) { return models.Search(ctx, nil, q, 20) }
	}
	s.buildEndpoints()
	s.buildVastModels()
}

// localSources gathers the models offered for a rental: ones published or
// rented before, then the Hugging Face cache, then ~/models.
func localSources(c *Console) []models.Model {
	var titles []string
	if entries, err := history.Read(); err == nil {
		for _, e := range entries {
			titles = append(titles, e.Title)
		}
	}
	home, _ := os.UserHomeDir()
	return models.Merge(
		models.SpecRepos(filepath.Join(filepath.Dir(c.d.RunsDir), "specs")),
		models.FromTitles(titles),
		models.HFCache(models.HubDir()),
		models.LocalDir(filepath.Join(home, "models")),
	)
}

// load starts the scans this screen shows: local servers and model sources.
func (s *qualityScreen) load(c *Console) Cmd {
	s.ensure()
	s.scanning = true
	scan, sources := s.scan, s.sources
	return Batch(func() Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return qbScanned{scan(ctx)}
	}, func() Msg { return qbSources{sources(c)} })
}

func (s *qualityScreen) runSearch(q string) Cmd {
	q = strings.TrimSpace(q)
	if q == "" || s.searching {
		return nil
	}
	s.searching = true
	find := s.find
	return func() Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ms, err := find(ctx, q)
		return qbSearched{q, ms, err}
	}
}

func (s *qualityScreen) buildEndpoints() {
	var items []widgets.Item
	for i, ep := range s.found {
		n := len(ep.Models)
		desc := fmt.Sprintf("%s · %d model", engineName(ep), n)
		if n != 1 {
			desc += "s"
		}
		items = append(items, widgets.Item{Title: ep.URL, Desc: desc, Value: i})
	}
	items = append(items, widgets.Item{Title: "Other…", Desc: "type a URL", Value: other{}})
	s.eps.Items = items
	if s.eps.Cursor >= len(items) {
		s.eps.Cursor = 0
	}
	s.modelsFor = "\x00"
	s.syncModels()
}

func engineName(ep discover.Endpoint) string {
	if ep.Version != "" {
		return ep.Engine + " " + ep.Version
	}
	return ep.Engine
}

// selectedEndpoint is the endpoint under the cursor; ok is false on Other.
func (s *qualityScreen) selectedEndpoint() (discover.Endpoint, bool) {
	it, ok := s.eps.Selected()
	if !ok {
		return discover.Endpoint{}, false
	}
	i, isIdx := it.Value.(int)
	if !isIdx || i >= len(s.found) {
		return discover.Endpoint{}, false
	}
	return s.found[i], true
}

// syncModels rebuilds the model list when the endpoint under the cursor changes.
func (s *qualityScreen) syncModels() {
	ep, ok := s.selectedEndpoint()
	key := ep.URL
	if !ok {
		key = ""
	}
	if key == s.modelsFor {
		return
	}
	s.modelsFor = key
	var items []widgets.Item
	loadedFirst := -1
	for _, m := range ep.Models {
		var tags []string
		if m.Backend != "" {
			tags = append(tags, m.Backend)
		}
		if m.GPU != "" {
			tags = append(tags, m.GPU)
		}
		if !m.Loaded {
			tags = append(tags, "not loaded: the first request pays the load time")
		} else if loadedFirst < 0 {
			loadedFirst = len(items)
		}
		items = append(items, widgets.Item{Title: m.ID, Desc: strings.Join(tags, " · "), Value: m.ID})
	}
	items = append(items, widgets.Item{Title: "Other…", Desc: "type a model id", Value: other{}})
	s.mods = widgets.List{Items: items, Filterable: true}
	if loadedFirst > 0 {
		s.mods.Cursor = loadedFirst
	}
}

func (s *qualityScreen) buildVastModels() {
	var items []widgets.Item
	for _, m := range models.Merge(s.hits, s.cands) {
		desc := m.Source
		if m.Detail != "" {
			desc += " · " + m.Detail
		}
		if !m.IsRepo() {
			desc += " · not a repo id: enter looks it up on Hugging Face"
		}
		items = append(items, widgets.Item{Title: m.ID, Desc: desc, Value: m})
	}
	items = append(items, widgets.Item{Title: "Other…", Desc: "type a Hugging Face repo (org/name)", Value: other{}})
	cur := s.vmods.Cursor
	s.vmods = widgets.List{Items: items, Filterable: true, Cursor: min(cur, len(items)-1)}
}

func (s *qualityScreen) local() bool { return s.target.Choice == targetLocal }

func pickedOther(l *widgets.List) bool {
	it, ok := l.Selected()
	if !ok {
		return false
	}
	_, o := it.Value.(other)
	return o
}

// sections are the rows the form shows now, in order.
func (s *qualityScreen) sections() []qsec {
	out := []qsec{qsScope}
	if s.scope.Choice == scopeCustom {
		out = append(out, qsTasks)
	}
	out = append(out, qsTarget)
	if s.local() {
		out = append(out, qsEndpoint)
		if pickedOther(&s.eps) {
			out = append(out, qsEndpointURL, qsModelID)
		} else {
			out = append(out, qsModel)
			if pickedOther(&s.mods) {
				out = append(out, qsModelID)
			}
		}
	} else {
		out = append(out, qsEngine, qsSearch, qsVastModel)
		if pickedOther(&s.vmods) {
			out = append(out, qsModelID)
		}
	}
	return append(out, qsThinking, qsNotes, qsKey, qsSubmit)
}

func (s *qualityScreen) move(d int) {
	secs := s.sections()
	i := 0
	for j, x := range secs {
		if x == s.focus {
			i = j
		}
	}
	s.focus = secs[(i+d+len(secs))%len(secs)]
}

func (s *qualityScreen) field(q qsec) *widgets.Field {
	switch q {
	case qsScope:
		return &s.scope
	case qsTarget:
		return &s.target
	case qsThinking:
		return &s.thinking
	case qsEngine:
		return &s.engine
	case qsEndpointURL:
		return &s.epURL
	case qsModelID:
		return &s.modelID
	case qsSearch:
		return &s.search
	case qsNotes:
		return &s.notes
	case qsKey:
		return &s.apikey
	}
	return nil
}

func (s *qualityScreen) list(q qsec) *widgets.List {
	switch q {
	case qsEndpoint:
		return &s.eps
	case qsModel:
		return &s.mods
	case qsVastModel:
		return &s.vmods
	}
	return nil
}

func listAtEdge(l *widgets.List, k input.Key) bool {
	n := len(l.Visible())
	return (k.Code == input.Up && l.Cursor <= 0) || (k.Code == input.Down && l.Cursor >= n-1)
}

func (s *qualityScreen) update(c *Console, m Msg) Cmd {
	s.ensure()
	switch v := m.(type) {
	case qbScanned:
		s.found, s.scanning, s.scanned = v.eps, false, true
		s.buildEndpoints()
		return nil
	case qbSources:
		s.cands = v.ms
		s.buildVastModels()
		return nil
	case qbSearched:
		s.searching, s.searched = false, v.query
		if v.err != nil {
			c.setFlash("search: "+v.err.Error(), true)
			return nil
		}
		s.hits = v.ms
		s.buildVastModels()
		s.vmods.Cursor = 0
		if len(v.ms) == 0 {
			c.setFlash(fmt.Sprintf("no Hugging Face models match %q", v.query), true)
		}
		return nil
	case jobDone:
		if v.j != s.job {
			return nil
		}
		s.phase = 2
		if r, ok := v.result.(*app.QualityResult); ok && r != nil {
			s.result = r
			s.summary = qualitySummary(r)
		}
		if v.err != nil {
			c.setFlash(v.err.Error(), true)
		}
		return nil
	case qbPublished:
		if v.err != nil {
			c.setFlash(v.err.Code+": "+v.err.Message+" — "+v.err.Remedy, true)
			return nil
		}
		s.url = v.art.URL
		c.setFlash("published: "+v.art.URL, false)
		return nil
	case PasteMsg:
		if f := s.field(s.focus); s.phase == 0 && f != nil && f.Kind == widgets.TextField {
			f.Input.HandlePaste(v)
		}
	case MouseMsg:
		if s.job != nil && s.phase > 0 {
			s.job.log.HandleMouse(v)
		}
	case KeyMsg:
		if s.phase == 0 {
			return s.formKey(c, v)
		}
		if s.job.log.HandleKey(v) {
			return nil
		}
		switch {
		case v.Ctrl && v.Rune == 'x' && s.job.running:
			s.job.cancel()
		case s.job.running:
		case v.Code == input.Rune && v.Rune == 'p' && s.result != nil && s.url == "":
			return s.publish(c, false)
		case v.Code == input.Rune && v.Rune == 'o' && s.result != nil:
			if err := openFile(s.result.Report); err != nil {
				c.setFlash("could not open "+s.result.Report+": "+err.Error(), true)
			} else {
				c.setFlash("opened "+s.result.Report, false)
			}
		case v.Code == input.Rune && v.Rune == 'n':
			s.phase, s.result, s.summary, s.url = 0, nil, nil, ""
		case isBack(v):
			s.phase = 0
		}
	}
	return nil
}

func (s *qualityScreen) formKey(c *Console, k KeyMsg) Cmd {
	f, l := s.field(s.focus), s.list(s.focus)
	typing := f != nil && f.Kind == widgets.TextField
	if l != nil && l.Filtering() {
		l.HandleKey(k)
		s.syncModels()
		return nil
	}
	switch {
	case k.Code == input.Esc:
		c.scr = scrHome
		return nil
	case k.Code == input.Tab:
		s.move(1)
		return nil
	case k.Code == input.Backtab:
		s.move(-1)
		return nil
	case k.Code == input.Rune && k.Rune == 'r' && !typing && !k.Ctrl:
		return s.load(c)
	}
	switch s.focus {
	case qsSubmit:
		switch k.Code {
		case input.Enter:
			return s.submit(c)
		case input.Up:
			s.move(-1)
		case input.Down:
			s.move(1)
		}
		return nil
	case qsTasks:
		if (k.Code == input.Up || k.Code == input.Down) && s.tasks.AtEdge(k) {
			s.move(map[bool]int{true: -1, false: 1}[k.Code == input.Up])
			return nil
		}
		if k.Code == input.Enter {
			s.move(1)
			return nil
		}
		s.tasks.HandleKey(k)
		return nil
	}
	if l != nil {
		switch {
		case (k.Code == input.Up || k.Code == input.Down) && listAtEdge(l, k):
			s.move(map[bool]int{true: -1, false: 1}[k.Code == input.Up])
		case k.Code == input.Enter:
			if it, ok := l.Selected(); ok && s.focus == qsVastModel {
				if m, isModel := it.Value.(models.Model); isModel && !m.IsRepo() {
					s.search.Input.Set(m.ID)
					return s.runSearch(m.ID)
				}
			}
			s.move(1)
		default:
			l.HandleKey(k)
		}
		s.syncModels()
		return nil
	}
	if f == nil {
		return nil
	}
	if f.Kind == widgets.ChoiceField {
		switch {
		case k.Code == input.Left:
			f.Choice = (f.Choice + len(f.Choices) - 1) % len(f.Choices)
		case k.Code == input.Right || (k.Code == input.Rune && k.Rune == ' '):
			f.Choice = (f.Choice + 1) % len(f.Choices)
		case k.Code == input.Up:
			s.move(-1)
		case k.Code == input.Down, k.Code == input.Enter:
			s.move(1)
		}
		return nil
	}
	switch k.Code {
	case input.Enter:
		if s.focus == qsSearch {
			return s.runSearch(s.search.Input.String())
		}
		s.move(1)
	case input.Up:
		s.move(-1)
	case input.Down:
		s.move(1)
	default:
		f.Input.HandleKey(k)
	}
	return nil
}

// scopeOf turns the Scope row (and the checklist) into a run scope.
func (s *qualityScreen) scopeOf() (qualitybench.Scope, error) {
	switch s.scope.Choice {
	case scopeQuick:
		return qualitybench.ResolveScope(true, "")
	case scopeCustom:
		var ids []string
		for _, v := range s.tasks.Checked() {
			ids = append(ids, v.(string))
		}
		if len(ids) == 0 {
			return qualitybench.Scope{}, fmt.Errorf("tick at least one task (space toggles, a ticks all)")
		}
		return qualitybench.ResolveScope(false, strings.Join(ids, ","))
	}
	return qualitybench.ResolveScope(false, "")
}

func (s *qualityScreen) estimate() string {
	sc, err := s.scopeOf()
	if err != nil {
		return "no tasks ticked"
	}
	mins, un := qualitybench.EstimateMinutes(sc.Tasks, sc.Tier == qualitybench.TierQuick)
	out := fmt.Sprintf("%d task", len(sc.Tasks))
	if len(sc.Tasks) != 1 {
		out += "s"
	}
	out += fmt.Sprintf(" · about %s with thinking off", humanMinutes(mins))
	if un > 0 {
		out += fmt.Sprintf(", +%d untimed", un)
	}
	return out
}

func humanMinutes(m int) string {
	if m < 60 {
		return fmt.Sprintf("%d min", m)
	}
	return fmt.Sprintf("%dh%02d", m/60, m%60)
}

// target resolves the endpoint and model the form points at.
func (s *qualityScreen) localTarget() (endpoint, model string, err error) {
	if ep, ok := s.selectedEndpoint(); ok {
		endpoint = ep.URL
		if it, ok := s.mods.Selected(); ok {
			if id, isID := it.Value.(string); isID {
				model = id
			}
		}
	} else {
		endpoint = s.epURL.Value()
		if endpoint == "" {
			return "", "", fmt.Errorf("pick a server, or type its URL under Other…")
		}
		if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
			return "", "", fmt.Errorf("the URL needs http:// or https://")
		}
	}
	if model == "" {
		model = s.modelID.Value()
	}
	if model == "" {
		return "", "", fmt.Errorf("pick a model, or type its id under Other…")
	}
	return endpoint, model, nil
}

func (s *qualityScreen) vastModel() (models.Model, error) {
	it, ok := s.vmods.Selected()
	var m models.Model
	if ok {
		m, _ = it.Value.(models.Model)
	}
	if m.ID == "" {
		m = models.Model{ID: s.modelID.Value()}
	}
	if !m.IsRepo() {
		return m, fmt.Errorf("pick a Hugging Face repo (org/name); search for one, or type it under Other…")
	}
	return m, nil
}

func (s *qualityScreen) submit(c *Console) Cmd {
	scope, err := s.scopeOf()
	if err != nil {
		c.setFlash(err.Error(), true)
		return nil
	}
	if !s.local() {
		m, err := s.vastModel()
		if err != nil {
			c.setFlash(err.Error(), true)
			return nil
		}
		eng := map[int]string{0: "vllm", 1: "llamacpp"}[s.engine.Choice]
		c.confirm(&widgets.Dialog{Title: "Rent via Vast",
			Body: "QualityBench runs on rented GPUs are coming: they need a `benchmark: qualitybench` step in L80 vast first. Nothing was rented.\n\n" +
				"To rent a box for this model now, write a spec and start it from the Rent (vast) screen:\n\n" +
				"  L80 vast spec --model " + m.ID + " --engine " + eng + "\n\n" +
				"then run QualityBench here, against the rented endpoint, under Other….",
			Buttons: []string{"OK"}}, nil)
		return nil
	}
	endpoint, model, err := s.localTarget()
	if err != nil {
		c.setFlash(err.Error(), true)
		return nil
	}
	var notes []string
	for _, n := range strings.Split(s.notes.Value(), ",") {
		if n = strings.TrimSpace(n); n != "" {
			if !strings.Contains(n, "=") {
				c.setFlash(fmt.Sprintf("note %q needs KEY=VALUE", n), true)
				return nil
			}
			notes = append(notes, n)
		}
	}
	thinking := ""
	if s.thinking.Choice > 0 {
		thinking = s.thinking.Value()
	}
	cwd, _ := os.Getwd()
	safe := strings.Map(func(r rune) rune {
		if r == '/' || r == ' ' || r == ':' {
			return '_'
		}
		return r
	}, model)
	out := filepath.Join(cwd, fmt.Sprintf("qualitybench-%s-%s.json", safe, time.Now().Format("20060102-1504")))
	opts := qualitybench.RunOptions{Endpoint: endpoint, Model: model, Out: out, Scope: scope, Thinking: thinking,
		Notes: notes, APIKey: s.apikey.Input.String()}
	s.prog = newQBProgress(scope.Tasks)
	s.job = newJob("QualityBench " + model)
	s.phase, s.result, s.summary, s.url = 1, nil, nil, ""
	prog := s.prog
	return c.start(s.job, func(ctx context.Context, w io.Writer) (any, int, error) {
		pw := &progressWriter{out: w, p: prog}
		res, apiErr := app.RunQuality(ctx, opts, app.WriterSink{W: pw})
		pw.flush()
		if apiErr != nil {
			return nil, 1, fmt.Errorf("%s: %s — %s", apiErr.Code, apiErr.Message, apiErr.Remedy)
		}
		return &res, 0, nil
	})
}

func qualitySummary(r *app.QualityResult) []string {
	out := []string{"results " + r.Path}
	if _, err := os.Stat(r.Report); err == nil {
		out = append(out, "report  "+r.Report)
	}
	return append(out, qualitybench.Headline(r.Data)...)
}

func (s *qualityScreen) publish(c *Console, confirmed bool) Cmd {
	if !c.d.Config.HasToken() {
		c.setFlash("publishing needs a token: set L80_TOKEN or ~/.config/L80/credentials.json (L80 auth status)", true)
		return nil
	}
	if !confirmed {
		c.confirm(&widgets.Dialog{Title: "Publish", Body: "Publish this run to launch80 as a public page?\n\nThe endpoint URL and hostname are removed first. The page is self-reported.",
			Buttons: []string{"Publish", "Cancel"}, Choice: 1}, func(ch int) Cmd {
			if ch == 0 {
				return s.publish(c, true)
			}
			return nil
		})
		return nil
	}
	res := s.result
	return func() Msg {
		body, err := qualitybench.Prepare(res.Data)
		if err != nil {
			return qbPublished{err: api.Newf("E_INPUT_INVALID", "Publish it from the command line to see details: L80 qualitybench --results <file>.", "%v", err)}
		}
		if len(body) > api.MaxPayloadBytesFor(qualitybench.TemplateID) {
			return qbPublished{err: api.Newf("E_PAYLOAD_TOO_LARGE", "Rerun with a current QualityBench.", "payload is %d bytes", len(body))}
		}
		pub, apiErr := app.Publish(c.d.Client, app.PublishRequest{Body: body, Template: qualitybench.TemplateID, SourcePath: res.Path, APIBase: c.d.Config.BaseURL})
		if apiErr != nil {
			return qbPublished{err: apiErr}
		}
		return qbPublished{art: pub.Artifact}
	}
}

// openFile shows a file in the desktop's default viewer, best effort.
var openFile = func(path string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	return exec.Command(name, path).Start()
}

// --- progress ------------------------------------------------------------------

// qbProgress is the per-task state parsed from the tool's qb:progress lines
// (docs/qualitybench.md). The job goroutine writes it; the view reads it.
type qbProgress struct {
	mu    sync.Mutex
	order []string
	rows  map[string]*qbRow
}

type qbRow struct {
	done, total, correct int
	state                string
}

func newQBProgress(tasks []string) *qbProgress {
	p := &qbProgress{rows: map[string]*qbRow{}}
	for _, t := range tasks {
		p.order = append(p.order, t)
		p.rows[t] = &qbRow{state: "queued"}
	}
	return p
}

const progressPrefix = "qb:progress "

func (p *qbProgress) parse(line string) bool {
	rest, ok := strings.CutPrefix(line, progressPrefix)
	if !ok {
		return false
	}
	var u struct {
		Task    string `json:"task"`
		Done    *int   `json:"done"`
		Total   *int   `json:"total"`
		Correct *int   `json:"correct"`
		State   string `json:"state"`
	}
	if json.Unmarshal([]byte(rest), &u) != nil || u.Task == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.rows[u.Task]
	if !ok {
		r = &qbRow{}
		p.rows[u.Task] = r
		p.order = append(p.order, u.Task)
	}
	if u.Total != nil {
		r.total = *u.Total
	}
	if u.Done != nil {
		r.done = *u.Done
	}
	if u.Correct != nil {
		r.correct = *u.Correct
	}
	switch {
	case u.State != "":
		r.state = u.State
	case r.state == "queued":
		r.state = "running"
	}
	if u.State == "scored" && r.total > 0 {
		r.done = r.total
	}
	return true
}

func (p *qbProgress) view(t widgets.Theme, w int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	barW := max(8, min(30, w-52))
	for _, id := range p.order {
		r := p.rows[id]
		label := id
		if tk, ok := qualitybench.Lookup(id); ok {
			label = tk.Label
		}
		frac := 0.0
		if r.total > 0 {
			frac = float64(r.done) / float64(r.total)
		}
		count := "—"
		if r.total > 0 {
			count = fmt.Sprintf("%d/%d", r.done, r.total)
		}
		st := t.Muted
		state := r.state
		switch r.state {
		case "running":
			st = t.Accent
		case "scored":
			st = t.OK
			if r.total > 0 {
				state = fmt.Sprintf("scored %.1f%%", 100*float64(r.correct)/float64(r.total))
			}
		case "partial":
			st = t.Warn
		}
		line := "  " + style.PadRight(label, 18) + widgets.Progress(t, frac, barW) + " " + style.PadRight(count, 11) + t.R(st, state)
		out = append(out, style.Truncate(line, w, "…"))
	}
	return out
}

// progressWriter passes the tool's output to the log, except qb:progress
// lines, which update the task table instead.
type progressWriter struct {
	out io.Writer
	p   *qbProgress
	buf []byte
}

func (w *progressWriter) Write(b []byte) (int, error) {
	w.buf = append(w.buf, b...)
	var pass []byte
	for {
		i := strings.IndexByte(string(w.buf), '\n')
		if i < 0 {
			break
		}
		line := w.buf[:i+1]
		if !w.p.parse(strings.TrimRight(string(line), "\r\n")) {
			pass = append(pass, line...)
		}
		w.buf = w.buf[i+1:]
	}
	if _, err := w.out.Write(pass); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (w *progressWriter) flush() {
	if len(w.buf) > 0 && !w.p.parse(string(w.buf)) {
		w.out.Write(w.buf)
	}
	w.buf = nil
}

// --- view -------------------------------------------------------------------------

func (s *qualityScreen) view(c *Console, w, h int) ([]string, []widgets.Binding) {
	s.ensure()
	t := c.th
	if s.phase > 0 {
		return s.runView(c, w, h)
	}
	const lw = 11
	var out []string
	focusAt := 0
	help := ""
	label := func(q qsec, name string) string {
		if s.focus == q {
			return t.R(t.Accent, style.PadRight(name, lw))
		}
		return t.R(t.Muted, style.PadRight(name, lw))
	}
	choice := func(q qsec, f *widgets.Field, extra string) {
		var parts []string
		for j, ch := range f.Choices {
			if j == f.Choice {
				ch = "‹" + ch + "›"
				if s.focus == q {
					ch = t.R(t.Selected, ch)
				} else {
					ch = t.R(t.Accent, ch)
				}
			}
			parts = append(parts, ch)
		}
		line := label(q, f.Label) + strings.Join(parts, " ")
		if extra != "" {
			line += t.R(t.Muted, "   "+extra)
		}
		out = append(out, style.Truncate(line, w, "…"))
	}
	text := func(q qsec, f *widgets.Field) {
		out = append(out, label(q, f.Label)+f.Input.View(t, w-lw, s.focus == q))
	}
	picker := func(q qsec, name string, l *widgets.List, rows int) {
		out = append(out, label(q, name))
		out = append(out, indent(l.View(t, w-2, rows, s.focus == q), 2)...)
	}
	for _, q := range s.sections() {
		if q == s.focus {
			focusAt = len(out)
		}
		switch q {
		case qsScope:
			choice(q, &s.scope, s.estimate())
		case qsTasks:
			out = append(out, label(q, "Tasks")+t.R(t.Muted, "minutes: full tier, "+qualitybench.RefRun))
			out = append(out, indent(s.tasks.View(t, w-2, len(s.tasks.Items), s.focus == q), 2)...)
		case qsTarget:
			choice(q, &s.target, "")
		case qsEndpoint:
			switch {
			case s.scanning:
				out = append(out, label(q, "Server")+t.R(t.Muted, widgets.Spin(c.tick)+" looking for servers on this machine…"))
			case s.scanned && len(s.found) == 0:
				out = append(out, label(q, "Server")+t.R(t.Warn, "no server found on the usual ports; start one and press r, or use Other…"))
			}
			picker(q, "Server", &s.eps, min(8, 2*len(s.eps.Items)))
		case qsModel:
			picker(q, "Model", &s.mods, min(12, 2*len(s.mods.Items)))
		case qsEngine:
			choice(q, &s.engine, "")
		case qsSearch:
			text(q, &s.search)
			if s.searching {
				out = append(out, strings.Repeat(" ", lw)+t.R(t.Muted, widgets.Spin(c.tick)+" searching Hugging Face…"))
			}
		case qsVastModel:
			picker(q, "Model", &s.vmods, min(12, 2*len(s.vmods.Items)))
		case qsThinking:
			choice(q, &s.thinking, "")
		case qsSubmit:
			name := "Run QualityBench"
			if !s.local() {
				name = "Plan the rental"
			}
			btn := "[ " + name + " ]"
			if s.focus == q {
				btn = t.R(t.Selected, btn)
			} else {
				btn = t.R(t.Accent, btn)
			}
			out = append(out, "", strings.Repeat(" ", lw)+btn)
		default:
			text(q, s.field(q))
		}
	}
	switch s.focus {
	case qsTasks:
		help = "Space ticks a task, a ticks or clears them all. Custom tasks run at full size."
	case qsEndpoint:
		help = "Servers found on this machine's usual ports (vLLM, llama.cpp, llama-swap, SGLang, Ollama, LM Studio). r scans again."
	case qsModel, qsVastModel:
		help = "↑↓ to pick, / to filter."
		if s.focus == qsVastModel {
			help += " Recent models, your Hugging Face cache and ~/models; search above for more."
		}
	case qsSubmit:
		if s.local() {
			help = "Runs QualityBench against the server; results and an HTML report go to the current directory."
		} else {
			help = "Shows what renting for this model takes. Nothing is rented from here yet."
		}
	default:
		if f := s.field(s.focus); f != nil {
			help = f.Help
		}
	}
	out = append(out, "")
	for _, l := range style.Wrap(help, w) {
		out = append(out, t.R(t.Muted, l))
	}
	// Keep the focused row on screen when the form is taller than the pane.
	if len(out) > h && focusAt > h/2 {
		out = out[min(focusAt-h/2, len(out)-h):]
	}
	keys := []widgets.Binding{{Keys: "tab", Desc: "next"}, {Keys: "↑↓", Desc: "move"}, {Keys: "space/←→", Desc: "choose"}, {Keys: "enter", Desc: "next / run"}, {Keys: "r", Desc: "rescan"}, {Keys: "esc", Desc: "back"}}
	return out, keys
}

func indent(lines []string, n int) []string {
	pad := strings.Repeat(" ", n)
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return lines
}

func (s *qualityScreen) runView(c *Console, w, h int) ([]string, []widgets.Binding) {
	t := c.th
	out := []string{jobHeader(c, s.job, w), ""}
	if s.prog != nil {
		out = append(out, s.prog.view(t, w)...)
		out = append(out, "")
	}
	if s.phase == 2 {
		for _, l := range s.summary {
			for _, wl := range style.Wrap(style.Sanitize(l), w-2) {
				out = append(out, t.R(t.Text, "  "+wl))
			}
		}
		if s.url != "" {
			out = append(out, t.R(t.OK, "  published: "+s.url))
		}
		out = append(out, "")
	}
	logH := max(3, h-len(out))
	out = append(out, s.job.log.View(t, w, logH)...)
	keys := []widgets.Binding{{Keys: "pgup/pgdn", Desc: "scroll"}}
	if s.job.running {
		keys = append(keys, widgets.Binding{Keys: "ctrl+x", Desc: "stop"})
	} else {
		if s.result != nil && s.url == "" {
			keys = append(keys, widgets.Binding{Keys: "p", Desc: "publish"})
		}
		if s.result != nil {
			keys = append(keys, widgets.Binding{Keys: "o", Desc: "open report"})
		}
		keys = append(keys, widgets.Binding{Keys: "n", Desc: "new run"}, widgets.Binding{Keys: "esc", Desc: "back to form"})
	}
	return out, keys
}
