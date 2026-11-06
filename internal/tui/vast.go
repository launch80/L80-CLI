package tui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	tab   int // 0 runs, 1 run a spec file, 2 new rental (the wizard)
	form  widgets.Form
	wiz   rentWizard
	prog  *rentProgress
	runs  []vast.RunState
	table widgets.Table
	job   *job
	err   string
}

var upTo = []string{"reap", "publish", "bench", "connect", "provision", "rent"}

func (s *vastScreen) init() {
	s.form = widgets.Form{Submit: "Start", Fields: []*widgets.Field{
		{Key: "spec", Label: "Spec file", Help: "A run spec YAML (the New rental tab writes one per rental, or `L80 vast spec`). No spec yet? Press ctrl+b for the New rental tab.",
			Input: widgets.TextInput{Placeholder: "ctrl+b: new rental"}},
		{Key: "hours", Label: "Max hours", Help: "Watchdog: when it fires, L80 stops the run and destroys its instance. Leave empty for none.",
			Input: widgets.TextInput{}},
		{Key: "upto", Label: "Stop after", Kind: widgets.ChoiceField, Choices: upTo,
			Help: "reap runs the whole pipeline and tears the box down. Earlier stages leave the instance running (and billing) for you to use."},
		{Key: "dry", Label: "Dry run", Kind: widgets.ToggleField, On: true, Help: "Show what would happen without renting anything. Turn off to rent."},
	}}
	s.form.Fields[1].Input.Set("2")

	s.wiz.init()
}

// specsDir sits next to the runs directory, as `L80 vast spec` writes it.
func specsDir(c *Console) string { return filepath.Join(filepath.Dir(c.d.RunsDir), "specs") }

// openBuilder switches to the New rental tab, explaining why.
func (s *vastScreen) openBuilder(c *Console, why string) Cmd {
	s.tab, s.wiz.page = 2, pgModel
	s.wiz.forms[pgModel].Focus = 0
	if why != "" {
		c.setFlash(why, false)
	}
	return c.qb.refreshResults(c)
}

// rentWith opens the New rental tab with a model and steps filled in (from
// the QualityBench screen); the user picks the hardware and reviews the cost.
func (c *Console) rentWith(model, engine string, steps []vast.Step) Cmd {
	s := &c.vast
	s.wiz.field(pgModel, "model").Input.Set(model)
	eng := s.wiz.field(pgModel, "engine")
	for i, e := range eng.Choices {
		if e == engine {
			eng.Choice = i
		}
	}
	s.wiz.handed = steps
	s.tab, s.wiz.page = 2, pgHardware
	s.wiz.forms[pgHardware].Focus = 0
	for i, id := range c.side {
		if id == scrVast {
			c.sel = i
		}
	}
	c.scr, c.paneFocus = scrVast, true
	c.setFlash("pick the GPU, then review the steps and the cost; nothing is rented until you confirm", false)
	return s.loadRuns(c)
}

// wizardKey drives the New rental tab.
func (s *vastScreen) wizardKey(c *Console, k KeyMsg, busy bool) Cmd {
	w := &s.wiz
	switch {
	case k.Ctrl && k.Rune == 'r' && len(w.handed) > 0:
		w.handed = nil
		return nil
	case k.Code == input.Rune && k.Rune == 's' && w.page == pgHardware && !k.Ctrl:
		if f := w.forms[pgHardware]; f.Focus < len(f.Fields) && f.Fields[f.Focus].Kind == widgets.TextField {
			break // typing an s
		}
		if busy {
			return nil
		}
		return s.searchOffers(c)
	}
	next, back := w.handKey(k)
	switch {
	case back && w.page == 0:
		s.tab = 0
	case back:
		w.page--
	case next && w.page < pgReview:
		if w.page == pgModel {
			if _, err := w.spec(); err != nil && strings.Contains(err.Error(), "model") {
				c.setFlash(err.Error(), true)
				return nil
			}
		}
		w.page++
		w.forms[w.page].Focus = 0
	case next && !busy:
		return s.startRental(c)
	}
	return nil
}

// searchOffers shows the offers the Hardware page matches, renting nothing.
func (s *vastScreen) searchOffers(c *Console) Cmd {
	sp, err := s.wiz.spec()
	if err != nil && !strings.Contains(err.Error(), "operation") {
		c.setFlash(err.Error(), true)
		return nil
	}
	f, err := os.CreateTemp("", "l80-search-*.yaml")
	if err != nil {
		c.setFlash(err.Error(), true)
		return nil
	}
	f.WriteString(sp.YAML())
	f.Close()
	cmd := s.tool(c, "search offers", []string{"search", "--spec", f.Name()})
	return cmd
}

// startRental writes the wizard's spec (the record), then asks once, with
// the price and the cap, and runs it.
func (s *vastScreen) startRental(c *Console) Cmd {
	sp, err := s.wiz.spec()
	if err != nil {
		c.setFlash(err.Error(), true)
		return nil
	}
	p := s.wiz.plan(sp)
	hrs, err := s.wiz.hours(p)
	if err != nil {
		c.setFlash(err.Error(), true)
		return nil
	}
	path := filepath.Join(specsDir(c), sp.FileName())
	dry := s.wiz.val(pgReview, "dry") == "true"
	limit := time.Duration(hrs * float64(time.Hour))
	run := func() Cmd {
		if _, err := vast.WriteSpec(sp, path, true); err != nil {
			c.setFlash(err.Error(), true)
			return nil
		}
		s.form.Fields[0].Input.Set(path)
		args := []string{"run", "--spec", path}
		if dry {
			args = append(args, "--dry-run")
		}
		return s.launch(c, path, args, limit, sp.Steps)
	}
	consent := func() Cmd {
		if dry {
			return run()
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Rent a GPU on Vast.ai for %s?\n\n", sp.ModelRepo)
		gpu := strings.Join(sp.GPUNames, ", ")
		if gpu == "" {
			gpu = "any"
		}
		fmt.Fprintf(&b, "GPU: %s × %d, at most $%s/hr\n", gpu, sp.GPUCount, strconv.FormatFloat(sp.MaxDPH, 'f', -1, 64))
		for i, st := range sp.Steps {
			fmt.Fprintf(&b, "%d. %s\n", i+1, st.Describe())
		}
		fmt.Fprintf(&b, "\nAbout %s; hard cap %.1f h, so at most $%.2f.\n", humanMinutes(p.minutes), hrs, hrs*sp.MaxDPH)
		b.WriteString("The box is destroyed when the run ends or the cap is hit; nothing waits on you.")
		if pubs := publishing(sp.Steps); pubs > 0 {
			fmt.Fprintf(&b, "\n%d step(s) publish a public launch80 page.", pubs)
		}
		c.confirm(&widgets.Dialog{Title: "Rent a GPU", Danger: true, Buttons: []string{"Rent", "Cancel"}, Choice: 1, Body: b.String()}, func(ch int) Cmd {
			if ch == 0 {
				return run()
			}
			return nil
		})
		return nil
	}
	if _, err := os.Stat(path); err == nil {
		c.confirm(&widgets.Dialog{Title: "Replace spec", Buttons: []string{"Replace", "Cancel"}, Choice: 1,
			Body: path + " already exists (an earlier rental's record). Replace it with this one?"}, func(ch int) Cmd {
			if ch == 0 {
				return consent()
			}
			return nil
		})
		return nil
	}
	return consent()
}

func publishing(steps []vast.Step) int {
	n := 0
	for _, st := range steps {
		if st.Publish {
			n++
		}
	}
	return n
}

type vastRunsLoaded struct {
	runs  []vast.RunState
	err   error
	specs []string // newest first
}

func (s *vastScreen) loadRuns(c *Console) Cmd {
	dir := specsDir(c)
	return func() Msg {
		runs, err := vast.List(c.d.RunsDir)
		return vastRunsLoaded{runs, err, vast.ListSpecs(dir)}
	}
}

func (s *vastScreen) env(c *Console) ([]string, error) { return c.d.VastEnv(c.d.RunsDir) }

func (s *vastScreen) update(c *Console, m Msg) Cmd {
	switch v := m.(type) {
	case vastRunsLoaded:
		s.runs, s.err = v.runs, ""
		if len(v.specs) > 0 && s.form.Get("spec") == "" {
			s.form.Fields[0].Input.Set(v.specs[0])
		}
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
	case qbResults:
		s.wiz.setResults(v.rs)
	case PasteMsg:
		switch s.tab {
		case 1:
			s.form.HandlePaste(v)
		case 2:
			s.wiz.forms[s.wiz.page].HandlePaste(v)
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
		if v.Code == input.Tab && !v.Ctrl && s.tab == 0 {
			return s.openBuilder(c, "")
		}
		if s.tab == 2 {
			return s.wizardKey(c, v, busy)
		}
		if s.tab == 1 {
			if v.Code == input.Esc {
				s.tab = 0
				return nil
			}
			if v.Ctrl && v.Rune == 'b' {
				return s.openBuilder(c, "")
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
			return s.openBuilder(c, "")
		case v.Code == input.Rune && v.Rune == 'f':
			s.tab = 1
		case v.Code == input.Rune && v.Rune == 'o' && s.prog != nil:
			for _, o := range s.prog.outputs() {
				if o.Report != "" {
					if err := openFile(o.Report); err != nil {
						c.setFlash(err.Error(), true)
					}
					return nil
				}
			}
			c.setFlash("no report yet", true)
		case v.Code == input.Rune && v.Rune == 'b':
			return s.openBuilder(c, "")
		case v.Code == input.Rune && v.Rune == 'r':
			return s.loadRuns(c)
		case busy:
		case v.Code == input.Rune && v.Rune == 'D':
			return s.tool(c, "vast doctor", []string{"doctor"})
		case v.Code == input.Rune && v.Rune == 's':
			spec := s.form.Get("spec")
			if spec == "" {
				return s.openBuilder(c, "no spec yet: fill this in to write one, then search")
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
		return s.openBuilder(c, "no spec yet: fill this in to write one")
	}
	if _, err := os.Stat(spec); errors.Is(err, os.ErrNotExist) {
		return s.openBuilder(c, spec+" does not exist: build one here")
	} else if err != nil {
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
	launch := func() Cmd { return s.launch(c, spec, args, limit, nil) }
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

// launch starts `bbvastl80 run` as a job, parsing its step lines.
func (s *vastScreen) launch(c *Console, spec string, args []string, limit time.Duration, steps []vast.Step) Cmd {
	env, err := s.env(c)
	if err != nil {
		c.setFlash(err.Error(), true)
		return nil
	}
	s.tab = 0
	s.job = newJob("vast run " + filepath.Base(spec))
	s.prog = newRentProgress(steps)
	prog := s.prog
	return c.start(s.job, func(ctx context.Context, out io.Writer) (any, int, error) {
		return vastPipeline(ctx, c.d.RunsDir, args, env, limit, &rentWriter{out: out, p: prog})
	})
}

// vastPipeline runs `bbvastl80 run`, enforces the watchdog, and reports any
// instance from this run that may still be billing.
func vastPipeline(ctx context.Context, runsDir string, args, env []string, limit time.Duration, out io.Writer) (any, int, error) {
	start := time.Now()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var fired atomic.Bool
	if limit > 0 {
		// bbvastl80 tells QualityBench to stop starting work before this.
		env = append(append([]string{}, env...), fmt.Sprintf("BBVASTL80_DEADLINE=%d", start.Add(limit).Unix()))
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
	tabs := []string{" Runs ", " Spec file ", " New rental "}
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
	switch s.tab {
	case 2:
		out = append(out, s.wiz.view(c, w)...)
		keys = []widgets.Binding{{Keys: "tab", Desc: "next field"}, {Keys: "←→", Desc: "choose"}, {Keys: "enter", Desc: "next"}, {Keys: "esc", Desc: "previous page"}}
		if s.wiz.page == pgHardware {
			keys = append(keys, widgets.Binding{Keys: "s", Desc: "search offers"})
		}
	case 1:
		out = append(out, s.form.View(t, w, true)...)
		keys = []widgets.Binding{{Keys: "tab", Desc: "next field"}, {Keys: "ctrl+b", Desc: "new rental"}, {Keys: "enter", Desc: "start"}, {Keys: "esc", Desc: "runs"}}
	default:
		if s.err != "" {
			out = append(out, t.R(t.Bad, clip(s.err, w)))
		}
		tableH := min(len(s.runs)+2, max(4, h/3))
		out = append(out, s.table.View(t, w, tableH, true)...)
		keys = []widgets.Binding{{Keys: "n", Desc: "new rental"}, {Keys: "f", Desc: "run a spec file"}, {Keys: "s", Desc: "search offers"}, {Keys: "d", Desc: "destroy"}, {Keys: "x", Desc: "ssh"}, {Keys: "esc", Desc: "back"}, {Keys: "D", Desc: "doctor"}, {Keys: "R", Desc: "reap"}, {Keys: "r", Desc: "refresh"}}
	}
	if s.job != nil {
		out = append(out, "", jobHeader(c, s.job, w))
		if s.prog != nil && len(s.prog.steps) > 0 && s.tab == 0 {
			out = append(out, s.prog.view(c.th, w, c.tick)...)
			for _, o := range s.prog.outputs() {
				if o.Results != "" {
					out = append(out, c.th.R(c.th.Muted, "  "+style.Sanitize(o.Label+": "+o.Results)))
				}
			}
		}
		out = append(out, s.job.log.View(t, w, max(3, h-len(out)))...)
	}
	for i := range out {
		out[i] = style.Truncate(out[i], w, "…")
	}
	return out, keys
}
