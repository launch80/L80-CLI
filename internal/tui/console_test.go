package tui

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/betterbench"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

// newTestConsole builds a console the way Run does, without a terminal.
func newTestConsole(t *testing.T) *Console {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"data":{"service":"x","version":"v1","templates":["bench.betterbench.v1"]}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Config{BaseURL: srv.URL}
	c := &Console{d: Deps{Version: "0.7.0", Config: cfg, Client: api.NewClient(srv.URL, "", "test"),
		VastEnv: func(string) ([]string, error) { return nil, nil }, RunsDir: t.TempDir(), DashboardState: t.TempDir(),
		Usage: "L80 — test\n\nUsage:\n  Tools (free, no token):\n  L80 pwdet scan <model> [--json] [--publish]    Check weights for tampering (Poisoned Weights Detector)\n"}}
	c.prog = NewProgram(c)
	c.th = widgets.DefaultTheme(style.NoColor, true)
	c.side = sidebar
	c.bench.init()
	c.vast.init()
	c.pw.init()
	return c
}

func feed(c *Console, m Msg) {
	_, cmd := c.Update(m)
	drain(c, cmd, 0)
}

// drain runs commands synchronously, as the program would asynchronously.
func drain(c *Console, cmd Cmd, depth int) {
	if cmd == nil || depth > 4 {
		return
	}
	switch m := cmd().(type) {
	case nil:
	case batch:
		for _, sub := range m {
			drain(c, sub, depth+1)
		}
	case tickMsg:
	default:
		_, next := c.Update(m)
		drain(c, next, depth+1)
	}
}

func checkFrame(t *testing.T, frame []string, w, h int) string {
	t.Helper()
	if len(frame) != h {
		t.Errorf("frame has %d lines, want %d", len(frame), h)
	}
	for i, l := range frame {
		if style.Width(l) > w {
			t.Errorf("line %d is %d wide (> %d): %q", i, style.Width(l), w, l)
		}
	}
	return strings.Join(frame, "\n")
}

func TestHomeAtSeveralSizes(t *testing.T) {
	c := newTestConsole(t)
	drain(c, c.Init(), 0)
	for _, sz := range [][2]int{{80, 24}, {120, 40}, {60, 16}} {
		feed(c, SizeMsg{sz[0], sz[1]})
		got := checkFrame(t, c.View(sz[0], sz[1]), sz[0], sz[1])
		for _, want := range []string{"L80 console", "Benchmark", "Dashboard", "Server, token and tools", "reachable", "↑↓ move"} {
			if !strings.Contains(got, want) {
				t.Errorf("%dx%d: missing %q\n%s", sz[0], sz[1], want, got)
			}
		}
	}
	if got := strings.Join(c.View(40, 10), "\n"); !strings.Contains(got, "at least 60×16") {
		t.Errorf("small terminal: %s", got)
	}
}

func TestNavigationAndDialog(t *testing.T) {
	c := newTestConsole(t)
	feed(c, SizeMsg{100, 30})
	feed(c, input.Key{Code: input.Rune, Rune: '?'})
	if c.scr != scrHelp || !strings.Contains(strings.Join(c.View(100, 30), "\n"), "Everything here is also a command") {
		t.Fatal("help screen")
	}
	feed(c, input.Key{Code: input.Esc})
	if c.scr != scrHome || c.paneFocus {
		t.Fatal("esc returns to the sidebar")
	}
	// Vast: starting a real run asks first, quoting the spec's cost caps.
	spec := filepath.Join(t.TempDir(), "run.yaml")
	os.WriteFile(spec, []byte("name: t\ngpu:\n  names: [H100]\n  count: 2\n  max_dph: 6.5\nbudget:\n  max_total_usd: 20\n"), 0o644)
	feed(c, input.Key{Code: input.Rune, Rune: '9'})
	feed(c, input.Key{Code: input.Rune, Rune: 'n'})
	c.vast.form.Fields[0].Input.Set(spec)
	c.vast.form.Fields[3].On = false // not a dry run
	c.vast.form.Focus = len(c.vast.form.Fields)
	feed(c, input.Key{Code: input.Enter})
	if c.dialog == nil {
		t.Fatal("renting must ask first")
	}
	view := strings.Join(c.View(100, 30), "\n")
	for _, want := range []string{"Rent a GPU", "$6.5/hr", "$20", "Watchdog: destroy after 2h0m0s"} {
		if !strings.Contains(view, want) {
			t.Errorf("dialog missing %q:\n%s", want, view)
		}
	}
	if c.dialog.Choice != 1 {
		t.Error("the safe choice must be focused")
	}
	feed(c, input.Key{Code: input.Esc})
	if c.dialog != nil || c.vast.job != nil {
		t.Error("cancel must not start anything")
	}
}

func TestBenchJobStreamsAndSummarises(t *testing.T) {
	c := newTestConsole(t)
	feed(c, SizeMsg{120, 36})
	fixture, _ := filepath.Abs("../betterbench/testdata/results_schema2_v061.json")
	dir := t.TempDir()
	script := "#!/bin/sh\n[ \"$1\" = --version ] && { echo 'BetterBench 0.6.1'; exit 0; }\necho 'measuring chat'\n" +
		"out=''; while [ $# -gt 0 ]; do [ \"$1\" = --out ] && out=$2; shift; done\ncp " + fixture + " \"$out\"\n"
	os.WriteFile(filepath.Join(dir, "betterbench"), []byte(script), 0o755)
	t.Setenv("L80_BETTERBENCH", filepath.Join(dir, "betterbench"))
	wd, _ := os.Getwd()
	os.Chdir(t.TempDir())
	defer os.Chdir(wd)

	feed(c, input.Key{Code: input.Rune, Rune: '3'})
	c.bench.form.Fields[0].Input.Set("http://h/v1")
	c.bench.form.Fields[1].Input.Set("m")
	c.bench.form.Fields[5].Input.Set("sk-secret")
	c.bench.form.Focus = len(c.bench.form.Fields)
	feed(c, input.Key{Code: input.Enter})
	deadline := time.Now().Add(5 * time.Second)
	for c.bench.job.running && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	view := strings.Join(c.View(120, 36), "\n")
	if c.bench.phase != 2 || !strings.Contains(view, "measuring chat") || !strings.Contains(view, "Combined decode") {
		t.Errorf("run screen:\n%s", view)
	}
	if !strings.Contains(view, "prefix cache") {
		t.Error("cache-hit warning must be shown before publishing")
	}
	if strings.Contains(view, "sk-secret") {
		t.Error("API key shown")
	}
	// Without a token, publishing explains instead of failing.
	feed(c, input.Key{Code: input.Rune, Rune: 'p'})
	if !c.bad || !strings.Contains(c.flash, "needs a token") {
		t.Errorf("flash = %q", c.flash)
	}
}

func TestOverlayKeepsWidth(t *testing.T) {
	base := strings.Repeat("x", 40)
	got := overlay(base, "[dialog]", 10, 40)
	if style.Width(got) != 40 || !strings.Contains(got, "xxxxxxxxxx[dialog]xxx") {
		t.Errorf("overlay = %q", got)
	}
}

func TestSidebarPreviewsAndFocus(t *testing.T) {
	c := newTestConsole(t)
	feed(c, SizeMsg{100, 30})
	drain(c, c.Init(), 0)
	// Moving the sidebar previews each screen without taking the keyboard.
	for range 8 {
		feed(c, input.Key{Code: input.Down})
	}
	if c.scr != scrVast || c.paneFocus {
		t.Fatalf("preview: scr=%v focus=%v", c.scr, c.paneFocus)
	}
	view := checkFrame(t, c.View(100, 30), 100, 30)
	for _, want := range []string{"Rent a Vast.ai GPU", "Home", "Rent (vast)", "Tools", "enter open", "1-9 jump"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
	// Enter gives the pane the keyboard; esc hands it back on the same item.
	feed(c, input.Key{Code: input.Enter})
	if !c.paneFocus || !strings.Contains(strings.Join(c.View(100, 30), "\n"), "esc back") {
		t.Fatal("enter focuses the pane")
	}
	feed(c, input.Key{Code: input.Esc})
	if c.paneFocus || c.scr != scrVast || c.sel != 8 {
		t.Fatalf("esc: scr=%v sel=%d focus=%v", c.scr, c.sel, c.paneFocus)
	}
	// Up from the top wraps to the bottom; enter on Home keeps the sidebar.
	feed(c, input.Key{Code: input.Rune, Rune: '1'})
	feed(c, input.Key{Code: input.Enter})
	if c.paneFocus {
		t.Error("Home has nothing to operate; the sidebar keeps focus")
	}
	feed(c, input.Key{Code: input.Up})
	if c.scr != scrVerify {
		t.Errorf("wrap: scr=%v", c.scr)
	}
}

func TestVerifyScreenQueueAndStamp(t *testing.T) {
	raw, _ := os.ReadFile("../betterbench/testdata/results_schema2_v061.json")
	fixture, err := betterbench.PrepareRaw(raw, betterbench.Prose{})
	if err != nil {
		t.Fatal(err)
	}
	var decided string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/keys/me":
			fmt.Fprint(w, `{"ok":true,"data":{"key_id":"k2","is_verifier":true}}`)
		case r.URL.Path == "/api/v1/verifications" && r.URL.Query().Get("scope") == "queue":
			fmt.Fprint(w, `{"ok":true,"data":[{"id":"v1","artifact_guid":"g1","url":"https://l80.test/a/g1","title":"Qwen3-8B on vLLM","requester_label":"lab-mi300","status":"requested","requested_at":"2026-10-17T12:00:00Z"}]}`)
		case r.URL.Path == "/api/v1/verifications":
			fmt.Fprint(w, `{"ok":true,"data":[]}`)
		case r.URL.Path == "/api/v1/artifacts/g1/payload":
			fmt.Fprintf(w, `{"ok":true,"data":%s}`, fixture)
		case r.URL.Path == "/api/v1/verifications/v1/decision":
			b, _ := io.ReadAll(r.Body)
			decided = string(b)
			fmt.Fprint(w, `{"ok":true,"data":{"id":"v1","status":"verified","url":"https://l80.test/a/g1"}}`)
		default:
			fmt.Fprint(w, `{"ok":true,"data":{"service":"x","templates":[]}}`)
		}
	}))
	defer srv.Close()
	c := newTestConsole(t)
	c.d.Config = config.Config{BaseURL: srv.URL, Token: "L80_k2_secret", KeyID: "k2"}
	c.d.Client = api.NewClient(srv.URL, c.d.Config.Token, "test")
	feed(c, SizeMsg{120, 36})
	feed(c, input.Key{Code: input.End}) // Verify is last; past the number keys
	feed(c, input.Key{Code: input.Enter})
	view := checkFrame(t, c.View(120, 36), 120, 36)
	for _, want := range []string{"Verify", "Queue (1)", "Qwen3-8B on vLLM", "verifier", "lab-mi300"} {
		if !strings.Contains(view, want) {
			t.Fatalf("queue missing %q:\n%s", want, view)
		}
	}
	feed(c, input.Key{Code: input.Enter})
	view = checkFrame(t, c.View(120, 36), 120, 36)
	for _, want := range []string{"To reproduce", "Published figures", "Combined decode", "p reproduce"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail missing %q:\n%s", want, view)
		}
	}
	// A verifier can stamp on review alone; it asks first, Cancel focused.
	feed(c, input.Key{Code: input.Rune, Rune: 'a'})
	if c.dialog == nil || !strings.Contains(c.dialog.Body, "review alone") || c.dialog.Choice != 1 {
		t.Fatalf("stamp must ask first: %+v", c.dialog)
	}
	feed(c, input.Key{Code: input.Left})
	feed(c, input.Key{Code: input.Enter})
	if !strings.Contains(decided, `"decision":"verified"`) || strings.Contains(decided, "reproduction") {
		t.Errorf("decision sent = %s", decided)
	}
	if !strings.Contains(c.flash, "verified") {
		t.Errorf("flash = %q", c.flash)
	}
}

func TestSidebarIsAlphabetical(t *testing.T) {
	if sidebar[0] != scrHome {
		t.Fatal("Home stays on top")
	}
	names := []string{}
	for _, s := range sidebar[1:] {
		names = append(names, strings.ToLower(labels[s]))
	}
	if !slices.IsSorted(names) {
		t.Errorf("sidebar after Home is not A-Z: %v", names)
	}
}

func TestAboutAndComingSoon(t *testing.T) {
	c := newTestConsole(t)
	c.side = sidebar
	feed(c, SizeMsg{80, 24})
	feed(c, input.Key{Code: input.Rune, Rune: '2'})
	if c.scr != scrAbout || !c.paneFocus {
		t.Fatalf("scr=%v", c.scr)
	}
	view := checkFrame(t, c.View(80, 24), 80, 24)
	if !strings.Contains(view, "Our story") || !strings.Contains(view, "more lines") {
		t.Errorf("about top:\n%s", view)
	}
	all := strings.Join(c.about.lines(c, 60), "\n")
	for _, want := range []string{"discord.gg/launch80", "launch80.com/supporter"} {
		if !strings.Contains(all, want) {
			t.Errorf("about missing %q", want)
		}
	}
	feed(c, input.Key{Code: input.End})
	view = checkFrame(t, c.View(80, 24), 80, 24)
	for _, want := range []string{"Command directory", "L80 pwdet scan", "Check weights"} {
		if !strings.Contains(view, want) {
			t.Errorf("about end missing %q:\n%s", want, view)
		}
	}
	feed(c, input.Key{Code: input.Esc})
	feed(c, input.Key{Code: input.Rune, Rune: '8'})
	view = checkFrame(t, c.View(80, 24), 80, 24)
	if c.scr != scrRecs || !strings.Contains(view, "Coming soon") {
		t.Errorf("recommendations:\n%s", view)
	}
	feed(c, input.Key{Code: input.Esc})
	for c.scr != scrTrain {
		feed(c, input.Key{Code: input.Down})
	}
	view = checkFrame(t, c.View(80, 24), 80, 24)
	if !strings.Contains(view, "Train, Tune, Quantize") || !strings.Contains(view, "Train & Tune") || !strings.Contains(view, "Recommendations") {
		t.Errorf("train:\n%s", view)
	}
}

func TestOnboardingFlow(t *testing.T) {
	c := newTestConsole(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	os.MkdirAll(filepath.Join(home, ".codex"), 0o755)
	for _, n := range []string{"L80_BETTERBENCH", "L80_VAST", "L80_PWDET", "L80_DASHBOARD"} {
		t.Setenv(n, "true")
	}
	c.d.Onboard = true
	c.d.Config.CredPath = config.CredentialsPath()
	feed(c, SizeMsg{100, 30})
	drain(c, c.Init(), 0)
	if c.scr != scrOnboard || !c.paneFocus {
		t.Fatalf("first run opens onboarding: scr=%v", c.scr)
	}
	enter := input.Key{Code: input.Enter}
	feed(c, enter) // welcome -> machine
	view := checkFrame(t, c.View(100, 30), 100, 30)
	if !strings.Contains(view, "Step 2 of 6") || !strings.Contains(view, "betterbench") {
		t.Errorf("machine step:\n%s", view)
	}
	feed(c, enter) // -> token
	feed(c, PasteMsg{Text: "not-a-token"})
	feed(c, enter)
	if c.onb.step != obToken || !c.onb.tokenBad {
		t.Fatal("a malformed token must not be saved")
	}
	c.onb.token.Set("L80_k7_secret")
	feed(c, enter)
	if c.onb.step != obGoal || !c.d.Config.HasToken() {
		t.Fatalf("token: step=%d msg=%q", c.onb.step, c.onb.tokenMsg)
	}
	if strings.Contains(strings.Join(c.View(100, 30), "\n"), "secret") {
		t.Error("token shown")
	}
	feed(c, input.Key{Code: input.Down}) // rented GPU
	feed(c, enter)                       // -> agents
	if c.onb.step != obAgents {
		t.Fatalf("step=%d", c.onb.step)
	}
	feed(c, enter) // install into the detected harness
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "l80", "SKILL.md")); err != nil {
		t.Errorf("skill not installed for the detected harness: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini")); err == nil {
		t.Error("installed for a harness that is not there")
	}
	feed(c, enter) // -> done
	if config.LoadState().NeedsOnboarding() {
		t.Error("finishing must be recorded")
	}
	feed(c, enter)
	if c.scr != scrVast || c.paneFocus || c.side[c.sel] != scrVast {
		t.Errorf("lands on the chosen screen: scr=%v sel=%d", c.scr, c.sel)
	}
}

func TestOnboardingSkip(t *testing.T) {
	c := newTestConsole(t)
	feed(c, SizeMsg{100, 30})
	drain(c, c.startOnboarding(), 0)
	feed(c, input.Key{Code: input.Esc})
	if c.scr != scrHome || c.paneFocus || !strings.Contains(c.flash, "L80 onboard") {
		t.Errorf("skip: scr=%v flash=%q", c.scr, c.flash)
	}
	if s := config.LoadState(); s.NeedsOnboarding() || !s.OnboardSkipped {
		t.Errorf("skip must be recorded: %+v", s)
	}
}
