package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/discover"
	"github.com/launch80/L80-CLI/internal/history"
	"github.com/launch80/L80-CLI/internal/models"
	"github.com/launch80/L80-CLI/internal/qualitybench"
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
	"github.com/launch80/L80-CLI/internal/vast"
)

// The QualityBench screen is one form whose rows depend on the choices above
// them: Custom adds the task checklist, Local lists the servers found on this
// machine and their models, Vast offers an engine and models from history,
// the local caches and a Hugging Face search. Nothing in the normal path is
// typed; "Other…" in each list opens a text field as the fallback.

type qsec int

const (
	qsMode qsec = iota
	qsScope
	qsTasks
	qsTarget
	qsEndpoint
	qsEndpointURL
	qsModel
	qsEngine
	qsSearch
	qsVastModel
	qsModelID
	qsTokenizer
	qsThinking
	qsReference
	qsCmpRef
	qsCmpTest
	qsThresholds
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

const (
	modeRun = iota
	modeCompare
)

// other marks the "Other…" row of a picker.
type other struct{}

type qualityScreen struct {
	inited bool
	focus  qsec

	mode, scope, target, thinking, engine widgets.Field
	epURL, modelID, search                widgets.Field
	notes, apikey, tokenizer, thresholds  widgets.Field
	tasks                                 widgets.Checklist
	eps, mods, vmods                      widgets.List
	refs, cmpRef, cmpTest                 widgets.List

	// results are the local results files with their .qb folders, for
	// --reference and Compare. findResults is swapped out by tests.
	results     []qualitybench.ResultFile
	findResults func(c *Console) []qualitybench.ResultFile
	comparing   bool // the finished job was a compare

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

	// estimate asks the tool how long a run takes; swapped out by tests.
	estimateFn func(ctx context.Context, o qualitybench.RunOptions, noProbe bool) qualitybench.Estimate
	// estimating is the "checking how long this will take" step after Run;
	// estCancel stops it.
	estimating bool
	estCancel  context.CancelFunc
	// scopeEst is a quiet estimate for the Scope row (history or the
	// reference table: no requests to the server), for scopeKey.
	scopeEst  *qualitybench.Estimate
	scopeKey  string
	scopeBusy bool

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
	qbResults   struct{ rs []qualitybench.ResultFile }
	qbEstimated struct {
		key    string
		est    qualitybench.Estimate
		forRun bool
		opts   qualitybench.RunOptions
		err    error
	}
)

func (s *qualityScreen) ensure() {
	if s.inited {
		return
	}
	s.inited = true
	s.focus = qsScope // Run is the common case; Do is one row up
	s.mode = widgets.Field{Key: "mode", Label: "Do", Kind: widgets.ChoiceField, Choices: []string{"Run", "Compare"},
		Help: "Run measures a model now. Compare pairs two finished runs item by item (paired deltas with intervals, KLD, threshold verdicts) and needs no server."}
	s.tokenizer = widgets.Field{Key: "tokenizer", Label: "Tokenizer", Help: "The Hugging Face repo (or local dir) logprobs and needle tokenize with. Leave empty to use the server's root or the model id; set it when the served id is an alias.",
		Input: widgets.TextInput{Placeholder: "auto"}}
	s.thresholds = widgets.Field{Key: "thresholds", Label: "Thresholds", Help: "A pre-registered thresholds file (Markdown); empty uses QualityBench's default bars. Its sha256 is recorded.",
		Input: widgets.TextInput{Placeholder: "default bars"}}
	s.scope = widgets.Field{Key: "scope", Label: "Scope", Kind: widgets.ChoiceField, Choices: []string{"Full", "Quick", "Custom"},
		Help: "Full runs the whole suite. Quick runs a fixed tenth of each task: a smoke test, and the page says so. Custom runs the tasks you tick, at full size."}
	s.target = widgets.Field{Key: "target", Label: "Target", Kind: widgets.ChoiceField, Choices: []string{"Local GPUs", "Rent via Vast"},
		Help: "Local runs against a server already running on this machine. Vast rents a GPU, serves the model there and tears the box down."}
	s.thinking = widgets.Field{Key: "thinking", Label: "Thinking", Kind: widgets.ChoiceField, Choices: []string{"default", "off", "on"},
		Help: "Force reasoning on or off for every request, or leave the server's default. Off is the usual baseline, and fine for models without thinking. QualityBench checks the server obeys before it starts; a model that thinks by default needs off or on."}
	s.thinking.Choice = 1 // off
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
	if s.findResults == nil {
		s.findResults = localResults
	}
	if s.estimateFn == nil {
		s.estimateFn = app.EstimateQuality
	}
	s.buildEndpoints()
	s.buildVastModels()
	s.buildResults()
}

// localResults finds QualityBench results this machine can compare: files
// it published, the current directory, and rental step folders.
func localResults(c *Console) []qualitybench.ResultFile {
	var files []string
	if entries, err := history.Read(); err == nil {
		for _, e := range entries {
			if e.TemplateID == qualitybench.TemplateID && e.SourcePath != "" {
				files = append(files, e.SourcePath)
			}
		}
	}
	cwd, _ := os.Getwd()
	return qualitybench.FindResults(files, cwd, c.d.RunsDir)
}

func (s *qualityScreen) buildResults() {
	mk := func(none string) []widgets.Item {
		var items []widgets.Item
		if none != "" {
			items = append(items, widgets.Item{Title: none, Value: ""})
		}
		for _, r := range s.results {
			if r.Compare {
				continue
			}
			items = append(items, widgets.Item{Title: r.Label(), Desc: style.Sanitize(r.Path), Value: r.Path})
		}
		return items
	}
	keep := func(l *widgets.List, items []widgets.Item) {
		cur := l.Cursor
		*l = widgets.List{Items: items, Filterable: true, Cursor: min(cur, max(0, len(items)-1))}
	}
	keep(&s.refs, mk("None: measure only"))
	keep(&s.cmpRef, mk(""))
	keep(&s.cmpTest, mk(""))
	if len(s.cmpTest.Items) > 1 && s.cmpTest.Cursor == s.cmpRef.Cursor {
		s.cmpTest.Cursor = 1
	}
}

func listPath(l *widgets.List) string {
	if it, ok := l.Selected(); ok {
		if p, isPath := it.Value.(string); isPath {
			return p
		}
	}
	return ""
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
	scan, sources, find := s.scan, s.sources, s.findResults
	return Batch(func() Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return qbScanned{scan(ctx)}
	}, func() Msg { return qbSources{sources(c)} }, func() Msg { return qbResults{find(c)} })
}

// refreshResults rescans local results (for Compare and the Rent wizard).
func (s *qualityScreen) refreshResults(c *Console) Cmd {
	s.ensure()
	find := s.findResults
	return func() Msg { return qbResults{find(c)} }
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
	if s.mode.Choice == modeCompare {
		return []qsec{qsMode, qsCmpRef, qsCmpTest, qsThresholds, qsSubmit}
	}
	out := []qsec{qsMode, qsScope}
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
		out = append(out, qsTokenizer)
	} else {
		out = append(out, qsEngine, qsSearch, qsVastModel)
		if pickedOther(&s.vmods) {
			out = append(out, qsModelID)
		}
	}
	out = append(out, qsThinking, qsReference)
	if listPath(&s.refs) != "" {
		out = append(out, qsThresholds)
	}
	return append(out, qsNotes, qsKey, qsSubmit)
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
	case qsMode:
		return &s.mode
	case qsTokenizer:
		return &s.tokenizer
	case qsThresholds:
		return &s.thresholds
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
	case qsReference:
		return &s.refs
	case qsCmpRef:
		return &s.cmpRef
	case qsCmpTest:
		return &s.cmpTest
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
		return s.quietEstimate()
	case qbSources:
		s.cands = v.ms
		s.buildVastModels()
		return nil
	case qbResults:
		s.results = v.rs
		s.buildResults()
		return nil
	case qbEstimated:
		if !v.forRun {
			s.scopeBusy = false
			s.scopeEst, s.scopeKey = &v.est, v.key
			return s.quietEstimate()
		}
		if !s.estimating {
			return nil // cancelled
		}
		s.estimating, s.estCancel = false, nil
		if v.err != nil {
			c.setFlash("estimate: "+v.err.Error(), true)
			return nil
		}
		if s.runKey() == v.key {
			s.scopeEst, s.scopeKey = &v.est, v.key
		}
		return s.approve(c, v.opts, v.est)
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
			if s.estimating {
				if v.Code == input.Esc || v.Ctrl && v.Rune == 'x' {
					s.estCancel()
					s.estimating, s.estCancel = false, nil
					c.setFlash("not started", false)
				}
				return nil
			}
			return Batch(s.formKey(c, v), s.quietEstimate())
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
	out := fmt.Sprintf("%d task", len(sc.Tasks))
	if len(sc.Tasks) != 1 {
		out += "s"
	}
	if s.scopeEst != nil && s.scopeKey == s.runKey() && s.scopeEst.FromTool {
		return out + " · " + s.scopeEst.Headline()
	}
	mins, un := qualitybench.EstimateMinutes(sc.Tasks, sc.Tier == qualitybench.TierQuick)
	if s.thinking.Value() == "on" {
		mins *= int(qualitybench.ThinkingFactor)
		out += fmt.Sprintf(" · about %s with thinking on (a guess)", humanMinutes(mins))
	} else {
		out += fmt.Sprintf(" · about %s on the reference run", humanMinutes(mins))
	}
	if un > 0 {
		out += fmt.Sprintf(", +%d untimed", un)
	}
	if s.local() {
		out += "; Run measures this server first"
	}
	return out
}

// thinkingArg is "", "off" or "on" for the Thinking row.
func (s *qualityScreen) thinkingArg() string {
	if s.thinking.Choice > 0 {
		return s.thinking.Value()
	}
	return ""
}

// runKey names what an estimate is for: server, model, scope, thinking.
// Empty when the form does not name a local run yet.
func (s *qualityScreen) runKey() string {
	if !s.local() || s.mode.Choice != modeRun {
		return ""
	}
	sc, err := s.scopeOf()
	if err != nil {
		return ""
	}
	ep, model, err := s.localTarget()
	if err != nil {
		return ""
	}
	return strings.Join([]string{ep, model, string(sc.Tier), strings.Join(sc.Tasks, ","), s.thinkingArg()}, "|")
}

// quietEstimate refreshes the Scope row's estimate from history (no
// requests to the server: probing would load the model) when the form
// names a different run than the last estimate. One runs at a time.
func (s *qualityScreen) quietEstimate() Cmd {
	key := s.runKey()
	if key == "" || key == s.scopeKey || s.scopeBusy || s.estimateFn == nil {
		return nil
	}
	sc, _ := s.scopeOf()
	ep, model, _ := s.localTarget()
	o := qualitybench.RunOptions{Endpoint: ep, Model: model, Scope: sc, Thinking: s.thinkingArg(), APIKey: s.apikey.Input.String()}
	s.scopeBusy = true
	est := s.estimateFn
	return func() Msg {
		return qbEstimated{key: key, est: est(context.Background(), o, true)}
	}
}

// approve shows the estimate and asks, unless qualitybench.confirm says not
// to; then the run starts.
func (s *qualityScreen) approve(c *Console, o qualitybench.RunOptions, e qualitybench.Estimate) Cmd {
	conf, src, err := config.QualityConfirm()
	if err != nil {
		c.setFlash("qualitybench.confirm ("+src+"): "+err.Error()+"; asking", true)
	}
	if !conf.Ask(e.Upper()) {
		return s.begin(c, o, e)
	}
	thinking := o.Thinking
	if thinking == "" {
		thinking = "server default"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s on %s · %s · thinking %s\n\n", o.Model, o.Endpoint, o.Scope.Tier, thinking)
	fmt.Fprintf(&b, "%s.\n", capitalize(e.Headline()))
	if line := e.TaskLine(); line != "" {
		fmt.Fprintf(&b, "  %s\n", line)
	}
	if e.SetupMin > 0 {
		fmt.Fprintf(&b, "First run: + about %s of setup.\n", qualitybench.Minutes(e.SetupMin))
	}
	if e.Warning != "" {
		fmt.Fprintf(&b, "(%s)\n", e.Warning)
	}
	b.WriteString("\n\"Don't ask again\" starts now and turns this question off (Settings turns it back on).")
	c.confirm(&widgets.Dialog{Title: "Start QualityBench?", Body: b.String(),
		Buttons: []string{"Start", "Start, don't ask again", "Cancel"}}, func(ch int) Cmd {
		switch ch {
		case 0:
			return s.begin(c, o, e)
		case 1:
			if err := config.Set(config.KeyQualityConfirm, "never"); err != nil {
				c.setFlash("could not save the setting: "+err.Error(), true)
			} else {
				c.setFlash("QualityBench runs now start without asking; Settings changes it", false)
			}
			return s.begin(c, o, e)
		}
		c.setFlash("not started", false)
		return nil
	})
	return nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
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
	if s.mode.Choice == modeCompare {
		return s.submitCompare(c)
	}
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
		thinking := ""
		if s.thinking.Choice > 0 {
			thinking = s.thinking.Value()
		}
		st := vast.Step{Type: vast.StepQualityBench, Quick: scope.Tier == qualitybench.TierQuick, Thinking: thinking,
			Reference: listPath(&s.refs), Thresholds: s.thresholds.Value(), Publish: true}
		if s.scope.Choice == scopeCustom {
			st.Tasks = scope.Tasks
		}
		return c.rentWith(m.ID, eng, []vast.Step{st})
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
	tok := s.tokenizer.Value()
	if tok == "" {
		tok = s.guessTokenizer()
	}
	ref := listPath(&s.refs)
	opts := qualitybench.RunOptions{Endpoint: endpoint, Model: model, Out: out, Scope: scope, Thinking: thinking,
		Notes: notes, APIKey: s.apikey.Input.String(), Tokenizer: tok, Reference: ref}
	if ref != "" {
		opts.Thresholds = s.thresholds.Value()
	}
	return s.checkTime(c, opts)
}

// checkTime is the step after Run: ask the tool how long this takes (it
// measures the server), then approve.
func (s *qualityScreen) checkTime(c *Console, o qualitybench.RunOptions) Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	s.estimating, s.estCancel = true, cancel
	key, est := s.runKey(), s.estimateFn
	return func() Msg {
		e := est(ctx, o, false)
		var err error
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return qbEstimated{key: key, est: e, forRun: true, opts: o, err: err}
	}
}

// begin starts the run the user approved.
func (s *qualityScreen) begin(c *Console, opts qualitybench.RunOptions, e qualitybench.Estimate) Cmd {
	// The tool would ask on its own terminal; L80 has asked. Only 0.2.0+,
	// which answered the estimate, knows --yes.
	opts.Yes = e.FromTool
	scope := opts.Scope
	s.prog = newQBProgress(scope.Tasks)
	s.prog.tr.SetEstimate(e)
	s.job = newJob("QualityBench " + opts.Model)
	s.job.status = s.prog.status
	s.phase, s.result, s.summary, s.url, s.comparing = 1, nil, nil, "", false
	prog := s.prog
	return c.start(s.job, func(ctx context.Context, w io.Writer) (any, int, error) {
		// Setup is idempotent: seconds once done, minutes the first time
		// (harness environments, the LiveCodeBench image, datasets).
		pw := &progressWriter{out: w, p: prog}
		if code, apiErr := app.SetupQuality(ctx, scope.Tasks, "", app.WriterSink{W: pw}); apiErr != nil {
			return nil, 1, fmt.Errorf("%s: %s — %s", apiErr.Code, apiErr.Message, apiErr.Remedy)
		} else if code != 0 {
			return nil, code, fmt.Errorf("QualityBench setup exited with status %d; see the log", code)
		}
		res, apiErr := app.RunQuality(ctx, opts, app.WriterSink{W: pw})
		pw.flush()
		if apiErr != nil {
			return nil, 1, fmt.Errorf("%s: %s — %s", apiErr.Code, apiErr.Message, apiErr.Remedy)
		}
		return &res, 0, nil
	})
}

// guessTokenizer is the tokenizer the selected server reports for the model.
func (s *qualityScreen) guessTokenizer() string {
	ep, ok := s.selectedEndpoint()
	if !ok {
		return ""
	}
	id := ""
	if it, ok := s.mods.Selected(); ok {
		id, _ = it.Value.(string)
	}
	for _, m := range ep.Models {
		if m.ID == id {
			return m.Tokenizer()
		}
	}
	return ""
}

// compareOut names the comparison after the runs, using a rental step's
// folder name when the file is just results.json.
func compareOut(ref, test string) string {
	stem := func(p string) string {
		b := strings.TrimSuffix(filepath.Base(p), ".json")
		if b == "results" {
			b = filepath.Base(filepath.Dir(p))
		}
		return b
	}
	return filepath.Join(filepath.Dir(test), stem(test)+"-vs-"+stem(ref)+".json")
}

func (s *qualityScreen) submitCompare(c *Console) Cmd {
	ref, test := listPath(&s.cmpRef), listPath(&s.cmpTest)
	switch {
	case ref == "" || test == "":
		c.setFlash("pick a reference and a test run; runs show here once their .qb folder is beside them", true)
		return nil
	case ref == test:
		c.setFlash("pick two different runs", true)
		return nil
	}
	out := compareOut(ref, test)
	th := s.thresholds.Value()
	s.prog = nil
	s.job = newJob("QualityBench compare")
	s.phase, s.result, s.summary, s.url, s.comparing = 1, nil, nil, "", true
	return c.start(s.job, func(ctx context.Context, w io.Writer) (any, int, error) {
		code, apiErr := app.CompareQuality(ctx, ref, test, th, out, app.WriterSink{W: w})
		if apiErr != nil {
			return nil, 1, fmt.Errorf("%s: %s — %s", apiErr.Code, apiErr.Message, apiErr.Remedy)
		}
		if code != 0 {
			return nil, code, fmt.Errorf("qualitybench compare exited with status %d; see the log", code)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			return nil, 1, err
		}
		return &app.QualityResult{Path: out, Report: qualitybench.ReportPath(out), Data: data}, 0, nil
	})
}

func qualitySummary(r *app.QualityResult) []string {
	out := []string{"results " + r.Path}
	for _, p := range r.Partial {
		out = append(out, "partial: "+p)
	}
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
		body := "Publish this run to launch80 as a public page?\n\nThe endpoint URL and hostname are removed first. The page is self-reported."
		if s.comparing {
			body = "Publish this comparison to launch80 as a public page? It is the reference run's page with the test run drawn as an arm."
		}
		if s.result != nil && len(s.result.Partial) > 0 {
			body += "\n\nThis run is partial (" + strings.Join(s.result.Partial, "; ") + "); the page lists what is missing."
		}
		c.confirm(&widgets.Dialog{Title: "Publish", Body: body,
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

// qbProgress draws a run from the tool's qb:progress lines: an overall bar
// with the time left, then one bar per task. The tracker does the counting.
type qbProgress struct {
	tr *qualitybench.Tracker
}

func newQBProgress(tasks []string) *qbProgress {
	return &qbProgress{tr: qualitybench.NewTracker(tasks)}
}

const progressPrefix = qualitybench.ProgressPrefix

func (p *qbProgress) parse(line string) bool { return p.tr.Parse(line) }

// status is the job title's live part: "48% · 23 min left".
func (p *qbProgress) status() string {
	o := p.tr.Overall()
	if o.Current == nil && o.Fraction == 0 {
		return ""
	}
	s := fmt.Sprintf("%d%%", int(o.Fraction*100))
	if o.Known {
		s += " · " + qualitybench.Duration(o.Left) + " left"
	}
	return s
}

func (p *qbProgress) view(t widgets.Theme, w int) []string {
	o := p.tr.Overall()
	barW := max(8, min(30, w-52))
	var out []string
	left := t.R(t.Muted, "working out the time left…")
	if o.Known {
		st := t.Accent
		if len(o.Cut) > 0 {
			st = t.Warn
		}
		left = t.R(st, qualitybench.Duration(o.Left)+" left")
		var meta []string
		if o.EstTotal > 0 {
			meta = append(meta, "est. "+strings.TrimSuffix(qualitybench.Duration(o.EstTotal), " min"))
		}
		if o.Elapsed > 0 {
			meta = append(meta, "running "+strings.TrimSuffix(qualitybench.Duration(o.Elapsed), " min"))
		}
		if len(meta) > 0 {
			left += t.R(t.Muted, "  ("+strings.Join(meta, ", ")+")")
		}
	}
	pct := fmt.Sprintf("%3d%%", int(o.Fraction*100))
	out = append(out, style.Truncate(t.R(t.Title, style.PadRight("Overall", 20))+widgets.Progress(t, o.Fraction, barW)+" "+style.PadRight(pct, 11)+left, w, "…"))
	if len(o.Cut) > 0 {
		out = append(out, style.Truncate("  "+t.R(t.Warn, "the deadline will cut "+strings.Join(qualitybench.Labels(o.Cut), ", ")+": they will be listed as partial"), w, "…"))
	}
	for _, r := range p.tr.Rows() {
		label := r.ID
		if tk, ok := qualitybench.Lookup(r.ID); ok {
			label = tk.Label
		}
		frac := 0.0
		if r.Total > 0 {
			frac = float64(r.Done) / float64(r.Total)
		}
		count := "—"
		if r.Total > 0 {
			count = fmt.Sprintf("%d/%d", r.Done, r.Total)
		}
		st := t.Muted
		state := r.State
		switch r.State {
		case "running", "scoring":
			st = t.Accent
			if d, ok := p.tr.TaskLeft(r.ID); ok && r.State == "running" {
				state += " · " + qualitybench.Duration(d) + " left"
			}
		case "scored":
			st = t.OK
			if r.Total > 0 {
				state = fmt.Sprintf("scored %.1f%%", 100*float64(r.Correct)/float64(r.Total))
			}
		case "partial", "failed":
			st = t.Warn
		case "queued":
			if d, ok := p.tr.TaskLeft(r.ID); ok {
				state += " · ~" + qualitybench.Duration(d)
			}
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
		case qsMode:
			choice(q, &s.mode, "")
		case qsTokenizer:
			if g := s.guessTokenizer(); g != "" {
				s.tokenizer.Input.Placeholder = "auto: " + g
			} else {
				s.tokenizer.Input.Placeholder = "set it if the served id is not a Hugging Face repo"
			}
			text(q, &s.tokenizer)
		case qsReference:
			picker(q, "Compare to", &s.refs, min(8, 2*len(s.refs.Items)))
		case qsCmpRef, qsCmpTest:
			name, l := "Reference", &s.cmpRef
			if q == qsCmpTest {
				name, l = "Test", &s.cmpTest
			}
			if len(l.Items) == 0 {
				out = append(out, label(q, name)+t.R(t.Warn, "no QualityBench results with a .qb folder found here yet"))
				continue
			}
			picker(q, name, l, min(8, 2*len(l.Items)))
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
			switch {
			case s.mode.Choice == modeCompare:
				name = "Compare"
			case !s.local():
				name = "Set up the rental"
			}
			btn := "[ " + name + " ]"
			if s.estimating {
				out = append(out, "", strings.Repeat(" ", lw)+t.R(t.Accent, widgets.Spin(c.tick)+" Checking how long this will take…")+t.R(t.Muted, "  (esc cancels)"))
				continue
			}
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
	case qsReference:
		help = "Optional. A finished run of the same tier and tokenizer: this run is then also compared with it, and the result is the reference's page with this run as an arm."
	case qsCmpRef:
		help = "The baseline, e.g. the BF16 run. Results from this directory, rental step folders and your publishes; each needs its .qb folder."
	case qsCmpTest:
		help = "The run to judge against the reference, e.g. the INT4 build. Both must be the same tier (full or quick)."
	case qsSubmit:
		switch {
		case s.mode.Choice == modeCompare:
			help = "Writes <test>-vs-<reference>.json and .html beside the test run; p publishes it, o opens the report."
		case s.local():
			help = "Sets QualityBench up if needed, then runs it against the server; results and an HTML report go to the current directory."
		default:
			help = "Opens Rent with this model and a QualityBench step filled in. You review the GPU and the cost there; nothing is rented until you confirm."
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
