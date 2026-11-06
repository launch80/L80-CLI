package tui

import (
	"slices"
	"strings"

	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

// The screen registry. A screen is one file: it declares its screenID, keeps
// its state somewhere it can reach from the Console, and registers itself in
// an init func. The sidebar, routing, the pane title and blurb all come from
// here, so nothing in console.go names a screen except Home and Help.

// screenID names a screen. It is a string so each file can declare its own.
type screenID string

const (
	scrHome    screenID = "home"
	scrBench   screenID = "bench"
	scrVast    screenID = "vast"
	scrPwdet   screenID = "pwdet"
	scrDash    screenID = "dash"
	scrHistory screenID = "history"
	scrVerify  screenID = "verify"
	scrTools   screenID = "tools"
	scrHelp    screenID = "help"
	scrAbout   screenID = "about"
	scrOnboard screenID = "onboard"
	scrRecs    screenID = "recs"
	scrTrain   screenID = "train"
	scrQuality screenID = "quality"
)

// screenModel is what the console needs from a page. A screen signals "back" by
// setting c.scr = scrHome, which hands the keyboard back to the sidebar.
type screenModel interface {
	update(c *Console, m Msg) Cmd
	view(c *Console, w, h int) ([]string, []widgets.Binding)
}

// screenDef registers one screen.
type screenDef struct {
	id    screenID
	label string // sidebar label, and the pane title unless title is set
	title string
	blurb string // the one line under the pane title
	side  bool   // listed in the sidebar
	// get returns the screen's state on c (usually a field of Console).
	get func(c *Console) screenModel
	// open runs when the screen is shown; optional.
	open func(c *Console) Cmd
}

var registry = map[screenID]*screenDef{}

// register adds a screen; a duplicate id is a programming error.
func register(d screenDef) {
	if _, dup := registry[d.id]; dup {
		panic("tui: screen registered twice: " + string(d.id))
	}
	registry[d.id] = &d
}

// def looks a screen up; the zero screenID is Home, where the console starts.
func def(s screenID) *screenDef {
	if s == "" {
		s = scrHome
	}
	return registry[s]
}

func label(s screenID) string {
	if d := def(s); d != nil {
		return d.label
	}
	return string(s)
}

func title(s screenID) string {
	if d := def(s); d != nil && d.title != "" {
		return d.title
	}
	return label(s)
}

func blurb(s screenID) string {
	if d := def(s); d != nil {
		return d.blurb
	}
	return ""
}

// sidebarIDs is the console's navigation: Home, then the rest A to Z by label
// (a test keeps it that way). The number keys jump to the first nine.
func sidebarIDs() []screenID {
	var rest []screenID
	for id, d := range registry {
		if d.side && id != scrHome {
			rest = append(rest, id)
		}
	}
	slices.SortFunc(rest, func(a, b screenID) int { return strings.Compare(strings.ToLower(label(a)), strings.ToLower(label(b))) })
	return append([]screenID{scrHome}, rest...)
}

// allIDs is every registered screen, in a stable order.
func allIDs() []screenID {
	var out []screenID
	for id := range registry {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// screenFunc adapts a stateless view (Help, the coming-soon pages) to screenModel.
type screenFunc func(c *Console, w, h int) ([]string, []widgets.Binding)

func (f screenFunc) update(c *Console, m Msg) Cmd {
	if k, ok := m.(KeyMsg); ok && isBack(k) {
		c.scr = scrHome
	}
	return nil
}

func (f screenFunc) view(c *Console, w, h int) ([]string, []widgets.Binding) { return f(c, w, h) }

// The screens that predate the registry. Their state stays a field of
// Console; new screens register from their own file.
func init() {
	register(screenDef{id: scrHome, label: "Home", side: true, blurb: "Server, token and tools at a glance.",
		get: func(c *Console) screenModel { return &c.home }, open: func(c *Console) Cmd { return c.home.load(c) }})
	register(screenDef{id: scrBench, label: "Benchmark", side: true, blurb: "Run BetterBench against an endpoint and publish the result.",
		get: func(c *Console) screenModel { return &c.bench }})
	register(screenDef{id: scrVast, label: "Rent (vast)", side: true, blurb: "Rent a Vast.ai GPU, serve, benchmark, publish, tear down.",
		get: func(c *Console) screenModel { return &c.vast }, open: func(c *Console) Cmd { return c.vast.loadRuns(c) }})
	register(screenDef{id: scrPwdet, label: "Poison Detector", side: true, blurb: "Check model weights for tampering.",
		get: func(c *Console) screenModel { return &c.pw }})
	register(screenDef{id: scrDash, label: "Dashboard", side: true, blurb: "GPU and inference telemetry for this machine.",
		get: func(c *Console) screenModel { return &c.dash }, open: func(c *Console) Cmd { return c.dash.load(c) }})
	register(screenDef{id: scrHistory, label: "History", side: true, blurb: "What this key has published, and each page's tier.",
		get: func(c *Console) screenModel { return &c.hist }, open: func(c *Console) Cmd { return c.hist.load(c) }})
	register(screenDef{id: scrVerify, label: "Verify", side: true, blurb: "Review others' runs: reproduce them, and stamp them if you are a verifier.",
		get: func(c *Console) screenModel { return &c.verify }, open: func(c *Console) Cmd { return c.verify.load(c) }})
	register(screenDef{id: scrTools, label: "Tools", side: true, blurb: "The pinned tools and how each resolves here.",
		get: func(c *Console) screenModel { return &c.tools }, open: func(c *Console) Cmd { return c.tools.load(c, false) }})
	register(screenDef{id: scrHelp, label: "Help", blurb: "Keys, commands and environment.",
		get: func(*Console) screenModel { return screenFunc(helpView) }})
	register(screenDef{id: scrAbout, label: "About", side: true, blurb: "Who we are, where to find us, and every command.",
		get: func(c *Console) screenModel { return &c.about }, open: func(c *Console) Cmd { c.about.off = 0; return nil }})
	register(screenDef{id: scrOnboard, label: "Onboarding", side: true, blurb: "A short tour that sets L80 up on this machine.",
		get: func(c *Console) screenModel { return &c.onb }, open: func(c *Console) Cmd { return c.onb.start(c) }})
	register(screenDef{id: scrQuality, label: "QualityBench", side: true, blurb: "Measure quality and quality loss on a local server, or plan a rented run.",
		get: func(c *Console) screenModel { return &c.qb }, open: func(c *Console) Cmd { return c.qb.load(c) }})
	// Deferred: registered so they can be shown, kept out of the sidebar until they ship.
	register(screenDef{id: scrRecs, label: "Recommendations", blurb: "Coming soon: what to run a model on, from the community's results.",
		get: func(*Console) screenModel { return soonScreen(scrRecs) }})
	register(screenDef{id: scrTrain, label: "Train & Tune", title: "Train, Tune, Quantize", blurb: "Coming soon: train, fine-tune and quantize models, then benchmark them.",
		get: func(*Console) screenModel { return soonScreen(scrTrain) }})
}

func soonScreen(s screenID) screenModel {
	return screenFunc(func(c *Console, w, h int) ([]string, []widgets.Binding) { return soonView(c, s, w, h) })
}

// stateOf is the state of a screen that registers from its own file: it
// lives in c.pages, created zeroed on first use, so adding a screen never
// means adding a field to Console.
func stateOf[T any](c *Console, id screenID) *T {
	if c.pages == nil {
		c.pages = map[screenID]any{}
	}
	if s, ok := c.pages[id].(*T); ok {
		return s
	}
	s := new(T)
	c.pages[id] = s
	return s
}
