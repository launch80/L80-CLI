package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/betterbench"
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

type benchScreen struct {
	form    widgets.Form
	job     *job
	result  *app.BenchResult
	summary []string
	url     string
	phase   int // 0 form, 1 running/log, 2 done
}

func (s *benchScreen) init() {
	s.form = widgets.Form{Submit: "Run benchmark", Fields: []*widgets.Field{
		{Key: "endpoint", Label: "Endpoint", Help: "OpenAI-compatible base URL, e.g. http://127.0.0.1:8000/v1. It never leaves this machine.",
			Input: widgets.TextInput{Placeholder: "http://127.0.0.1:8000/v1"}},
		{Key: "model", Label: "Model", Help: "The model name the server answers to (see GET /v1/models).", Input: widgets.TextInput{Placeholder: "Qwen3-8B"}},
		{Key: "quick", Label: "Quick run", Kind: widgets.ToggleField, Help: "5 passes per category: verifiable as a quick run (needs a reproduction; medians only). A full 20-pass run takes a while and carries p95/p99."},
		{Key: "phases", Label: "Phases", Kind: widgets.ChoiceField, Choices: []string{"all", "decode", "prefill", "concurrency"},
			Help: "Which measurements to run. All three is what the launch80 page shows best."},
		{Key: "notes", Label: "Notes", Help: "Comma-separated KEY=VALUE recorded with the run and shown as chips, e.g. engine=vLLM 0.9.3, quant=mxfp4, hardware=2x R9700.",
			Input: widgets.TextInput{Placeholder: "engine=vLLM 0.9.3, hardware=1x RTX 4090"}},
		{Key: "apikey", Label: "API key", Help: "Only if the endpoint needs one. Passed to BetterBench by environment, never recorded or shown.",
			Input: widgets.TextInput{Mask: true, Placeholder: "(none)"}},
	}}
	if k := os.Getenv("L80_BETTERBENCH_API_KEY"); k != "" {
		s.form.Fields[5].Input.Set(k)
	}
}

type benchPublished struct {
	art *api.Artifact
	err *api.Error
}

func (s *benchScreen) update(c *Console, m Msg) Cmd {
	switch v := m.(type) {
	case jobDone:
		if v.j != s.job {
			return nil
		}
		s.phase = 2
		if r, ok := v.result.(*app.BenchResult); ok && r != nil {
			s.result = r
			s.summary = benchSummary(r)
		}
		return nil
	case benchPublished:
		if v.err != nil {
			c.setFlash(v.err.Code+": "+v.err.Message+" — "+v.err.Remedy, true)
			return nil
		}
		s.url = v.art.URL
		c.setFlash("published: "+v.art.URL, false)
		return nil
	case PasteMsg:
		if s.phase == 0 {
			s.form.HandlePaste(v)
		}
	case MouseMsg:
		if s.job != nil {
			s.job.log.HandleMouse(v)
		}
	case KeyMsg:
		switch s.phase {
		case 0:
			if v.Code == input.Esc {
				c.scr = scrHome
				return nil
			}
			used, submit := s.form.HandleKey(v)
			if submit {
				return s.run(c)
			}
			if !used && v.Code == input.Rune && v.Rune == 'q' && s.form.Focus == len(s.form.Fields) {
				c.scr = scrHome
			}
		case 1, 2:
			if s.job.log.HandleKey(v) {
				return nil
			}
			switch {
			case v.Ctrl && v.Rune == 'x' && s.job.running:
				s.job.cancel()
			case s.job.running:
			case v.Code == input.Rune && v.Rune == 'p' && s.result != nil && s.url == "":
				return s.publish(c, false)
			case v.Code == input.Rune && v.Rune == 'n':
				s.phase, s.result, s.summary, s.url = 0, nil, nil, ""
			case isBack(v):
				s.phase = 0
			}
		}
	}
	return nil
}

func (s *benchScreen) run(c *Console) Cmd {
	vals := s.form.Values()
	if vals["endpoint"] == "" || vals["model"] == "" {
		c.setFlash("endpoint and model are required", true)
		return nil
	}
	var notes []string
	for _, n := range strings.Split(vals["notes"], ",") {
		if n = strings.TrimSpace(n); n != "" {
			if !strings.Contains(n, "=") {
				c.setFlash(fmt.Sprintf("note %q needs KEY=VALUE", n), true)
				return nil
			}
			notes = append(notes, n)
		}
	}
	var phases []string
	if p := vals["phases"]; p != "all" {
		phases = []string{p}
	}
	cwd, _ := os.Getwd()
	safe := strings.Map(func(r rune) rune {
		if r == '/' || r == ' ' || r == ':' {
			return '_'
		}
		return r
	}, vals["model"])
	out := filepath.Join(cwd, fmt.Sprintf("betterbench-%s-%s.json", safe, time.Now().Format("20060102-1504")))
	opts := betterbench.RunOptions{Endpoint: vals["endpoint"], Model: vals["model"], Out: out, Quick: vals["quick"] == "true",
		Warmup: -1, Notes: notes, Phases: phases, APIKey: s.form.Fields[5].Input.String()}
	s.job = newJob("BetterBench " + vals["model"])
	s.phase, s.result, s.summary, s.url = 1, nil, nil, ""
	return c.start(s.job, func(ctx context.Context, w io.Writer) (any, int, error) {
		res, apiErr := app.RunBenchmark(ctx, opts, app.WriterSink{W: w})
		if apiErr != nil {
			return nil, 1, fmt.Errorf("%s: %s", apiErr.Code, apiErr.Message)
		}
		return &res, 0, nil
	})
}

func benchSummary(r *app.BenchResult) []string {
	out := []string{"results " + r.Path, "BetterBench " + orDash(r.Version) + " via " + r.Runner.Source}
	parsed, err := betterbench.Parse(r.Data)
	if err != nil {
		return out
	}
	p, err := betterbench.Build(parsed, betterbench.Options{})
	if err == nil {
		for _, m := range p.Headline {
			out = append(out, strings.TrimSpace(m.Label+": "+m.Value+" "+m.Unit))
		}
	}
	for _, w := range app.ResultWarnings(parsed) {
		out = append(out, "warning: "+w)
	}
	if err == nil {
		for _, cv := range p.Caveats {
			out = append(out, "caveat: "+cv)
		}
	}
	return out
}

func (s *benchScreen) publish(c *Console, confirmed bool) Cmd {
	if !c.d.Config.HasToken() {
		c.setFlash("publishing needs a token: set L80_TOKEN or ~/.config/L80/credentials.json (L80 auth status)", true)
		return nil
	}
	if !confirmed {
		c.confirm(&widgets.Dialog{Title: "Publish", Body: "Publish this run to launch80 as a public page?\n\nThe endpoint URL and hostname are removed first. The page is self-reported until launch80 verifies it.",
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
		body, err := betterbench.PrepareRaw(res.Data, betterbench.Prose{})
		if err != nil {
			return benchPublished{err: api.Newf("E_INPUT_INVALID", "Publish it from the command line to see details.", "%v", err)}
		}
		if len(body) > api.MaxPayloadBytesFor(betterbench.RawTemplateID) {
			return benchPublished{err: api.Newf("E_PAYLOAD_TOO_LARGE", "Re-run with fewer passes.", "payload is %d bytes", len(body))}
		}
		pub, apiErr := app.Publish(c.d.Client, app.PublishRequest{Body: body, Template: betterbench.RawTemplateID, SourcePath: res.Path, APIBase: c.d.Config.BaseURL})
		if apiErr != nil {
			return benchPublished{err: apiErr}
		}
		return benchPublished{art: pub.Artifact}
	}
}

func (s *benchScreen) view(c *Console, w, h int) ([]string, []widgets.Binding) {
	t := c.th
	if s.phase == 0 {
		out := []string{t.R(t.Title, "Run BetterBench against an OpenAI-compatible endpoint"),
			t.R(t.Muted, "Runs the BetterBench pinned in this L80 and keeps results.json in the current directory."), ""}
		out = append(out, s.form.View(t, w, true)...)
		return out, []widgets.Binding{{Keys: "tab", Desc: "next field"}, {Keys: "enter", Desc: "next / run"}, {Keys: "esc", Desc: "back"}}
	}
	out := []string{jobHeader(c, s.job, w)}
	if s.phase == 2 {
		for _, l := range s.summary {
			st := t.Text
			if strings.HasPrefix(l, "warning:") {
				st = t.Warn
			} else if strings.HasPrefix(l, "caveat:") {
				st = t.Muted
			}
			for _, wl := range style.Wrap(style.Sanitize(l), w-2) {
				out = append(out, t.R(st, "  "+wl))
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
		keys = append(keys, widgets.Binding{Keys: "n", Desc: "new run"}, widgets.Binding{Keys: "esc", Desc: "back to form"})
	}
	return out, keys
}
