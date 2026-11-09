package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
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
	// Usage is `L80 help`'s command list, for the About page's directory.
	Usage string
	// Onboard opens the console on onboarding (first run, or `L80 onboard`).
	Onboard bool
	// Update runs `L80 update` with args, in process; nil where it cannot.
	Update func(args []string, stdout, stderr io.Writer) int
}

// Console is the root model.
type Console struct {
	d    Deps
	th   widgets.Theme
	prog *Program
	w, h int
	scr  screenID
	// The sidebar is always visible; the pane shows the selected screen.
	// Moving through the sidebar previews each screen live; enter gives the
	// pane the keyboard and esc hands it back.
	side      []screenID
	sel       int
	paneFocus bool
	tick      int
	flash     string
	bad       bool

	dialog   *widgets.Dialog
	onDialog func(choice int) Cmd

	home   homeScreen
	bench  benchScreen
	vast   vastScreen
	pw     pwdetScreen
	dash   dashScreen
	hist   histScreen
	verify verifyScreen
	tools  toolsScreen
	about  aboutScreen
	onb    onboardScreen
	qb     qualityScreen
	// pages holds the state of screens that register from their own file.
	pages   map[screenID]any
	redraw  atomic.Bool
	running int    // jobs in flight, for the spinner tick
	jobs    []*job // every job started, oldest first (see track)
}

// Run starts the console. It returns ErrNoTTY when there is no terminal.
func Run(d Deps) error {
	c := &Console{d: d, scr: scrHome}
	p := NewProgram(c)
	c.prog = p
	unicodeOK := !strings.Contains(strings.ToLower(os.Getenv("LANG")+os.Getenv("LC_ALL")), "c") || strings.Contains(strings.ToLower(os.Getenv("LANG")+os.Getenv("LC_ALL")+os.Getenv("LC_CTYPE")), "utf")
	if os.Getenv("TERM") == "linux" {
		unicodeOK = false
	}
	c.th = widgets.DefaultTheme(p.Profile(), unicodeOK)
	c.side = sidebarIDs()
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
	// status, when set, adds live progress to the title while the job runs
	// ("48% · 23 min left").
	status func() string
}

// label is the job's title with its live status while it runs.
func (j *job) label() string {
	if j.running && j.status != nil {
		if s := j.status(); s != "" {
			return j.title + " · " + s
		}
	}
	return j.title
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
	c.track(j)
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

// maxJobs bounds the jobs list; the oldest finished ones go first.
const maxJobs = 50

// track records j in the console's jobs list, so a job stays visible (and
// cancellable on quit) on Home after its own screen moves on.
func (c *Console) track(j *job) {
	if slices.Contains(c.jobs, j) {
		return
	}
	c.jobs = append(c.jobs, j)
	for len(c.jobs) > maxJobs {
		i := slices.IndexFunc(c.jobs, func(j *job) bool { return !j.running })
		if i < 0 {
			break
		}
		c.jobs = slices.Delete(c.jobs, i, i+1)
	}
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

func (c *Console) Init() Cmd {
	if c.d.Onboard {
		return Batch(c.home.load(c), c.startOnboarding())
	}
	return c.home.load(c)
}

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
	for _, j := range c.jobs {
		if j.running && j.cancel != nil {
			j.cancel()
		}
	}
}

// route sends input to the sidebar or the focused pane, and everything else
// (load results, finished jobs) to every screen, since a result can arrive
// after the sidebar has moved on. Each screen ignores what is not its own.
func (c *Console) route(m Msg) Cmd {
	switch v := m.(type) {
	case KeyMsg:
		if !c.paneFocus {
			return c.sideKey(v)
		}
	case MouseMsg, PasteMsg:
		if !c.paneFocus {
			return nil
		}
	default:
		var cmds []Cmd
		for _, s := range allIDs() {
			if cmd := c.routeTo(s, m); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return Batch(cmds...)
	}
	before := c.scr
	cmd := c.routeTo(c.scr, m)
	// A screen going "back" (esc, q) sets scrHome: in this layout that means
	// handing the keyboard back to the sidebar, on the item it came from.
	if c.scr == scrHome && before != scrHome {
		c.paneFocus = false
		c.scr = c.side[c.sel]
	}
	return cmd
}

// sideKey handles keys while the sidebar has focus.
func (c *Console) sideKey(k KeyMsg) Cmd {
	move := func(i int) Cmd {
		i = (i + len(c.side)) % len(c.side)
		if i == c.sel && c.scr == c.side[i] {
			return nil
		}
		c.sel = i
		return c.open(c.side[i])
	}
	switch {
	case k.Code == input.Up || (k.Code == input.Rune && k.Rune == 'k'):
		return move(c.sel - 1)
	case k.Code == input.Down || (k.Code == input.Rune && k.Rune == 'j'):
		return move(c.sel + 1)
	case k.Code == input.Home:
		return move(0)
	case k.Code == input.End:
		return move(len(c.side) - 1)
	case k.Code == input.Rune && k.Rune >= '1' && k.Rune <= '9' && int(k.Rune-'1') < len(c.side):
		cmd := move(int(k.Rune - '1'))
		c.paneFocus = true
		return cmd
	case k.Code == input.Enter || k.Code == input.Right || k.Code == input.Tab:
		c.paneFocus = true
		return nil
	case k.Code == input.Rune && k.Rune == '?':
		c.scr, c.paneFocus = scrHelp, true
		return nil
	case k.Code == input.Rune && k.Rune == 'r':
		return c.open(c.side[c.sel])
	case k.Code == input.Rune && k.Rune == 'q', k.Code == input.Esc:
		return Quit
	}
	return nil
}

// routeTo delivers a message to one screen.
func (c *Console) routeTo(s screenID, m Msg) Cmd {
	d := def(s)
	if d == nil {
		d = def(scrHome)
	}
	return d.get(c).update(c, m)
}

func isBack(k KeyMsg) bool {
	return k.Code == input.Esc || (k.Code == input.Rune && k.Rune == 'q' && !k.Ctrl && !k.Alt)
}

// goTo shows screen s on its sidebar item; focus gives it the keyboard.
func (c *Console) goTo(s screenID, focus bool) Cmd {
	c.sel = max(0, slices.Index(c.side, s))
	c.paneFocus = focus
	return c.open(s)
}

func (c *Console) open(s screenID) Cmd {
	c.scr = s
	if d := def(s); d != nil && d.open != nil {
		return d.open(c)
	}
	return nil
}

// --- frame ------------------------------------------------------------------------

const sideW = 22

// narrowSideW fits the longest label with the selection bar, and no more.
func narrowSideW() int {
	n := 0
	for _, s := range sidebarIDs() {
		n = max(n, style.Width(label(s)))
	}
	return n + 4
}

func (c *Console) View(w, h int) []string {
	if w < 60 || h < 16 {
		return style.Place([]string{"L80 needs at least 60×16 to draw.", fmt.Sprintf("This terminal is %d×%d.", w, h), "", "Ctrl-C quits."}, w, h)
	}
	t := c.th
	left := " " + t.R(t.Accent, "L80") + t.R(t.Title, " console") + t.R(t.Muted, " · "+c.d.Version)
	host := strings.TrimPrefix(strings.TrimPrefix(c.d.Config.BaseURL, "https://"), "http://")
	right := host + " · " + t.R(t.Warn, "no token")
	if c.d.Config.HasToken() {
		right = host + " · token " + t.R(t.OK, "✓")
	}
	if c.running > 0 {
		right = widgets.Spin(c.tick) + " " + fmt.Sprint(c.running) + " running  ·  " + right
	}
	header := style.PadRight(left, w-style.Width(right)-1) + t.R(t.Muted, right) + " "

	bodyH := h - 2
	innerW, innerH := w-2, bodyH-2
	sw := sideW
	if innerW < 80 {
		sw = narrowSideW()
	}
	pw := innerW - sw - 1
	padW := pw - 4

	// Sidebar: the selected item gets an accent bar; brighter while it has focus.
	// When the list is taller than the pane it scrolls to keep the selection in view.
	var side []string
	side = append(side, "")
	first, last := sideWindow(len(c.side), c.sel, innerH-1)
	for i := first; i < last; i++ {
		name := style.Truncate(label(c.side[i]), sw-4, "…")
		switch {
		case (i == first && first > 0) || (i == last-1 && last < len(c.side)):
			side = append(side, "   "+t.R(t.Muted, "⋮"))
		case i == c.sel && c.scr != scrHelp:
			bar, st := t.R(t.Accent, "▌"), t.Selected
			if c.paneFocus {
				st = t.Title
			}
			side = append(side, " "+bar+t.R(st, style.PadRight(" "+name, sw-3)))
		default:
			side = append(side, "   "+t.R(t.Muted, name))
		}
	}

	// Pane: title, one line of context, then the screen itself.
	var body []string
	var keys []widgets.Binding
	contentH := innerH - 4
	d := def(c.scr)
	if d == nil {
		d = def(scrHome)
	}
	body, keys = d.get(c).view(c, padW, contentH)
	pane := []string{"", t.R(t.Title, title(c.scr)), t.R(t.Muted, style.Truncate(blurb(c.scr), padW, "…")), ""}
	pane = append(pane, body...)
	for i := range pane {
		pane[i] = "  " + pane[i]
	}
	sep := make([]string, innerH)
	for i := range sep {
		sep[i] = t.R(t.Edge, "│")
	}
	inner := style.JoinH(style.Fit(side, sw, innerH), sep, style.Fit(pane, pw, innerH))
	frame := []string{header}
	frame = append(frame, style.Box(t.Border, t.P, t.Edge, "", inner, w, bodyH)...)

	if !c.paneFocus {
		keys = []widgets.Binding{{Keys: "↑↓", Desc: "move"}, {Keys: "enter", Desc: "open"}, {Keys: fmt.Sprintf("1-%d", min(9, len(c.side))), Desc: "jump"}, {Keys: "?", Desc: "help"}, {Keys: "q", Desc: "quit"}}
	} else if !slices.ContainsFunc(keys, func(b widgets.Binding) bool { return b.Keys == "esc" || strings.HasPrefix(b.Desc, "back") }) {
		keys = append(keys, widgets.Binding{Keys: "esc", Desc: "back"})
	}
	footer := widgets.HelpLine(t, w-2, keys...)
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

// sideWindow is the [first, last) slice of n sidebar items that fits in h
// rows with sel inside it. A clipped end shows "⋮" in place of its item, so
// sel is kept one row away from a clipped edge.
func sideWindow(n, sel, h int) (first, last int) {
	if h <= 0 || n <= h {
		return 0, n
	}
	first = max(0, min(sel-h/2, n-h))
	last = first + h
	return first, last
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
