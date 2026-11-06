package tui

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
)

func view(t *testing.T, c *Console) string {
	t.Helper()
	return style.Strip(checkFrame(t, c.View(110, 34), 110, 34))
}

func TestSettingsLoginLogoutNeverShowsTheToken(t *testing.T) {
	c := newTestConsole(t)
	c.d.Config.CredPath = config.CredentialsPath()
	feed(c, SizeMsg{110, 34})
	jumpTo(t, c, scrSettings)
	if v := view(t, c); !strings.Contains(v, "api base") || !strings.Contains(v, "Log in") || !strings.Contains(v, "none") {
		t.Fatalf("settings:\n%s", v)
	}
	feed(c, input.Key{Code: input.Enter}) // Log in
	feed(c, PasteMsg{Text: "not-a-token"})
	feed(c, input.Key{Code: input.Enter})
	if c.d.Config.HasToken() || !strings.Contains(settings(c).msg, "does not look like") {
		t.Fatalf("a malformed token must not be saved: %q", settings(c).msg)
	}
	settings(c).token.Set("L80_k7_supersecret")
	feed(c, input.Key{Code: input.Enter})
	if !c.d.Config.HasToken() || c.d.Config.KeyID != "k7" || settings(c).typing {
		t.Fatalf("login: %+v typing=%v", c.d.Config.KeyID, settings(c).typing)
	}
	if v := view(t, c); strings.Contains(v, "supersecret") || !strings.Contains(v, "k7") {
		t.Errorf("token shown, or key id missing:\n%s", v)
	}
	if _, err := os.Stat(config.CredentialsPath()); err != nil {
		t.Fatal(err)
	}
	// Log out asks first, then removes the file.
	feed(c, input.Key{Code: input.Down})
	feed(c, input.Key{Code: input.Enter})
	if c.dialog == nil || c.dialog.Choice != 1 {
		t.Fatal("log out must ask, Cancel focused")
	}
	feed(c, input.Key{Code: input.Left})
	feed(c, input.Key{Code: input.Enter})
	if c.d.Config.HasToken() {
		t.Error("still logged in")
	}
	if _, err := os.Stat(config.CredentialsPath()); !os.IsNotExist(err) {
		t.Errorf("credentials file left behind: %v", err)
	}
}

func TestSettingsUpdateCheckAndRun(t *testing.T) {
	c := newTestConsole(t)
	var calls [][]string
	c.d.Update = func(args []string, stdout, stderr io.Writer) int {
		calls = append(calls, args)
		if len(args) > 0 && args[0] == "--check" {
			fmt.Fprint(stdout, `{"current":"0.7.0","latest":"0.8.0","update_available":true,"notes":"- Console: Settings\n- Faster skills","actions":["Run `+"`L80 update`"+`: it installs what --check found."],"up_to_date":false}`)
			return 0
		}
		fmt.Fprintln(stdout, "updated L80 0.7.0 -> 0.8.0")
		return 0
	}
	feed(c, SizeMsg{110, 34})
	jumpTo(t, c, scrSettings)
	feed(c, input.Key{Code: input.Down})
	feed(c, input.Key{Code: input.Down})
	feed(c, input.Key{Code: input.Enter}) // Check for updates
	v := view(t, c)
	for _, want := range []string{"0.8.0 available", "What's new in 0.8.0", "• Console: Settings", "Faster skills"} {
		if !strings.Contains(v, want) {
			t.Errorf("check missing %q:\n%s", want, v)
		}
	}
	feed(c, input.Key{Code: input.Down})
	feed(c, input.Key{Code: input.Enter}) // Update L80
	if c.dialog == nil || !strings.Contains(c.dialog.Body, "0.8.0") || c.dialog.Choice != 1 {
		t.Fatalf("update must ask first: %+v", c.dialog)
	}
	feed(c, input.Key{Code: input.Esc})
	if len(calls) != 1 {
		t.Fatalf("cancel ran it: %v", calls)
	}
	feed(c, input.Key{Code: input.Enter})
	feed(c, input.Key{Code: input.Left})
	feed(c, input.Key{Code: input.Enter})
	if len(calls) < 2 || calls[1] != nil {
		t.Fatalf("calls = %v", calls)
	}
	if v := view(t, c); !strings.Contains(v, "updated L80 0.7.0 -> 0.8.0") {
		t.Errorf("update log:\n%s", v)
	}
}

func TestSkillsInstallPerTarget(t *testing.T) {
	c := newTestConsole(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	os.MkdirAll(filepath.Join(home, ".gemini"), 0o755)
	feed(c, SizeMsg{110, 34})
	jumpTo(t, c, scrSkills)
	v := view(t, c)
	for _, want := range []string{"claude-code", "gemini", "detected", "4 not installed"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
	for skillsOf(c).targets[skillsOf(c).table.Cursor].t.Name != "gemini" {
		feed(c, input.Key{Code: input.Down})
	}
	feed(c, input.Key{Code: input.Rune, Rune: 'i'})
	if _, err := os.Stat(filepath.Join(home, ".gemini", "skills", "l80", "SKILL.md")); err != nil {
		t.Fatalf("not installed: %v", err)
	}
	if v := view(t, c); !strings.Contains(v, "gemini: installed 4 skills") || !strings.Contains(v, "4 current") {
		t.Errorf("after install:\n%s", v)
	}
	// Linking needs a checkout; outside one it says so and changes nothing.
	wd, _ := os.Getwd()
	os.Chdir(t.TempDir())
	defer os.Chdir(wd)
	feed(c, input.Key{Code: input.Rune, Rune: 'l'})
	if c.dialog != nil || !strings.Contains(c.flash, "checkout") {
		t.Errorf("link outside a checkout: dialog=%v flash=%q", c.dialog, c.flash)
	}
}

func TestTemplatesListAndDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/templates" {
			fmt.Fprint(w, `{"ok":true,"data":[{"id":"bench.report.v1","version":1,"label":"BetterBench report","schema_url":"https://l80.test/schemas/bench.report.v1.json"},{"id":"generic.report.v1","version":2,"label":"Report"}]}`)
			return
		}
		fmt.Fprint(w, `{"ok":true,"data":{"service":"x","templates":[]}}`)
	}))
	defer srv.Close()
	c := newTestConsole(t)
	c.d.Config.BaseURL, c.d.Client = srv.URL, api.NewClient(srv.URL, "", "test")
	feed(c, SizeMsg{110, 34})
	jumpTo(t, c, scrTemplates)
	v := view(t, c)
	for _, want := range []string{"2 template(s)", "bench.report.v1", "BetterBench report", "v2"} {
		if !strings.Contains(v, want) {
			t.Errorf("list missing %q:\n%s", want, v)
		}
	}
	feed(c, input.Key{Code: input.Enter})
	v = view(t, c)
	for _, want := range []string{"schemas/bench.report.v1.json", "L80 betterbench", "L80 publish payload.json --template bench.report.v1 --dry-run"} {
		if !strings.Contains(v, want) {
			t.Errorf("detail missing %q:\n%s", want, v)
		}
	}
}

func TestDoctorChecksAndRemedies(t *testing.T) {
	got := doctorChecks(doctorInputs{version: "1.3.0", cfg: config.Config{BaseURL: "https://x", CredPath: "/c.json"},
		healthErr: &api.Error{Code: "E_NETWORK", Message: "dial tcp: refused"},
		tools:     []app.ToolStatus{{Name: "betterbench", Status: "ok", Source: "PATH"}, {Name: "pwdet", Status: "missing", Remedy: "Install uv."}, {Name: "dashboard", Status: "stale"}}})
	want := map[string]string{"L80": "ok", "launch80": "fail", "token": "warn", "uvx": "warn", "betterbench": "ok", "pwdet": "fail", "dashboard": "warn"}
	for _, ck := range got {
		if want[ck.name] != ck.status {
			t.Errorf("%s: %s, want %s", ck.name, ck.status, want[ck.name])
		}
		if ck.status != "ok" && ck.remedy == "" {
			t.Errorf("%s fails with no remedy", ck.name)
		}
	}
	if len(got) != len(want) {
		t.Errorf("%d checks, want %d", len(got), len(want))
	}

	c := newTestConsole(t)
	feed(c, SizeMsg{110, 34})
	jumpTo(t, c, scrDoctor)
	v := view(t, c)
	for _, want := range []string{"launch80", "reachable", "token", "Log in from Settings"} {
		if !strings.Contains(v, want) {
			t.Errorf("doctor missing %q:\n%s", want, v)
		}
	}
}

func TestHomeLauncherOpensScreens(t *testing.T) {
	c := newTestConsole(t)
	feed(c, SizeMsg{110, 34})
	drain(c, c.Init(), 0)
	feed(c, input.Key{Code: input.Enter})
	v := view(t, c)
	for _, want := range []string{"What do you want to do?", "▸ Benchmark speed", "Scan weights", "server", "reachable", "Last publishes"} {
		if !strings.Contains(v, want) {
			t.Errorf("home missing %q:\n%s", want, v)
		}
	}
	rent := -1
	for i, l := range launch {
		if def(l.to) == nil {
			t.Errorf("launcher %q goes to unregistered %s", l.title, l.to)
		}
		if l.title == "Rent a GPU" {
			rent = i
		}
	}
	for range rent {
		feed(c, input.Key{Code: input.Down})
	}
	feed(c, input.Key{Code: input.Enter})
	if c.scr != scrVast || !c.paneFocus || c.side[c.sel] != scrVast {
		t.Fatalf("launch: scr=%v sel=%d focus=%v", c.scr, c.sel, c.paneFocus)
	}
	feed(c, input.Key{Code: input.Esc})
	if c.paneFocus || c.scr != scrVast {
		t.Errorf("back: scr=%v focus=%v", c.scr, c.paneFocus)
	}
}
