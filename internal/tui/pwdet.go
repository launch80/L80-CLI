package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	url    string
	phase  int
}

func (s *pwdetScreen) init() {
	s.form = widgets.Form{Submit: "Scan", Fields: []*widgets.Field{
		{Key: "model", Label: "Model", Help: "A local directory, a Hugging Face repo (org/name[@revision]), or a served copy (http://host:port/v1#model).",
			Input: widgets.TextInput{Placeholder: "Qwen/Qwen3-0.6B"}},
		{Key: "profile", Label: "Profile", Kind: widgets.ChoiceField, Choices: []string{"quick", "full-static", "milestone1", "all"},
			Help: "quick takes seconds; full-static reads every tensor (minutes on a large model); milestone1 adds black-box probes against a served copy."},
		{Key: "parent", Label: "Parent", Help: "Optional: the model this one claims to derive from, to compare against.", Input: widgets.TextInput{Placeholder: "(from the model card)"}},
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
			s.report, s.path = r.report, r.path
			s.table = widgets.Table{Columns: []widgets.Column{{Title: "Status", Width: 6}, {Title: "Severity", Width: 8}, {Title: "Check", Width: 26}, {Title: "Observation"}}}
			if r.report != nil {
				for _, f := range r.report.ToReview() {
					s.table.Rows = append(s.table.Rows, []string{f.Status, f.EffectiveSeverity(), f.CheckID, f.Title})
				}
			}
		}
	case pwdetPublished:
		if v.err != nil {
			c.setFlash(v.err.Code+": "+v.err.Message+" — "+v.err.Remedy, true)
		} else {
			s.url = v.art.URL
			c.setFlash("published: "+v.art.URL, false)
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
		if s.table.HandleKey(v) {
			return nil
		}
		switch {
		case v.Code == input.Rune && v.Rune == 'p' && s.report != nil && s.url == "":
			return s.publish(c, false)
		case v.Code == input.Rune && v.Rune == 'n':
			s.phase, s.report, s.url = 0, nil, ""
		case isBack(v):
			s.phase = 0
		}
	}
	return nil
}

func (s *pwdetScreen) scan(c *Console) Cmd {
	vals := s.form.Values()
	if vals["model"] == "" {
		c.setFlash("a model is required", true)
		return nil
	}
	cwd, _ := os.Getwd()
	args := []string{"scan", vals["model"], "--profile", vals["profile"], "--out", filepath.Join(cwd, "reports"), "--json-only"}
	if p := vals["parent"]; p != "" {
		args = append(args, "--parent", p)
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
	rep, path := s.report, s.path
	return func() Msg {
		p, err := pwdet.Build(rep, pwdet.Options{})
		if err != nil {
			return pwdetPublished{err: api.Newf("E_INPUT_INVALID", "Publish from the command line to see details.", "%v", err)}
		}
		body, _ := json.Marshal(p)
		pub, apiErr := app.Publish(c.d.Client, app.PublishRequest{Body: body, Template: pwdet.TemplateID, Title: p.Title, SourcePath: path, APIBase: c.d.Config.BaseURL})
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
		label := "no verdict"
		var rationale []string
		if s.report.Verdict != nil {
			label, rationale = s.report.Verdict.Label, s.report.Verdict.Rationale
		}
		st := t.OK
		switch label {
		case "suspicious", "inconclusive":
			st = t.Warn
		case "likely_tampered", "malicious":
			st = t.Bad
		}
		out = append(out, "", "  Verdict: "+t.R(st, strings.ToUpper(strings.ReplaceAll(label, "_", " ")))+t.R(t.Muted, "   "+s.report.Subject.Label()+" · profile "+s.report.Profile))
		for _, r := range rationale {
			out = append(out, t.R(t.Muted, "  "+clip(style.Sanitize(r), w-2)))
		}
		out = append(out, t.R(t.Muted, "  report "+clip(s.path, w-10)), t.R(t.Muted, "  "+strings.TrimSuffix(clip(s.path, w-10), ".json")+".html for the full detail"))
		if s.url != "" {
			out = append(out, t.R(t.OK, "  published: "+s.url))
		}
		out = append(out, "")
		if len(s.table.Rows) == 0 {
			out = append(out, t.R(t.OK, "  No check reported a warning or a failure."))
		} else {
			out = append(out, t.R(t.Title, fmt.Sprintf("  Findings to review (%d)", len(s.table.Rows))))
			out = append(out, s.table.View(t, w, max(3, h-len(out)-1), true)...)
		}
		keys := []widgets.Binding{{Keys: "↑↓", Desc: "findings"}}
		if s.url == "" {
			keys = append(keys, widgets.Binding{Keys: "p", Desc: "publish"})
		}
		return out, append(keys, widgets.Binding{Keys: "n", Desc: "new scan"}, widgets.Binding{Keys: "esc", Desc: "back to form"})
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
