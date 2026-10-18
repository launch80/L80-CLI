package tui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/launch80/L80-CLI/internal/api"
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
		VastEnv: func(string) ([]string, error) { return nil, nil }, RunsDir: t.TempDir(), DashboardState: t.TempDir()}}
	c.prog = NewProgram(c)
	c.th = widgets.DefaultTheme(style.NoColor, true)
	c.menu = widgets.List{Items: []widgets.Item{{Title: "Benchmark an endpoint", Value: scrBench}, {Title: "Rent a GPU and benchmark", Value: scrVast},
		{Title: "Scan weights for tampering", Value: scrPwdet}, {Title: "Telemetry dashboard", Value: scrDash},
		{Title: "Published pages", Value: scrHistory}, {Title: "Tools", Value: scrTools}, {Title: "Help", Value: scrHelp}}}
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
		for _, want := range []string{"What do you want to do?", "Benchmark an endpoint", "This machine", "reachable"} {
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
	feed(c, input.Key{Code: input.Rune, Rune: '7'})
	if c.scr != scrHelp || !strings.Contains(strings.Join(c.View(100, 30), "\n"), "Everything here is also a command") {
		t.Fatal("help screen")
	}
	feed(c, input.Key{Code: input.Esc})
	if c.scr != scrHome {
		t.Fatal("esc returns home")
	}
	// Vast: starting a real run asks first, quoting the spec's cost caps.
	spec := filepath.Join(t.TempDir(), "run.yaml")
	os.WriteFile(spec, []byte("name: t\ngpu:\n  names: [H100]\n  count: 2\n  max_dph: 6.5\nbudget:\n  max_total_usd: 20\n"), 0o644)
	feed(c, input.Key{Code: input.Rune, Rune: '2'})
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

	feed(c, input.Key{Code: input.Rune, Rune: '1'})
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
