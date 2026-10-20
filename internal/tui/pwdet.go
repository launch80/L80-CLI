package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/pwdet"
	"github.com/launch80/L80-CLI/internal/pytool"
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

type pwdetScreen struct {
	form   widgets.Form
	job    *job
	report *pwdet.Report
	path   string
	table  widgets.Table
	shown  []pwdet.Finding // the table's rows
	review bool            // only the findings to review
	method bool            // the Method tab instead of the checks
	scroll int             // the Method tab's first line
	url    string
	phase  int
}

// pwdetLevels are pwdet's assurance-level profiles, as the form offers them.
var pwdetLevels = []string{"basic", "standard", "thorough", "deep"}

func (s *pwdetScreen) init() {
	s.form = widgets.Form{Submit: "Scan", Fields: []*widgets.Field{
		{Key: "model", Label: "Model", Help: "A local directory, a Hugging Face repo (org/name[@revision]), or a served copy (http://host:port/v1#model).",
			Input: widgets.TextInput{Placeholder: "Qwen/Qwen3-0.6B"}},
		{Key: "profile", Label: "Level", Kind: widgets.ChoiceField, Choices: pwdetLevels, Choice: 1,
			Help: "basic: seconds, files and metadata only; it cannot see weight implants. standard: weights compared tensor by tensor with the base. " +
				"thorough: also probes behaviour, serving both models on this machine's GPU (minutes per model). deep: also white-box activations."},
		{Key: "base", Label: "Base", Help: "Optional: the pristine model it was made from, e.g. Qwen/Qwen3-0.6B. Empty: taken from the model card. Standard and above compare against it.",
			Input: widgets.TextInput{Placeholder: "(from the model card)"}},
		{Key: "parent", Label: "Parent", Help: "Optional: the direct upstream when it is not the base (the fine-tune a quantisation was made from).", Input: widgets.TextInput{Placeholder: "(from the model card)"}},
		{Key: "endpoint", Label: "Endpoint", Help: "Optional: the model already served (http://host:port/v1#model). Empty: thorough and deep serve it here.",
			Input: widgets.TextInput{Placeholder: "(serve on this GPU)"}},
		{Key: "offline", Label: "Offline", Kind: widgets.ToggleField, Help: "Use only local files; skip Hugging Face lookups."},
	}}
}

type pwdetScanned struct {
	report *pwdet.Report
	path   string
	err    error
}

type pwdetPublished struct {
	art *api.Artifact
	err *api.Error
}

func (s *pwdetScreen) update(c *Console, m Msg) Cmd {
	switch v := m.(type) {
	case jobDone:
		if v.j != s.job {
			return nil
		}
		s.phase = 2
		if r, ok := v.result.(pwdetScanned); ok {
			if r.err != nil {
				c.setFlash(r.err.Error(), true)
			}
			s.report, s.path, s.review, s.method = r.report, r.path, false, false
			s.fill()
		}
	case pwdetPublished:
		if v.err != nil {
			c.setFlash(v.err.Code+": "+v.err.Message+" — "+v.err.Remedy, true)
		} else {
			s.url = v.art.URL
			msg := "published: " + v.art.URL
			if v.art.TemplateID == pwdet.ScanTemplateID {
				msg += " — verifiable: History, then v"
			}
			c.setFlash(msg, false)
		}
	case PasteMsg:
		if s.phase == 0 {
			s.form.HandlePaste(v)
		}
	case KeyMsg:
		if s.phase == 0 {
			if v.Code == input.Esc {
				c.scr = scrHome
				return nil
			}
			if _, submit := s.form.HandleKey(v); submit {
				return s.scan(c)
			}
			return nil
		}
		if v.Ctrl && v.Rune == 'x' && s.job.running {
			s.job.cancel()
			return nil
		}
		if s.job.running {
			s.job.log.HandleKey(v)
			return nil
		}
		if v.Code == input.PageUp || v.Code == input.PageDown || v.Code == input.End {
			s.job.log.HandleKey(v)
			return nil
		}
		if s.method && s.report != nil {
			switch {
			case v.Code == input.Up:
				s.scroll = max(0, s.scroll-1)
			case v.Code == input.Down:
				s.scroll++
			case v.Code == input.Rune && v.Rune == 'm', isBack(v):
				s.method = false
			}
			return nil
		}
		if s.table.HandleKey(v) {
			return nil
		}
		switch {
		case v.Code == input.Rune && v.Rune == 'm' && s.report != nil:
			s.method, s.scroll = true, 0
		case v.Code == input.Rune && v.Rune == 'p' && s.report != nil && s.url == "":
			return s.publish(c, false)
		case v.Code == input.Rune && v.Rune == 'a' && s.report != nil:
			s.review = !s.review
			s.fill()
		case v.Code == input.Rune && v.Rune == 'n':
			s.phase, s.report, s.url = 0, nil, ""
		case isBack(v):
			s.phase = 0
		}
	}
	return nil
}

// fill puts the report's checks in the table: every check, the ones to
// review first, or only those to review.
func (s *pwdetScreen) fill() {
	s.table = widgets.Table{Columns: []widgets.Column{{Title: "Status", Width: 7}, {Title: "Severity", Width: 8}, {Title: "Check", Width: 27}, {Title: "Observation"}}}
	s.shown = nil
	if s.report == nil {
		return
	}
	if s.review {
		s.shown = s.report.ToReview()
	} else {
		s.shown = s.report.Ordered()
	}
	for _, f := range s.shown {
		s.table.Rows = append(s.table.Rows, []string{statusMark[f.Status] + " " + f.Status, f.EffectiveSeverity(), f.CheckID, strings.Join(strings.Fields(f.Title), " ")})
	}
}

var statusMark = map[string]string{"pass": "✓", "warn": "!", "fail": "✗", "skipped": "–", "error": "?"}

func (s *pwdetScreen) scan(c *Console) Cmd {
	vals := s.form.Values()
	if vals["model"] == "" {
		c.setFlash("a model is required", true)
		return nil
	}
	cwd, _ := os.Getwd()
	args := []string{"scan", vals["model"], "--profile", vals["profile"], "--out", filepath.Join(cwd, "reports"), "--json-only"}
	for _, k := range []string{"base", "parent", "endpoint"} {
		if v := strings.TrimSpace(vals[k]); v != "" {
			args = append(args, "--"+k, v)
		}
	}
	if vals["offline"] == "true" {
		args = append(args, "--offline")
	}
	s.job = newJob("pwdet scan " + vals["model"])
	s.phase, s.report, s.url = 1, nil, ""
	return c.start(s.job, func(ctx context.Context, out io.Writer) (any, int, error) {
		t, err := pytool.Lookup("pwdet")
		if err != nil {
			return nil, -1, err
		}
		r, err := pytool.Resolve(t)
		if err != nil {
			return nil, -1, err
		}
		fmt.Fprintf(out, "$ %s\n", pytool.FormatArgv(append(append([]string{}, r.Argv...), args...)))
		var stdout bytes.Buffer
		code, err := pytool.Exec(ctx, r, args, pytool.ExecOpts{Stdout: io.MultiWriter(&stdout, out), Stderr: out})
		if err != nil {
			return nil, code, err
		}
		var sum struct {
			Report string `json:"report"`
		}
		lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
		if json.Unmarshal([]byte(lines[len(lines)-1]), &sum) != nil || sum.Report == "" {
			return pwdetScanned{err: fmt.Errorf("pwdet exited %d without a report", code)}, code, nil
		}
		data, rerr := os.ReadFile(sum.Report)
		if rerr != nil {
			return pwdetScanned{err: rerr}, code, nil
		}
		rep, perr := pwdet.Parse(data)
		return pwdetScanned{report: rep, path: sum.Report, err: perr}, 0, nil
	})
}

func (s *pwdetScreen) publish(c *Console, confirmed bool) Cmd {
	if !c.d.Config.HasToken() {
		c.setFlash("publishing needs a token: set L80_TOKEN or ~/.config/L80/credentials.json", true)
		return nil
	}
	if !confirmed {
		c.confirm(&widgets.Dialog{Title: "Publish", Buttons: []string{"Publish", "Cancel"}, Choice: 1,
			Body: "Publish this scan to launch80 as a public page?\n\nThe hostname, local paths and private endpoints are removed. A clean verdict means these checks found nothing, not that the model is safe."},
			func(ch int) Cmd {
				if ch == 0 {
					return s.publish(c, true)
				}
				return nil
			})
		return nil
	}
	path := s.path
	return func() Msg {
		raw, err := os.ReadFile(path)
		if err != nil {
			return pwdetPublished{err: api.Newf("E_INPUT_INVALID", "Check the report is still where pwdet wrote it.", "%v", err)}
		}
		pg, err := app.BuildPwdetPage(raw, pwdet.Options{}, app.ServerHasTemplate(c.d.Client, pwdet.ScanTemplateID))
		if err != nil {
			return pwdetPublished{err: api.Newf("E_INPUT_INVALID", "Publish from the command line to see details.", "%v", err)}
		}
		pub, apiErr := app.Publish(c.d.Client, app.PublishRequest{Body: pg.Body, Template: pg.Template, Title: pg.Title, SourcePath: path, APIBase: c.d.Config.BaseURL})
		if apiErr != nil {
			return pwdetPublished{err: apiErr}
		}
		return pwdetPublished{art: pub.Artifact}
	}
}

func (s *pwdetScreen) view(c *Console, w, h int) ([]string, []widgets.Binding) {
	t := c.th
	if s.phase == 0 {
		out := []string{t.R(t.Title, "Check a model for tampered or poisoned weights"),
			t.R(t.Muted, "Runs the Poisoned Weights Detector pinned in this L80. Reports go to ./reports."), ""}
		out = append(out, s.form.View(t, w, true)...)
		return out, []widgets.Binding{{Keys: "tab", Desc: "next field"}, {Keys: "enter", Desc: "next / scan"}, {Keys: "esc", Desc: "back"}}
	}
	out := []string{jobHeader(c, s.job, w)}
	if s.report != nil {
		return s.reportView(c, out, w, h)
	}
	out = append(out, s.job.log.View(t, w, max(3, h-len(out)))...)
	keys := []widgets.Binding{{Keys: "pgup/pgdn", Desc: "scroll"}}
	if s.job.running {
		keys = append(keys, widgets.Binding{Keys: "ctrl+x", Desc: "stop"})
	} else {
		keys = append(keys, widgets.Binding{Keys: "n", Desc: "new scan"}, widgets.Binding{Keys: "esc", Desc: "back"})
	}
	return out, keys
}

// reportView shows the whole report inline: verdict, coverage by layer,
// every check, and the selected check's evidence and remediation.
func (s *pwdetScreen) reportView(c *Console, out []string, w, h int) ([]string, []widgets.Binding) {
	t, r := c.th, s.report
	label := "no verdict"
	var rationale []string
	if r.Verdict != nil {
		label, rationale = r.Verdict.Label, r.Verdict.Rationale
	}
	st := t.OK
	switch label {
	case "suspicious", "inconclusive":
		st = t.Warn
	case "likely_tampered", "malicious":
		st = t.Bad
	}
	subject := r.Subject.Label()
	if rev := r.Subject.Revision; rev != "" {
		subject += " @ " + rev[:min(12, len(rev))]
	}
	verdict := strings.ToUpper(strings.ReplaceAll(label, "_", " "))
	if lvl := r.Level(); lvl != "" {
		verdict += " · " + strings.ToUpper(lvl)
	}
	out = append(out, "", "  Verdict: "+t.R(st, verdict)+
		t.R(t.Muted, clip("   "+subject+" · profile "+r.Profile+" · pwdet "+r.ToolVersion, w-11-style.Width(verdict))))
	for _, l := range rationale {
		out = append(out, t.R(t.Muted, "  "+clip(style.Sanitize(l), w-2)))
	}
	if s.method {
		return s.methodView(c, out, w, h)
	}
	out = append(out, threatLines(c, r, w)...)
	if s.url != "" {
		out = append(out, t.R(t.OK, "  published: "+s.url))
	}
	out = append(out, "")
	out = append(out, coverageLines(c, r, w)...)

	title := fmt.Sprintf("  Checks (%d)", len(r.Findings))
	if s.review {
		title = fmt.Sprintf("  Findings to review (%d of %d checks)", len(s.shown), len(r.Findings))
	}
	out = append(out, "", t.R(t.Title, title))
	detail := s.detail(c, w)
	foot := []string{t.R(t.Muted, "  report "+clip(relPath(s.path), w-10))}
	if len(s.shown) == 0 {
		out = append(out, t.R(t.OK, "  No check reported a warning or a failure."))
	} else {
		rows := max(3, h-len(out)-len(detail)-len(foot)-1)
		out = append(out, s.table.View(t, w, rows, true)...)
	}
	out = append(out, "")
	out = append(out, detail...)
	out = append(out, foot...)

	keys := []widgets.Binding{{Keys: "↑↓", Desc: "checks"}, {Keys: "m", Desc: "method"}}
	if s.review {
		keys = append(keys, widgets.Binding{Keys: "a", Desc: "all checks"})
	} else {
		keys = append(keys, widgets.Binding{Keys: "a", Desc: "only to review"})
	}
	if s.url == "" {
		keys = append(keys, widgets.Binding{Keys: "p", Desc: "publish"})
	}
	return style.Fit(out, w, h), append(keys, widgets.Binding{Keys: "n", Desc: "new scan"}, widgets.Binding{Keys: "esc", Desc: "back to form"})
}

var threatMark = map[string]string{"tested": "✓", "partial": "~", "not_tested": "✗"}

// threatLines is the threat coverage matrix on one or two lines: which
// kinds of tampering this scan could see at all.
func threatLines(c *Console, r *pwdet.Report, w int) []string {
	t := c.th
	if r.Assurance == nil || len(r.Assurance.Threats) == 0 {
		return nil
	}
	var cells []string
	for _, th := range r.Assurance.Threats {
		st := t.OK
		switch th.State {
		case "partial":
			st = t.Warn
		case "not_tested":
			st = t.Bad
		}
		cells = append(cells, t.R(st, threatMark[th.State]+" "+th.Title))
	}
	out := []string{}
	line := "  Tested: "
	for _, cell := range cells {
		if style.Width(line)+style.Width(cell)+3 > w && strings.TrimSpace(line) != "Tested:" {
			out = append(out, line)
			line = "          "
		}
		line += cell + "   "
	}
	out = append(out, line)
	if a := r.Assurance.AccuracyText; a != "" {
		out = append(out, t.R(t.Muted, "  "+clip(style.Sanitize(a), w-2)))
	}
	return out
}

// methodView is the Method tab: how pwdet works, what this scan tested and
// did not, and the limits a reader should know before trusting a verdict.
func (s *pwdetScreen) methodView(c *Console, out []string, w, h int) ([]string, []widgets.Binding) {
	t := c.th
	var body []string
	para := func(st style.Style, text string) {
		for _, l := range style.Wrap(style.Sanitize(text), w-4) {
			body = append(body, "  "+t.R(st, l))
		}
	}
	head := func(s string) { body = append(body, "", t.R(t.Title, "  "+s)) }
	for _, sec := range methodSections(s.report) {
		head(sec.title)
		for _, p := range sec.paras {
			para(t.Text, p)
		}
		for _, b := range sec.bullets {
			st := t.Muted
			if strings.HasPrefix(b, "✗") {
				st = t.Warn
			}
			for i, l := range style.Wrap(style.Sanitize(b), w-6) {
				prefix := "  · "
				if i > 0 {
					prefix = "    "
				}
				body = append(body, prefix+t.R(st, l))
			}
		}
	}
	room := max(3, h-len(out)-1)
	s.scroll = min(s.scroll, max(0, len(body)-room))
	end := min(len(body), s.scroll+room)
	out = append(out, body[s.scroll:end]...)
	return style.Fit(out, w, h), []widgets.Binding{{Keys: "↑↓", Desc: "scroll"}, {Keys: "m", Desc: "checks"}, {Keys: "esc", Desc: "checks"}}
}

type methodSection struct {
	title   string
	paras   []string
	bullets []string
}

// methodSections are the Method tab's contents, from the report where it
// says something about this scan and fixed text where it describes pwdet.
func methodSections(r *pwdet.Report) []methodSection {
	var out []methodSection
	out = append(out, methodSection{title: "How pwdet works", paras: []string{pwdet.Method(r)}, bullets: []string{
		"static: files, safetensors headers, configs, tokenizer, chat template and weight statistics; compared tensor by tensor with the base from Standard up",
		"provenance: Hub metadata, the lineage the model claims, parent hashes and a signed manifest",
		"black-box (Thorough, Deep): the same paraphrased prompts sent to the model and its base, with and without candidate triggers; a finding needs a significant, calibrated difference",
		"white-box (Deep): activations and per-layer deltas of the loaded model against the base",
		"verdict: malicious on a confident critical finding; likely tampered on a confident high finding, or two independent confident medium findings about the same thing; suspicious on any other medium or worse finding; inconclusive when fewer than four core checks could run; otherwise clean, meaning nothing was found in what ran",
	}})
	if a := r.Assurance; a != nil {
		sec := methodSection{title: "What this scan tested", paras: []string{a.Summary}}
		for _, th := range a.Threats {
			b := threatMark[th.State] + " " + pwdet.ThreatLine(th)
			sec.bullets = append(sec.bullets, b)
		}
		out = append(out, sec)
		if len(a.Gaps) > 0 {
			out = append(out, methodSection{title: "Gaps, and how to close them", bullets: a.Gaps})
		}
		if a.AccuracyText != "" {
			out = append(out, methodSection{title: "Measured accuracy", paras: []string{a.AccuracyText,
				"Confidence on each finding is measured the same way where the corpus has at least ten examples of that kind; otherwise the check's own estimate is shown and says so."}})
		}
	} else {
		out = append(out, methodSection{title: "What this scan tested", paras: []string{
			"This report was written by pwdet before 0.2, which did not record what it tested. Read the coverage table: a layer with no checks run was not tested."}})
	}
	limits := methodSection{title: "Strengths and limits"}
	for _, f := range r.Findings {
		if strings.HasPrefix(f.Title, "probe power:") {
			limits.bullets = append(limits.bullets, "Probe power for this scan: "+strings.TrimPrefix(f.Title, "probe power: "))
		}
	}
	limits.bullets = append(limits.bullets,
		"pwdet never runs the model's code: code on load (pickle opcodes, auto_map side-loading) is found by reading the files.",
		"A Basic scan compares nothing against a reference, so it cannot see weight implants or trained-in behaviour. Use Standard or above to rule them out.",
		"A backdoor trained in by fine-tuning looks like ordinary fine-tuning in the weights; only behavioural probes (Thorough, Deep) can find it, and only for triggers like the ones probed.",
		"A trigger that fires on a small share of matching prompts can be missed; the probe power line above says how small.",
		"Behavioural scorers are English-first; triggers and leaks in other languages are covered less well.",
		"Quantised models (4-bit) carry conversion noise; weight comparisons use noise floors measured on benign conversions, so a very small implant in a 4-bit model can sit under the floor.",
		"Accuracy is measured on pwdet's own corpus of small models (up to 1.7B parameters) and the attacks it builds; real attacks on larger models may differ.",
		"A clean verdict means these checks found nothing. It is not an audit and does not mean the model is safe.")
	return append(out, limits)
}

// coverageLines is a small table of how many checks each layer ran, and how
// they came out.
func coverageLines(c *Console, r *pwdet.Report, w int) []string {
	t := c.th
	if r.Verdict == nil || len(r.Verdict.Coverage) == 0 {
		return nil
	}
	cols := []string{"run", "pass", "warn", "fail", "skipped"}
	layers := make([]string, 0, len(r.Verdict.Coverage))
	for l, m := range r.Verdict.Coverage {
		layers = append(layers, l)
		if m["error"] > 0 && cols[len(cols)-1] != "error" {
			cols = append(cols, "error")
		}
	}
	sort.Strings(layers)
	head := "  " + style.PadRight("Coverage", 14)
	for _, k := range cols {
		head += style.PadLeft(k, 9)
	}
	out := []string{t.R(t.Muted, clip(head, w))}
	for _, l := range layers {
		line := "  " + style.PadRight(l, 14)
		for _, k := range cols {
			n := r.Verdict.Coverage[l][k]
			cell := style.PadLeft(strconv.Itoa(n), 9)
			switch {
			case n > 0 && k == "fail":
				cell = t.R(t.Bad, cell)
			case n > 0 && k == "warn":
				cell = t.R(t.Warn, cell)
			case n > 0 && k == "pass":
				cell = t.R(t.OK, cell)
			}
			line += cell
		}
		out = append(out, line)
	}
	return out
}

// detail is the selected check in full: what it observed, its evidence and
// what to do about it.
func (s *pwdetScreen) detail(c *Console, w int) []string {
	t := c.th
	if s.table.Cursor < 0 || s.table.Cursor >= len(s.shown) {
		return nil
	}
	f := s.shown[s.table.Cursor]
	head := fmt.Sprintf("  %s · %s · %s · confidence %.2f", f.CheckID, f.Status, f.EffectiveSeverity(), f.Confidence)
	if f.Declared {
		head += " · disclosed"
	}
	out := []string{t.R(t.Title, clip(head, w))}
	for _, l := range style.Wrap(strings.Join(strings.Fields(style.Sanitize(f.Title)), " "), w-4) {
		out = append(out, "    "+l)
	}
	for i, e := range f.Evidence {
		if i == 2 {
			out = append(out, t.R(t.Muted, fmt.Sprintf("    … %d more in the report", len(f.Evidence)-2)))
			break
		}
		line := e.Kind + " " + relPath(e.Locator)
		if e.Summary != "" {
			line += " — " + e.Summary
		}
		// a summary can hold a whole diff: one line of it, whitespace collapsed
		out = append(out, t.R(t.Muted, "    "+clip(strings.Join(strings.Fields(style.Sanitize(line)), " "), w-4)))
	}
	if f.Remediation != "" {
		out = append(out, t.R(t.Warn, "    "+clip(strings.Join(strings.Fields(style.Sanitize(f.Remediation)), " "), w-4)))
	}
	return out
}

// relPath shortens a path under the working directory or home for display.
func relPath(p string) string {
	if !filepath.IsAbs(p) {
		return p
	}
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}
