package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/output"
	"github.com/launch80/L80-CLI/internal/pytool"
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

// Deps is what the console needs from the CLI that it cannot import.
type Deps struct {
	Version string
	Config  config.Config
	Client  *api.Client
	// VastEnv returns the environment `L80 vast` gives bbvastl80.
	VastEnv func(runsDir string) ([]string, error)
	// RunsDir is where bbvastl80 keeps run state.
	RunsDir string
	// DashboardState is AI_DASHBOARD_STATE for the dashboard.
	DashboardState string
}

type screenID int

const (
	scrHome screenID = iota
	scrBench
	scrVast
	scrPwdet
	scrDash
	scrHistory
	scrTools
	scrHelp
)

// Console is the root model.
type Console struct {
	d     Deps
	th    widgets.Theme
	prog  *Program
	w, h  int
	scr   screenID
	menu  widgets.List
	tick  int
	flash string
	bad   bool

	dialog   *widgets.Dialog
	onDialog func(choice int) Cmd

	home    homeScreen
	bench   benchScreen
	vast    vastScreen
	pw      pwdetScreen
	dash    dashScreen
	hist    histScreen
	tools   toolsScreen
	redraw  atomic.Bool
	running int // jobs in flight, for the spinner tick
}

// Run starts the console. It returns ErrNoTTY when there is no terminal.
func Run(d Deps) error {
	c := &Console{d: d}
	p := NewProgram(c)
	c.prog = p
	unicodeOK := !strings.Contains(strings.ToLower(os.Getenv("LANG")+os.Getenv("LC_ALL")), "c") || strings.Contains(strings.ToLower(os.Getenv("LANG")+os.Getenv("LC_ALL")+os.Getenv("LC_CTYPE")), "utf")
	if os.Getenv("TERM") == "linux" {
		unicodeOK = false
	}
	c.th = widgets.DefaultTheme(p.Profile(), unicodeOK)
	c.menu = widgets.List{Items: []widgets.Item{
		{Title: "Benchmark an endpoint", Desc: "Run BetterBench and publish the result", Value: scrBench},
		{Title: "Rent a GPU and benchmark", Desc: "Vast.ai: rent, serve, benchmark, publish, tear down", Value: scrVast},
		{Title: "Scan weights for tampering", Desc: "Poisoned Weights Detector on a checkpoint or endpoint", Value: scrPwdet},
		{Title: "Telemetry dashboard", Desc: "GPU and inference state of this machine", Value: scrDash},
		{Title: "Published pages", Desc: "What this key has published, and each page's tier", Value: scrHistory},
		{Title: "Tools", Desc: "Pinned versions and how each resolves here", Value: scrTools},
		{Title: "Help", Desc: "Keys, commands, environment", Value: scrHelp},
	}}
	c.bench.init()
	c.vast.init()
	c.pw.init()
	return p.Run()
}

// --- messages -----------------------------------------------------------------

type (
	tickMsg   struct{}
	redrawMsg struct{}
	flashMsg  struct {
		text string
		bad  bool
	}
)

func (c *Console) setFlash(text string, bad bool) { c.flash, c.bad = text, bad }

// notify asks for a redraw without flooding the queue: at most one pending.
func (c *Console) notify() {
	if c.redraw.CompareAndSwap(false, true) {
		c.prog.Send(redrawMsg{})
	}
}

// paneWriter writes into a log pane and asks for a redraw.
type paneWriter struct {
	pane *widgets.LogPane
	c    *Console
}

func (w paneWriter) Write(p []byte) (int, error) {
	n, err := w.pane.Write(p)
	w.c.notify()
	return n, err
}

// job is one background run with its own log.
type job struct {
	title   string
	log     *widgets.LogPane
	cancel  context.CancelFunc
	started time.Time
	ended   time.Time
	running bool
	code    int
	err     error
}

type jobDone struct {
	j      *job
	result any
	code   int
	err    error
}

func newJob(title string) *job { return &job{title: title, log: widgets.NewLogPane(5000)} }

// start runs fn in the background; fn writes progress to out.
func (c *Console) start(j *job, fn func(ctx context.Context, out io.Writer) (any, int, error)) Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	j.log.Clear()
	j.cancel, j.started, j.running, j.err, j.code = cancel, time.Now(), true, nil, 0
	c.running++
	out := paneWriter{pane: j.log, c: c}
	first := c.running == 1
	run := func() Msg {
		res, code, err := fn(ctx, out)
		cancel()
		return jobDone{j: j, result: res, code: code, err: err}
	}
	if first {
		return Batch(run, c.tickCmd())
	}
	return run
}

func (c *Console) tickCmd() Cmd {
	return Tick(120*time.Millisecond, func(time.Time) Msg { return tickMsg{} })
}

// runTool streams a pinned tool's output into out and returns its status.
func runTool(ctx context.Context, name string, args, env []string, out io.Writer) (int, error) {
	t, err := pytool.Lookup(name)
	if err != nil {
		return -1, err
	}
	r, err := pytool.Resolve(t)
	if err != nil {
		return -1, err
	}
	fmt.Fprintf(out, "$ %s\n", pytool.FormatArgv(append(append([]string{}, r.Argv...), args...)))
	for _, n := range r.Notes {
		fmt.Fprintf(out, "note: %s\n", n)
	}
	return pytool.Exec(ctx, r, args, pytool.ExecOpts{Stdout: out, Stderr: out, ExtraEnv: env, GraceAfterCancel: 60 * time.Second})
}

// interactive hands the terminal to a tool (ssh, the dashboard UI).
func (c *Console) interactive(name string, args, env []string, done func(error) Msg) Cmd {
	return func() Msg {
		return ExecMsg{Run: func() error {
			t, err := pytool.Lookup(name)
			if err != nil {
				return err
			}
			r, err := pytool.Resolve(t)
			if err != nil {
				return err
			}
			code, err := pytool.Exec(context.Background(), r, args, pytool.ExecOpts{Interactive: true, ExtraEnv: env})
			if err == nil && code != 0 {
				err = fmt.Errorf("%s exited with status %d", t.Exe, code)
			}
			return err
		}, Done: done}
	}
}

// confirm opens a modal; on answer calls then(choice).
func (c *Console) confirm(d *widgets.Dialog, then func(int) Cmd) {
	c.dialog, c.onDialog = d, then
}

// --- Elm loop -------------------------------------------------------------------

func (c *Console) Init() Cmd { return c.home.load(c) }

func (c *Console) Update(m Msg) (Model, Cmd) {
	switch v := m.(type) {
	case SizeMsg:
		c.w, c.h = v.W, v.H
		return c, nil
	case redrawMsg:
		c.redraw.Store(false)
		return c, nil
	case tickMsg:
		c.tick++
		if c.running > 0 {
			return c, c.tickCmd()
		}
		return c, nil
	case flashMsg:
		c.setFlash(v.text, v.bad)
		return c, nil
	case ErrorMsg:
		c.setFlash(v.Err.Error(), true)
		return c, nil
	case jobDone:
		c.running--
		v.j.running, v.j.ended, v.j.code, v.j.err = false, time.Now(), v.code, v.err
	case MouseMsg:
		return c, c.route(m)
	case KeyMsg:
		if c.dialog != nil {
			if c.dialog.HandleKey(v) {
				d, then := c.dialog, c.onDialog
				c.dialog, c.onDialog = nil, nil
				if then != nil {
					return c, then(d.Choice)
				}
			}
			return c, nil
		}
		if v.Ctrl && v.Rune == 'c' {
			if c.running > 0 {
				c.confirm(&widgets.Dialog{Title: "Quit", Body: "A job is still running. Quitting interrupts it (a rental run then tears its instance down).",
					Buttons: []string{"Quit", "Stay"}, Choice: 1, Danger: true}, func(ch int) Cmd {
					if ch == 0 {
						c.cancelAll()
						return Quit
					}
					return nil
				})
				return c, nil
			}
			return c, Quit
		}
		c.flash = ""
	}
	return c, c.route(m)
}

func (c *Console) cancelAll() {
	for _, j := range []*job{c.bench.job, c.vast.job, c.pw.job, c.tools.job} {
		if j != nil && j.running && j.cancel != nil {
			j.cancel()
		}
	}
}

// route sends a message to the current screen.
func (c *Console) route(m Msg) Cmd {
	switch c.scr {
	case scrBench:
		return c.bench.update(c, m)
	case scrVast:
		return c.vast.update(c, m)
	case scrPwdet:
		return c.pw.update(c, m)
	case scrDash:
		return c.dash.update(c, m)
	case scrHistory:
		return c.hist.update(c, m)
	case scrTools:
		return c.tools.update(c, m)
	case scrHelp:
		if k, ok := m.(KeyMsg); ok && isBack(k) {
			c.scr = scrHome
		}
		return nil
	}
	return c.home.update(c, m)
}

func isBack(k KeyMsg) bool {
	return k.Code == input.Esc || (k.Code == input.Rune && k.Rune == 'q' && !k.Ctrl && !k.Alt)
}

func (c *Console) open(s screenID) Cmd {
	c.scr = s
	switch s {
	case scrHistory:
		return c.hist.load(c)
	case scrTools:
		return c.tools.load(c, false)
	case scrDash:
		return c.dash.load(c)
	case scrVast:
		return c.vast.loadRuns(c)
	}
	return nil
}

// --- frame ------------------------------------------------------------------------

var titles = map[screenID]string{
	scrHome: "Home", scrBench: "Benchmark an endpoint", scrVast: "Rent a GPU and benchmark",
	scrPwdet: "Scan weights for tampering", scrDash: "Telemetry dashboard", scrHistory: "Published pages",
	scrTools: "Tools", scrHelp: "Help",
}

func (c *Console) View(w, h int) []string {
	if w < 60 || h < 16 {
		return style.Place([]string{"L80 needs at least 60×16 to draw.", fmt.Sprintf("This terminal is %d×%d.", w, h), "", "Ctrl-C quits."}, w, h)
	}
	t := c.th
	left := t.R(t.Accent, " L80 ") + t.R(t.Muted, c.d.Version) + t.R(t.Edge, "  ·  ") + t.R(t.Title, titles[c.scr])
	right := "no token"
	if c.d.Config.HasToken() {
		right = "key " + orDash(c.d.Config.KeyID)
	}
	if c.running > 0 {
		right = widgets.Spin(c.tick) + " " + fmt.Sprint(c.running) + " running  " + right
	}
	header := style.PadRight(left, w-style.Width(right)-1) + t.R(t.Muted, right) + " "

	bodyH := h - 2
	var body []string
	var keys []widgets.Binding
	switch c.scr {
	case scrBench:
		body, keys = c.bench.view(c, w-2, bodyH-2)
	case scrVast:
		body, keys = c.vast.view(c, w-2, bodyH-2)
	case scrPwdet:
		body, keys = c.pw.view(c, w-2, bodyH-2)
	case scrDash:
		body, keys = c.dash.view(c, w-2, bodyH-2)
	case scrHistory:
		body, keys = c.hist.view(c, w-2, bodyH-2)
	case scrTools:
		body, keys = c.tools.view(c, w-2, bodyH-2)
	case scrHelp:
		body, keys = helpView(c, w-2, bodyH-2)
	default:
		body, keys = c.home.view(c, w-2, bodyH-2)
	}
	frame := []string{header}
	frame = append(frame, style.Box(t.Border, t.P, t.Edge, "", body, w, bodyH)...)

	footer := widgets.HelpLine(t, w-2, append(keys, widgets.Binding{Keys: "ctrl+c", Desc: "quit"})...)
	if c.flash != "" {
		st := t.OK
		if c.bad {
			st = t.Bad
		}
		footer = t.R(st, style.Truncate(style.Sanitize(c.flash), w-2, "…"))
	}
	frame = append(frame, " "+footer)

	if c.dialog != nil {
		dv := c.dialog.View(t, min(w-4, 72))
		top := max(1, (h-len(dv))/2)
		left := max(0, (w-style.Width(dv[0]))/2)
		for i, l := range dv {
			if top+i < len(frame) {
				frame[top+i] = overlay(frame[top+i], l, left, w)
			}
		}
	}
	return frame
}

// overlay replaces cells [col, col+width(top)) of base with top. Colour in
// the covered part of base is dropped; the rest is redrawn plain, which is
// acceptable under a modal.
func overlay(base, top string, col, w int) string {
	plain := style.Strip(base)
	r := []rune(plain)
	pre := style.Truncate(string(r), col, "")
	pre = style.PadRight(pre, col)
	tw := style.Width(top)
	restStart := col + tw
	rest := ""
	cells := 0
	for _, ch := range r {
		cw := style.RuneWidth(ch)
		if cells >= restStart {
			rest += string(ch)
		}
		cells += cw
	}
	return style.Truncate(pre+top+rest, w, "")
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func masked(cfg config.Config) string { return output.Mask(cfg.Token) }

func elapsed(j *job) string {
	if j == nil || j.started.IsZero() {
		return ""
	}
	end := time.Now()
	if !j.running {
		end = j.ended
	}
	return end.Sub(j.started).Round(time.Second).String()
}
