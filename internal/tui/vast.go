package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
	"github.com/launch80/L80-CLI/internal/vast"
)

type vastScreen struct {
	tab   int // 0 runs, 1 new run
	form  widgets.Form
	runs  []vast.RunState
	table widgets.Table
	job   *job
	err   string
}

var upTo = []string{"reap", "publish", "bench", "connect", "provision", "rent"}

func (s *vastScreen) init() {
	s.form = widgets.Form{Submit: "Start", Fields: []*widgets.Field{
		{Key: "spec", Label: "Spec file", Help: "The run's YAML: GPU filter and price ceiling (gpu.max_dph), model, engine, benchmark, publish. `L80 vast init` writes a starter.",
			Input: widgets.TextInput{Placeholder: "run.yaml"}},
		{Key: "hours", Label: "Max hours", Help: "Watchdog: when it fires, L80 stops the run and destroys its instance. Leave empty for none.",
			Input: widgets.TextInput{}},
		{Key: "upto", Label: "Stop after", Kind: widgets.ChoiceField, Choices: upTo,
			Help: "reap runs the whole pipeline and tears the box down. Earlier stages leave the instance running (and billing) for you to use."},
		{Key: "dry", Label: "Dry run", Kind: widgets.ToggleField, On: true, Help: "Show what would happen without renting anything. Turn off to rent."},
	}}
	s.form.Fields[1].Input.Set("2")
}

type vastRunsLoaded struct {
	runs []vast.RunState
	err  error
}

func (s *vastScreen) loadRuns(c *Console) Cmd {
	return func() Msg {
		runs, err := vast.List(c.d.RunsDir)
		return vastRunsLoaded{runs, err}
	}
}

func (s *vastScreen) env(c *Console) ([]string, error) { return c.d.VastEnv(c.d.RunsDir) }

func (s *vastScreen) update(c *Console, m Msg) Cmd {
	switch v := m.(type) {
	case vastRunsLoaded:
		s.runs, s.err = v.runs, ""
		if v.err != nil {
			s.err = v.err.Error()
		}
		s.table = widgets.Table{Cursor: s.table.Cursor, Columns: []widgets.Column{{Title: "Run", Width: 28}, {Title: "Status", Width: 11}, {Title: "Instance", Width: 9, Right: true}, {Title: "$/hr", Width: 6, Right: true}, {Title: "Billing", Width: 8}, {Title: "Page"}}}
		now := time.Now()
		for _, r := range v.runs {
			inst, dph, bill := "—", "—", ""
			if r.InstanceID != nil {
				inst = strconv.FormatInt(*r.InstanceID, 10)
			}
			if r.DPH != nil {
				dph = fmt.Sprintf("%.2f", *r.DPH)
			}
			if r.MayBeBilling() {
				bill = "YES"
				if d := r.BillingFor(now); d > 0 {
					bill = d.String()
				}
			}
			s.table.Rows = append(s.table.Rows, []string{r.RunID, r.Status, inst, dph, bill, orDash(r.PublishURL)})
		}
	case jobDone:
		if v.j == s.job {
			return s.loadRuns(c)
		}
	case PasteMsg:
		if s.tab == 1 {
			s.form.HandlePaste(v)
		}
	case MouseMsg:
		if s.job != nil {
			s.job.log.HandleMouse(v)
		}
	case KeyMsg:
		busy := s.job != nil && s.job.running
		if v.Ctrl && v.Rune == 'x' && busy {
			s.job.cancel()
			return nil
		}
		if v.Code == input.Tab && v.Ctrl == false && s.tab == 0 {
			s.tab = 1
			return nil
		}
		if s.tab == 1 {
			if v.Code == input.Esc {
				s.tab = 0
				return nil
			}
			used, submit := s.form.HandleKey(v)
			if submit && !busy {
				return s.startRun(c)
			}
			_ = used
			return nil
		}
		if s.job != nil && (v.Code == input.PageUp || v.Code == input.PageDown || v.Code == input.End) {
			s.job.log.HandleKey(v)
			return nil
		}
		if s.table.HandleKey(v) {
			return nil
		}
		row, haveRow := s.selected()
		switch {
		case isBack(v):
			c.scr = scrHome
		case v.Code == input.Rune && v.Rune == 'n':
			s.tab = 1
		case v.Code == input.Rune && v.Rune == 'r':
			return s.loadRuns(c)
		case busy:
		case v.Code == input.Rune && v.Rune == 'D':
			return s.tool(c, "vast doctor", []string{"doctor"})
		case v.Code == input.Rune && v.Rune == 's':
			spec := s.form.Get("spec")
			if spec == "" {
				c.setFlash("set a spec file in the New run tab first (n)", true)
				return nil
			}
			return s.tool(c, "search offers", []string{"search", "--spec", spec})
		case v.Code == input.Rune && v.Rune == 'd' && haveRow:
			if !row.MayBeBilling() {
				c.setFlash("that run has no instance to destroy", true)
				return nil
			}
			c.confirm(&widgets.Dialog{Title: "Destroy instance", Danger: true, Buttons: []string{"Destroy", "Cancel"}, Choice: 1,
				Body: fmt.Sprintf("Destroy instance %d from run %s? Anything on the box is lost. This cannot be undone.", *row.InstanceID, row.RunID)},
				func(ch int) Cmd {
					if ch == 0 {
						return s.tool(c, "destroy "+row.RunID, []string{"destroy", "--run", row.RunID, "-y"})
					}
					return nil
				})
		case v.Code == input.Rune && v.Rune == 'R':
			c.confirm(&widgets.Dialog{Title: "Reap", Danger: true, Buttons: []string{"Reap", "Cancel"}, Choice: 1,
				Body: "Destroy every Vast.ai instance bbvastl80 labelled on this account, including ones started elsewhere?"},
				func(ch int) Cmd {
					if ch == 0 {
						return s.tool(c, "reap", []string{"reap", "-y"})
					}
					return nil
				})
		case v.Code == input.Rune && v.Rune == 'x' && haveRow && row.InstanceID != nil:
			env, err := s.env(c)
			if err != nil {
				c.setFlash(err.Error(), true)
				return nil
			}
			return c.interactive("vast", []string{"ssh", "--instance", strconv.FormatInt(*row.InstanceID, 10)}, env,
				func(err error) Msg {
					if err != nil {
						return flashMsg{text: err.Error(), bad: true}
					}
					return nil
				})
		}
	}
	return nil
}

func (s *vastScreen) selected() (vast.RunState, bool) {
	if s.table.Cursor < 0 || s.table.Cursor >= len(s.runs) {
		return vast.RunState{}, false
	}
	return s.runs[s.table.Cursor], true
}

func (s *vastScreen) tool(c *Console, title string, args []string) Cmd {
	env, err := s.env(c)
	if err != nil {
		c.setFlash(err.Error(), true)
		return nil
	}
	s.job = newJob(title)
	return c.start(s.job, func(ctx context.Context, out io.Writer) (any, int, error) {
		code, err := runTool(ctx, "vast", args, env, out)
		return nil, code, err
	})
}

// specCaps reads the cost-relevant values from a spec without a YAML
// parser: the GPU names, count, the hourly ceiling and the budget.
func specCaps(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	out := map[string]string{}
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") && strings.HasSuffix(strings.TrimSpace(line), ":") {
			section = strings.TrimSuffix(strings.TrimSpace(line), ":")
			continue
		}
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		key := section + "." + strings.TrimSpace(k)
		switch key {
		case "gpu.names", "gpu.count", "gpu.max_dph", "budget.max_total_usd", "model.id", "name":
			out[key] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return out
}

func (s *vastScreen) startRun(c *Console) Cmd {
	vals := s.form.Values()
	spec := vals["spec"]
	if spec == "" {
		c.setFlash("a spec file is required", true)
		return nil
	}
	if _, err := os.Stat(spec); err != nil {
		c.setFlash("cannot read "+spec+": "+err.Error(), true)
		return nil
	}
	var limit time.Duration
	if h := vals["hours"]; h != "" {
		f, err := strconv.ParseFloat(h, 64)
		if err != nil || f <= 0 {
			c.setFlash("max hours must be a positive number", true)
			return nil
		}
		limit = time.Duration(f * float64(time.Hour))
	}
	args := []string{"run", "--spec", spec}
	if u := vals["upto"]; u != "reap" {
		args = append(args, "--up-to", u)
	}
	dry := vals["dry"] == "true"
	if dry {
		args = append(args, "--dry-run")
	}
	launch := func() Cmd {
		env, err := s.env(c)
		if err != nil {
			c.setFlash(err.Error(), true)
			return nil
		}
		s.tab = 0
		s.job = newJob("vast run " + spec)
		return c.start(s.job, func(ctx context.Context, out io.Writer) (any, int, error) {
			return vastPipeline(ctx, c.d.RunsDir, args, env, limit, out)
		})
	}
	if dry {
		return launch()
	}
	caps := specCaps(spec)
	body := fmt.Sprintf("Rent a GPU on Vast.ai and run %s?\n\n", spec)
	body += fmt.Sprintf("GPU: %s × %s\nPrice ceiling: $%s/hr\nBudget check: $%s\n", orDash(caps["gpu.names"]), orDash(caps["gpu.count"]), orDash(caps["gpu.max_dph"]), orDash(caps["budget.max_total_usd"]))
	if limit > 0 {
		body += fmt.Sprintf("Watchdog: destroy after %s\n", limit)
	} else {
		body += "No watchdog: the box runs until the pipeline ends or you destroy it.\n"
	}
	if vals["upto"] != "reap" {
		body += "\nStopping after " + vals["upto"] + " leaves the instance running and billing."
	}
	c.confirm(&widgets.Dialog{Title: "Rent a GPU", Danger: true, Buttons: []string{"Rent", "Cancel"}, Choice: 1, Body: body}, func(ch int) Cmd {
		if ch == 0 {
			return launch()
		}
		return nil
	})
	return nil
}

// vastPipeline runs `bbvastl80 run`, enforces the watchdog, and reports any
// instance from this run that may still be billing.
func vastPipeline(ctx context.Context, runsDir string, args, env []string, limit time.Duration, out io.Writer) (any, int, error) {
	start := time.Now()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var fired atomic.Bool
	if limit > 0 {
		t := time.AfterFunc(limit, func() {
			fired.Store(true)
			fmt.Fprintf(out, "\nL80: watchdog reached (%s); stopping the run and destroying its instance\n", limit)
			cancel()
		})
		defer t.Stop()
	}
	code, err := runTool(runCtx, "vast", args, env, out)
	runs, _ := vast.List(runsDir)
	mine := vast.Since(runs, start)
	if fired.Load() {
		for _, r := range vast.Billing(mine) {
			fmt.Fprintf(out, "destroying instance %d (run %s)\n", *r.InstanceID, r.RunID)
			runTool(context.Background(), "vast", []string{"destroy", "--run", r.RunID, "-y"}, env, out)
		}
		runs, _ = vast.List(runsDir)
		mine = vast.Since(runs, start)
	}
	for _, r := range vast.Billing(mine) {
		fmt.Fprintln(out, "warning: "+r.Reminder(time.Now()))
	}
	return nil, code, err
}

func (s *vastScreen) view(c *Console, w, h int) ([]string, []widgets.Binding) {
	t := c.th
	tabs := []string{" Runs ", " New run "}
	for i := range tabs {
		if i == s.tab {
			tabs[i] = t.R(t.Selected, tabs[i])
		} else {
			tabs[i] = t.R(t.Muted, tabs[i])
		}
	}
	out := []string{strings.Join(tabs, " ") + t.R(t.Muted, "   runs in "+clip(c.d.RunsDir, w-30)), ""}
	billing := vast.Billing(s.runs)
	if len(billing) > 0 {
		out = append(out, t.R(t.Bad, fmt.Sprintf("%d instance(s) may still be billing. Select one and press d to destroy it.", len(billing))), "")
	}
	var keys []widgets.Binding
	if s.tab == 1 {
		out = append(out, s.form.View(t, w, true)...)
		keys = []widgets.Binding{{Keys: "tab", Desc: "next field"}, {Keys: "enter", Desc: "start"}, {Keys: "esc", Desc: "runs"}}
	} else {
		if s.err != "" {
			out = append(out, t.R(t.Bad, clip(s.err, w)))
		}
		tableH := min(len(s.runs)+2, max(4, h/3))
		out = append(out, s.table.View(t, w, tableH, true)...)
		keys = []widgets.Binding{{Keys: "n", Desc: "new run"}, {Keys: "d", Desc: "destroy"}, {Keys: "x", Desc: "ssh"}, {Keys: "s", Desc: "search offers"}, {Keys: "D", Desc: "doctor"}, {Keys: "R", Desc: "reap"}, {Keys: "r", Desc: "refresh"}, {Keys: "esc", Desc: "back"}}
	}
	if s.job != nil {
		out = append(out, "", jobHeader(c, s.job, w))
		out = append(out, s.job.log.View(t, w, max(3, h-len(out)))...)
	}
	for i := range out {
		out[i] = style.Truncate(out[i], w, "…")
	}
	return out, keys
}
