package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/qualitybench"
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
	"github.com/launch80/L80-CLI/internal/vast"
)

// The rental wizard: Model → Hardware → Operations → Review. Everything a
// rental does is set here; the spec it writes to the specs folder is the
// record, and what `L80 vast run --spec` reruns.

const (
	pgModel = iota
	pgHardware
	pgOps
	pgReview
	nPages
)

var pageNames = []string{"Model", "Hardware", "Operations", "Review"}

// Presets tick the operations; Custom leaves them as they are.
var presets = []string{"Speed + quality", "Quality baseline", "Speed only", "Compare to a run", "Custom"}

type rentWizard struct {
	page  int
	forms [nPages]widgets.Form
	// refPaths backs the Reference choice field: index 0 is "none".
	refPaths   []string
	lastPreset int
	// When the QualityBench screen hands over a step, it is kept as is.
	handed []vast.Step
}

func (r *rentWizard) init() {
	d := vast.DefaultSpec()
	num := func(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
	text := func(key, label, help, ph, val string) *widgets.Field {
		f := &widgets.Field{Key: key, Label: label, Help: help, Input: widgets.TextInput{Placeholder: ph}}
		f.Input.Set(val)
		return f
	}
	r.forms[pgModel] = widgets.Form{Submit: "Next: hardware", Fields: []*widgets.Field{
		text("model", "Model (HF repo)", "The Hugging Face repo with the weights, e.g. Qwen/Qwen3-8B. For llamacpp, a GGUF repo.", "Qwen/Qwen3-8B", ""),
		text("id", "Served as", "The model id the endpoint answers to at /v1. Leave empty to use the repo name.", "same as the repo", ""),
		{Key: "engine", Label: "Engine", Kind: widgets.ChoiceField, Choices: vast.Engines,
			Help: "vllm serves safetensors (and GPTQ/AWQ) weights; llamacpp serves GGUF."},
		text("context", "Context length", "max_model_len for the engine. QualityBench's needle task wants 131072 for its long half; a shorter context skips it and the page says so. 0 leaves the engine's default.", "", strconv.Itoa(d.MaxModelLen)),
		text("toolparser", "Tool parser", "vLLM --tool-call-parser, e.g. hermes (Qwen), llama3_json, mistral. QualityBench's bfcl scores the server's own parser; without one bfcl fails.", "none", ""),
		text("reasoning", "Reasoning parser", "vLLM --reasoning-parser, e.g. qwen3, deepseek_r1. Needed for a QualityBench run with thinking on.", "none", ""),
	}}
	r.forms[pgHardware] = widgets.Form{Submit: "Next: operations", Fields: []*widgets.Field{
		text("gpu", "GPU types", "Vast.ai GPU names, comma-separated, exactly as `s` (search offers) lists them, e.g. RTX 4090, A100 SXM4, H100 SXM. The cheapest matching offer is rented. Empty means any GPU that fits the memory and price.", "any", ""),
		text("count", "GPU count", "GPUs on the box. With vllm, more than one also sets tensor parallelism.", "", strconv.Itoa(d.GPUCount)),
		text("vram", "Min GPU memory", "Total GPU memory in GB across the GPUs. Rule of thumb: the weights' size on disk plus 20% for the KV cache.", "", num(d.MinGPURAMGB)),
		text("dph", "Max $/hr", "Price ceiling. No offer above this is rented.", "", num(d.MaxDPH)),
		text("disk", "Disk GB", "Instance disk. It must hold the weights plus the engine image.", "", strconv.Itoa(d.DiskGB)),
	}}
	r.forms[pgOps] = widgets.Form{Submit: "Next: review", Fields: []*widgets.Field{
		{Key: "preset", Label: "Preset", Kind: widgets.ChoiceField, Choices: presets,
			Help: "Speed + quality: BetterBench, then QualityBench with thinking off. Quality baseline: QualityBench only, to compare later runs against. Compare to a run: QualityBench against a finished run (pick it under Reference). Custom: tick what you want below."},
		{Key: "bb", Label: "BetterBench", Kind: widgets.ToggleField, On: true, Help: "Speed: decode, prefill and concurrency, published as a BetterBench page."},
		{Key: "bbquick", Label: "  quick", Kind: widgets.ToggleField, Help: "A short BetterBench pass: cheaper, less precise; the page says so."},
		{Key: "qboff", Label: "QualityBench", Kind: widgets.ToggleField, On: true, Help: "Quality with thinking off (or the server's default when the model has no thinking mode): GSM8K, IFEval, MMLU-Pro, HumanEval+/MBPP+, BFCL, LiveCodeBench, logprobs, needle. Code tasks run in Docker (or podman) on this machine."},
		{Key: "qbon", Label: "  + thinking on", Kind: widgets.ToggleField, Help: "A second QualityBench run with thinking forced on. Needs the reasoning parser on the Model page. Much slower: replies are long."},
		{Key: "qbscope", Label: "  scope", Kind: widgets.ChoiceField, Choices: []string{"Full", "Quick"}, Help: "Quick runs a fixed tenth of each task: a smoke test, and the page says so. Quick runs compare only with quick runs."},
		text("qbtasks", "  tasks", "Comma-separated task ids (see Bench Quality), or empty for the whole default suite.", "the default suite", ""),
		{Key: "ref", Label: "  reference", Kind: widgets.ChoiceField, Choices: []string{"none"},
			Help: "A finished QualityBench run (same tier and tokenizer) to compare against: the page then shows this run as an arm of the reference, with paired deltas, KLD and threshold verdicts."},
		text("thresholds", "  thresholds", "A pre-registered thresholds file for the compare; empty uses QualityBench's default bars.", "default bars", ""),
		text("deadline", "  time budget", "Per QualityBench run, e.g. 3h: no new work starts after it and cut tasks are listed as partial. Empty: only the run's max hours.", "none", ""),
		{Key: "publish", Label: "Publish pages", Kind: widgets.ToggleField, On: true, Help: "Publish each finished step as a launch80 page (endpoint and host removed). Off keeps everything local; you can publish later from Bench - History or Bench Quality."},
		{Key: "listed", Label: "  List in Directory", Kind: widgets.ToggleField, On: listByDefault(), Help: "Also list the published pages in the launch80 Directory (launch80.com/directory), where anyone can search and compare them. Off: unlisted, reachable only by link; list them later from Bench - History (l)."},
	}}
	r.forms[pgReview] = widgets.Form{Submit: "Rent and run", Fields: []*widgets.Field{
		text("hours", "Max hours", "The hard cap. When it is reached L80 stops the run and destroys the box; QualityBench stops starting work 15 minutes before, so its results are written.", "", ""),
		text("budget", "Budget $", "The run refuses to start if the Vast.ai balance is below this. Empty: the cost cap (max hours × max $/hr).", "the cost cap", ""),
		text("name", "Save as", "The spec's file name, without .yaml. Leave empty to name it after the model.", "from the model", ""),
		{Key: "dry", Label: "Dry run", Kind: widgets.ToggleField, On: true, Help: "Show the offers and the plan without renting anything. Turn off to rent."},
	}}
	r.lastPreset = -1
}

func (r *rentWizard) field(page int, key string) *widgets.Field {
	for _, f := range r.forms[page].Fields {
		if f.Key == key {
			return f
		}
	}
	panic("no field " + key)
}

func (r *rentWizard) val(page int, key string) string { return r.field(page, key).Value() }

// setResults offers local QualityBench results as references.
func (r *rentWizard) setResults(rs []qualitybench.ResultFile) {
	f := r.field(pgOps, "ref")
	cur := ""
	if f.Choice > 0 && f.Choice < len(r.refPaths) {
		cur = r.refPaths[f.Choice]
	}
	r.refPaths, f.Choices, f.Choice = []string{""}, []string{"none"}, 0
	for _, x := range rs {
		if x.Compare {
			continue
		}
		r.refPaths = append(r.refPaths, x.Path)
		f.Choices = append(f.Choices, x.Label())
		if x.Path == cur {
			f.Choice = len(f.Choices) - 1
		}
	}
}

func (r *rentWizard) reference() string {
	f := r.field(pgOps, "ref")
	if f.Choice > 0 && f.Choice < len(r.refPaths) {
		return r.refPaths[f.Choice]
	}
	return ""
}

// applyPreset ticks the operations for a preset when the choice changes.
func (r *rentWizard) applyPreset() {
	p := r.field(pgOps, "preset").Choice
	if p == r.lastPreset {
		return
	}
	r.lastPreset = p
	on := func(key string, v bool) { r.field(pgOps, key).On = v }
	switch presets[p] {
	case "Speed + quality":
		on("bb", true)
		on("qboff", true)
	case "Quality baseline":
		on("bb", false)
		on("qboff", true)
		r.field(pgOps, "ref").Choice = 0
	case "Speed only":
		on("bb", true)
		on("qboff", false)
		on("qbon", false)
	case "Compare to a run":
		on("bb", false)
		on("qboff", true)
		if r.field(pgOps, "ref").Choice == 0 && len(r.refPaths) > 1 {
			r.field(pgOps, "ref").Choice = 1
		}
	}
}

// steps turns the Operations page into spec steps, in run order.
func (r *rentWizard) steps() ([]vast.Step, error) {
	if len(r.handed) > 0 {
		out := append([]vast.Step{}, r.handed...)
		pub := r.val(pgOps, "publish") == "true"
		for i := range out {
			out[i].Publish = pub
		}
		return out, nil
	}
	pub := r.val(pgOps, "publish") == "true"
	var out []vast.Step
	if r.val(pgOps, "bb") == "true" {
		out = append(out, vast.Step{Type: vast.StepBetterBench, Quick: r.val(pgOps, "bbquick") == "true", Publish: pub})
	}
	var tasks []string
	if t := r.val(pgOps, "qbtasks"); t != "" {
		ts, err := qualitybench.ParseTasks(t)
		if err != nil {
			return nil, fmt.Errorf("QualityBench tasks: %v", err)
		}
		tasks = ts
	}
	qb := func(thinking, label string) vast.Step {
		return vast.Step{Type: vast.StepQualityBench, Label: label, Quick: r.val(pgOps, "qbscope") == "Quick", Tasks: tasks,
			Thinking: thinking, Reference: r.reference(), Thresholds: expandHome(r.val(pgOps, "thresholds")),
			Deadline: r.val(pgOps, "deadline"), Publish: pub}
	}
	if r.val(pgOps, "qboff") == "true" {
		// Off is the baseline. enable_thinking=false is a chat-template switch, so
		// it needs no reasoning parser; a model without thinking ignores it.
		out = append(out, qb("off", "quality"))
	}
	if r.val(pgOps, "qbon") == "true" {
		st := qb("on", "quality-thinking")
		st.Reference = "" // the reference was picked for the thinking-off run
		out = append(out, st)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("tick at least one operation")
	}
	if d := r.val(pgOps, "deadline"); d != "" {
		if _, err := time.ParseDuration(d); err != nil {
			return nil, fmt.Errorf("time budget %q: use a duration such as 3h or 90m", d)
		}
	}
	return out, nil
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, rest)
		}
	}
	return p
}

// spec builds the whole spec, naming the field at fault.
func (r *rentWizard) spec() (vast.Spec, error) {
	sp := vast.Spec{ModelRepo: r.val(pgModel, "model"), ModelID: r.val(pgModel, "id"), Engine: r.val(pgModel, "engine"),
		ToolCallParser: r.val(pgModel, "toolparser"), ReasoningParser: r.val(pgModel, "reasoning"), Name: r.val(pgReview, "name")}
	sp.GPUNames = vast.ParseGPUNames(r.val(pgHardware, "gpu"))
	ints := []struct {
		page       int
		key, label string
		dst        *int
	}{{pgHardware, "count", "GPU count", &sp.GPUCount}, {pgModel, "context", "context length", &sp.MaxModelLen}, {pgHardware, "disk", "disk GB", &sp.DiskGB}}
	for _, f := range ints {
		n, err := strconv.Atoi(r.val(f.page, f.key))
		if err != nil {
			return sp, fmt.Errorf("%s must be a whole number", f.label)
		}
		*f.dst = n
	}
	floats := []struct {
		page       int
		key, label string
		dst        *float64
	}{{pgHardware, "vram", "min GPU memory", &sp.MinGPURAMGB}, {pgHardware, "dph", "max $/hr", &sp.MaxDPH}, {pgReview, "budget", "budget", &sp.BudgetUSD}}
	for _, f := range floats {
		if f.key == "budget" && r.val(f.page, f.key) == "" {
			continue // filled from the cap below
		}
		n, err := strconv.ParseFloat(strings.TrimPrefix(r.val(f.page, f.key), "$"), 64)
		if err != nil {
			return sp, fmt.Errorf("%s must be a number", f.label)
		}
		*f.dst = n
	}
	st, err := r.steps()
	if err != nil {
		return sp, err
	}
	sp.Steps = st
	if r.val(pgReview, "budget") == "" {
		if h, err := r.hours(r.plan(sp)); err == nil {
			sp.BudgetUSD = math.Ceil(h*sp.MaxDPH*100) / 100
		}
	}
	return sp, sp.Validate()
}

// Rough step times for the review: provisioning (image pull, weights
// download, engine start), then each step.
const provisionMinutes = 20

func stepMinutes(st vast.Step) (int, string) {
	switch st.Type {
	case vast.StepBetterBench:
		if st.Quick {
			return 10, "rough"
		}
		return 45, "rough"
	case vast.StepQualityBench:
		tasks := st.Tasks
		if len(tasks) == 0 {
			sc, _ := qualitybench.ResolveScope(st.Quick, "")
			tasks = sc.Tasks
		}
		m, _ := qualitybench.EstimateMinutes(tasks, st.Quick)
		if st.Thinking == "on" {
			return m * 4, "thinking on: replies are long; a guess"
		}
		return m, "measured on " + qualitybench.RefRun
	}
	return 0, ""
}

// plan is the review: per-step minutes, the suggested cap, and the cost.
type plan struct {
	lines    []string
	minutes  int
	capHours float64
}

func (r *rentWizard) plan(sp vast.Spec) plan {
	p := plan{minutes: provisionMinutes}
	p.lines = append(p.lines, fmt.Sprintf("  %-46s %s", "rent, provision, serve", humanMinutes(provisionMinutes)+"  (image pull, weights, engine start)"))
	for i, st := range sp.Steps {
		m, why := stepMinutes(st)
		p.minutes += m
		p.lines = append(p.lines, fmt.Sprintf("  %-46s %s", fmt.Sprintf("%d. %s", i+1, st.Describe()), humanMinutes(m)+"  ("+why+")"))
	}
	p.capHours = math.Ceil(float64(p.minutes)*1.3/30) / 2
	return p
}

func (r *rentWizard) hours(p plan) (float64, error) {
	h := r.val(pgReview, "hours")
	if h == "" {
		return p.capHours, nil
	}
	f, err := strconv.ParseFloat(h, 64)
	if err != nil || f <= 0 {
		return 0, fmt.Errorf("max hours must be a positive number")
	}
	return f, nil
}

// handKey sends a key to the current page; next/back move between pages.
func (r *rentWizard) handKey(k input.Key) (next, back bool) {
	f := &r.forms[r.page]
	if k.Code == input.Esc {
		return false, true
	}
	_, submit := f.HandleKey(k)
	if r.page == pgOps {
		r.applyPreset()
	}
	return submit, false
}

func (r *rentWizard) view(c *Console, w int) []string {
	t := c.th
	var crumbs []string
	for i, n := range pageNames {
		switch {
		case i == r.page:
			crumbs = append(crumbs, t.R(t.Selected, " "+n+" "))
		case i < r.page:
			crumbs = append(crumbs, t.R(t.OK, " ✓ "+n+" "))
		default:
			crumbs = append(crumbs, t.R(t.Muted, " "+n+" "))
		}
	}
	out := []string{strings.Join(crumbs, t.R(t.Muted, "›")), ""}
	switch r.page {
	case pgModel:
		out = append(out, wrapMuted(t, "Which model the rented box serves, and how. Nothing is rented until the review page and a confirmation.", w)...)
	case pgHardware:
		out = append(out, wrapMuted(t, "What to rent. s searches the offers that match (free).", w)...)
	case pgOps:
		if len(r.handed) > 0 {
			out = append(out, t.R(t.Accent, "From QualityBench: ")+style.Sanitize(r.handed[0].Describe()))
			out = append(out, wrapMuted(t, "The operations below are ignored while this step is set; press ctrl+r to clear it.", w)...)
		} else {
			out = append(out, wrapMuted(t, "What runs on the box, in order: BetterBench, then QualityBench. Each step's results are on this machine as soon as it ends.", w)...)
		}
	case pgReview:
		sp, err := r.spec()
		if err != nil {
			out = append(out, t.R(t.Bad, "Fix this first: "+err.Error()), "")
			break
		}
		p := r.plan(sp)
		out = append(out, t.R(t.Title, "Plan"))
		for _, l := range p.lines {
			out = append(out, style.Truncate(style.Sanitize(l), w, "…"))
		}
		hrs, herr := r.hours(p)
		out = append(out, fmt.Sprintf("  %-46s %s", "total", humanMinutes(p.minutes)))
		if herr == nil {
			out = append(out, "", t.R(t.Title, "Cost"),
				fmt.Sprintf("  at most $%.2f: %.1f h cap × $%s/hr ceiling (the offer rented may be cheaper)", hrs*sp.MaxDPH, hrs, strconv.FormatFloat(sp.MaxDPH, 'f', -1, 64)))
			if r.val(pgReview, "hours") == "" {
				out = append(out, t.R(t.Muted, fmt.Sprintf("  max hours is empty: the cap is %.1f h (the plan plus 30%%)", p.capHours)))
			}
		}
		if sp.NeedsLogprobs() && sp.Engine != "vllm" {
			out = append(out, t.R(t.Warn, "  logprobs needs vLLM's prompt_logprobs; on llama.cpp that task fails (the rest still run)"))
		}
		for _, w8 := range contextWarnings(sp) {
			out = append(out, t.R(t.Warn, "  "+w8))
		}
		for _, st := range sp.Steps {
			if st.Type == vast.StepQualityBench && sp.ToolCallParser == "" && (len(st.Tasks) == 0 || containsStr(st.Tasks, "bfcl")) {
				out = append(out, t.R(t.Warn, "  no tool parser: QualityBench's bfcl task will fail (set one on the Model page)"))
				break
			}
		}
		out = append(out, "")
	}
	out = append(out, r.forms[r.page].View(t, w, true)...)
	return out
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func wrapMuted(t widgets.Theme, s string, w int) []string {
	var out []string
	for _, l := range style.Wrap(s, w) {
		out = append(out, t.R(t.Muted, l))
	}
	return append(out, "")
}

// --- run progress --------------------------------------------------------------

// rentProgress is the step list parsed from bbvastl80's `bbvastl80:step`
// lines, plus the running QualityBench step's per-task progress.
type rentProgress struct {
	mu    sync.Mutex
	steps []vast.Step
	state map[int]stepLine
	qb    *qbProgress
	phase string // the last pipeline line worth showing (renting, provisioning…)
	// qbDeadline is when bbvastl80 tells QualityBench to stop starting work
	// (the watchdog less its margin); zero without a watchdog.
	qbDeadline time.Time
}

// qbDeadlineMargin mirrors bbvastl80's DEADLINE_MARGIN_S.
const qbDeadlineMargin = 15 * time.Minute

// status is the rental job title's live part while a QualityBench step runs.
func (p *rentProgress) status() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.steps {
		if s, ok := p.state[i+1]; ok && s.State == "running" {
			out := fmt.Sprintf("step %d/%d", i+1, len(p.steps))
			if s.Type == vast.StepQualityBench && p.qb != nil {
				if q := p.qb.status(); q != "" {
					out += " · QualityBench " + q
				}
			}
			return out
		}
	}
	return ""
}

type stepLine struct {
	Index   int    `json:"index"`
	Type    string `json:"type"`
	State   string `json:"state"`
	Label   string `json:"label"`
	Results string `json:"results"`
	Report  string `json:"report"`
	URL     string `json:"url"`
	Error   string `json:"error"`
}

const stepPrefix = "bbvastl80:step "

func newRentProgress(steps []vast.Step) *rentProgress {
	return &rentProgress{steps: steps, state: map[int]stepLine{}}
}

func (p *rentProgress) parse(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i := strings.Index(line, stepPrefix); i >= 0 {
		var s stepLine
		if json.Unmarshal([]byte(line[i+len(stepPrefix):]), &s) == nil {
			p.state[s.Index] = s
			if s.Type == vast.StepQualityBench && s.State == "running" && s.Index-1 < len(p.steps) {
				st := p.steps[s.Index-1]
				tasks := st.Tasks
				if len(tasks) == 0 {
					sc, _ := qualitybench.ResolveScope(st.Quick, "")
					tasks = sc.Tasks
				}
				p.qb = newQBProgress(tasks)
				if !p.qbDeadline.IsZero() {
					p.qb.tr.SetDeadline(p.qbDeadline)
				}
				if st.Deadline != "" {
					if d, err := time.ParseDuration(st.Deadline); err == nil {
						if at := time.Now().Add(d); p.qbDeadline.IsZero() || at.Before(p.qbDeadline) {
							p.qb.tr.SetDeadline(at)
						}
					}
				}
			}
		}
		return
	}
	for _, pre := range []string{progressPrefix, qualitybench.EstimatePrefix} {
		if i := strings.Index(line, pre); i >= 0 {
			if p.qb != nil {
				p.qb.parse(line[i:])
			}
			return
		}
	}
	l := strings.TrimSpace(line)
	for _, k := range []string{"renting offer", "instance ", "waiting", "provision", "endpoint verified", "destroying", "destroyed"} {
		if strings.Contains(l, k) {
			p.phase = l
			return
		}
	}
}

// done lists the finished steps' outputs.
func (p *rentProgress) outputs() []stepLine {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []stepLine
	for i := 1; i <= len(p.steps); i++ {
		if s, ok := p.state[i]; ok && (s.Results != "" || s.URL != "") {
			out = append(out, s)
		}
	}
	return out
}

func (p *rentProgress) view(t widgets.Theme, w int, tick int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	if p.phase != "" {
		out = append(out, t.R(t.Muted, style.Truncate(style.Sanitize(p.phase), w, "…")))
	}
	for i, st := range p.steps {
		s, ok := p.state[i+1]
		mark, tail := t.R(t.Muted, "·"), ""
		switch {
		case !ok:
		case s.State == "running":
			mark = t.R(t.Accent, widgets.Spin(tick))
		case s.State == "done":
			mark = t.R(t.OK, "✓")
			tail = s.URL
		case s.State == "failed":
			mark = t.R(t.Bad, "✗")
			tail = s.Error
		}
		line := fmt.Sprintf("%s %d. %s", mark, i+1, st.Describe())
		if tail != "" {
			line += t.R(t.Muted, "  "+style.Sanitize(tail))
		}
		out = append(out, style.Truncate(line, w, "…"))
		if ok && s.State == "running" && s.Type == vast.StepQualityBench && p.qb != nil {
			out = append(out, indent(p.qb.view(t, w-4), 4)...)
		}
	}
	return out
}

// rentWriter feeds whole lines to the progress parser on the way to the log.
type rentWriter struct {
	out  interface{ Write([]byte) (int, error) }
	p    *rentProgress
	part []byte
}

func (w *rentWriter) Write(b []byte) (int, error) {
	w.part = append(w.part, b...)
	for {
		i := strings.IndexByte(string(w.part), '\n')
		if i < 0 {
			break
		}
		w.p.parse(string(w.part[:i]))
		w.part = w.part[i+1:]
	}
	return w.out.Write(b)
}

// contextWarnings flags QualityBench work the context length will cut.
func contextWarnings(sp vast.Spec) []string {
	if sp.MaxModelLen == 0 {
		return nil
	}
	var out []string
	needle, thinking := false, false
	for _, st := range sp.Steps {
		if st.Type != vast.StepQualityBench {
			continue
		}
		if len(st.Tasks) == 0 || containsStr(st.Tasks, "needle") {
			needle = true
		}
		if st.Thinking == "on" {
			thinking = true
		}
	}
	if thinking && sp.MaxModelLen < 40960 {
		out = append(out, fmt.Sprintf("context %d: thinking on asks for at least 32768 new tokens; set 65536 or more", sp.MaxModelLen))
	}
	switch {
	case needle && sp.MaxModelLen < 34000:
		out = append(out, fmt.Sprintf("context %d: needle needs 32k (and 128k) prompts and will be skipped; set 131072 to run it all", sp.MaxModelLen))
	case needle && sp.MaxModelLen < 131072:
		out = append(out, fmt.Sprintf("context %d: needle's 128k half will be skipped (listed as partial)", sp.MaxModelLen))
	}
	return out
}

// listByDefault is the List in Directory toggle's starting value: on unless
// publish.public says otherwise.
func listByDefault() bool {
	mode, _, _ := config.PublishListing()
	return mode == config.ListingAlways
}
