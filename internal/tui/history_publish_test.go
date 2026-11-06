package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/qualitybench"
	"github.com/launch80/L80-CLI/internal/tui/input"
)

// History lists QualityBench results on this machine that are not published
// yet, read from disk so they survive a restart, and p publishes one. A
// BFCL-only run (no logprobs) is one of them.
func TestHistoryPublishesALocalQualityBenchReport(t *testing.T) {
	data, err := os.ReadFile("../qualitybench/testdata/results-bf16.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	json.Unmarshal(data, &doc)
	doc["logprobs"] = map[string]any{}
	delete(doc, "eval_meta")
	var bfcl []any
	for _, x := range doc["tasks"].([]any) {
		if x.(map[string]any)["id"] == "bfcl" {
			bfcl = append(bfcl, x)
		}
	}
	doc["tasks"], doc["title"] = bfcl, "Qwopus INT4 BFCL"
	data, _ = json.Marshal(doc)

	dir := t.TempDir()
	results := filepath.Join(dir, "qualitybench-qwopus-bfcl.json")
	os.WriteFile(results, data, 0o644)
	os.Mkdir(qualitybench.SidecarDir(results), 0o755)
	os.WriteFile(qualitybench.ReportPath(results), []byte("<html>"), 0o644)
	wd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(wd)
	var opened string
	old := openFile
	openFile = func(p string) error { opened = p; return nil }
	defer func() { openFile = old }()

	var posted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/artifacts":
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &posted)
			w.Write([]byte(`{"ok":true,"data":{"guid":"g1","url":"https://launch80.com/a/g1","template_id":"bench.qualitybench.v1","created_at":"2026-11-06T15:00:00Z"}}`))
		case r.URL.Path == "/api/v1/artifacts":
			w.Write([]byte(`{"ok":true,"data":{"key_id":"k1","artifacts":[{"guid":"g0","url":"https://launch80.com/a/g0","template_id":"bench.betterbench.v1","title":"Older run","created_at":"2026-11-04T21:01:00Z","trust_tier":"unverified"}]}}`))
		default:
			w.Write([]byte(`{"ok":true,"data":{}}`))
		}
	}))
	defer srv.Close()

	c := newTestConsole(t)
	c.d.Config = config.Config{BaseURL: srv.URL, Token: "L80_k1_secret", KeyID: "k1"}
	c.d.Client = api.NewClient(srv.URL, c.d.Config.Token, "test")
	feed(c, SizeMsg{120, 44})
	for _, id := range c.side {
		if id == "jobs" {
			t.Fatal("the Jobs screen is gone: its list did not survive a restart")
		}
	}
	jumpTo(t, c, scrHistory)
	v := screen(t, c)
	for _, want := range []string{"1 QualityBench report(s) on this machine not published", "local", "Qwopus INT4 BFCL · not published", "Older run", "p publish", "o open report"} {
		if !strings.Contains(v, want) {
			t.Errorf("history missing %q:\n%s", want, v)
		}
	}
	feed(c, key('o'))
	if opened != qualitybench.ReportPath(results) {
		t.Errorf("opened %q", opened)
	}
	feed(c, code(input.Enter))
	if v := screen(t, c); !strings.Contains(v, "BFCL: 89.0% (356/400)") || !strings.Contains(v, "Not published") {
		t.Errorf("local detail:\n%s", v)
	}
	feed(c, code(input.Esc))

	feed(c, key('p'))
	if c.dialog == nil || c.dialog.Title != "Publish" || c.dialog.Choice != 1 {
		t.Fatalf("publish must ask first, cancel focused: %+v", c.dialog)
	}
	feed(c, code(input.Esc))
	if posted != nil {
		t.Fatal("published without a yes")
	}
	feed(c, key('p'))
	feed(c, code(input.Left))
	feed(c, code(input.Enter))
	if posted == nil || posted["$template"] != "bench.qualitybench.v1" || posted["title"] != "Qwopus INT4 BFCL" {
		t.Fatalf("posted %v; flash %q", posted, c.flash)
	}
	if !strings.Contains(c.flash, "published: https://launch80.com/a/g1") {
		t.Errorf("flash %q", c.flash)
	}
	// Published: it is in this machine's record now, so it is no longer listed as local.
	if v := screen(t, c); strings.Contains(v, "not published") {
		t.Errorf("published report still listed as local:\n%s", v)
	}
}
