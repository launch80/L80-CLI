package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/launch80/L80-CLI/internal/history"
)

// fakeAPI serves the listing and the publish endpoint, recording what it saw.
func fakeAPI(t *testing.T, pages map[string][]map[string]any) (*httptest.Server, *[]string) {
	t.Helper()
	var auth []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/artifacts", func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") != "Bearer L80_k1_secret" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"ok":false,"error":{"code":"E_UNAUTHORIZED","message":"bad token","remedy":"fix it"}}`)
			return
		}
		if r.Method == http.MethodPost {
			fmt.Fprint(w, `{"ok":true,"data":{"guid":"11111111-2222-3333-4444-555555555555","url":"https://l80.test/a/1111","template_id":"bench.betterbench.v1","template_version":1,"trust_tier":"unverified","byte_size":42,"created_at":"2026-09-06T01:02:03.000Z"}}`)
			return
		}
		before := r.URL.Query().Get("before")
		page := pages[before]
		var next any
		if before == "" && len(pages) > 1 {
			next = page[len(page)-1]["created_at"]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{"key_id": "k1", "artifacts": page, "next_before": next}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &auth
}

func art(guid, created, title string) map[string]any {
	return map[string]any{"guid": guid, "url": "https://l80.test/a/" + guid, "template_id": "bench.betterbench.v1",
		"template_version": 1, "trust_tier": "unverified", "title": title, "byte_size": 100, "created_at": created}
}

func runHist(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runHistory(env{stdout: &out, stderr: &errOut}, args)
	return code, out.String(), errOut.String()
}

func TestHistoryListsFromServerNewestFirst(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("L80_TOKEN", "L80_k1_secret")
	srv, auth := fakeAPI(t, map[string][]map[string]any{
		"": {art("g2", "2026-09-06T10:00:00Z", "Second run"), art("g1", "2026-09-05T10:00:00Z", "First run")},
	})
	code, out, errOut := runHist(t, "--api-base", srv.URL)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "2026-09-06 10:00  bench.betterbench.v1   unverified https://l80.test/a/g2") || !strings.Contains(out, "Second run") {
		t.Errorf("out = %s", out)
	}
	if strings.Index(out, "g2") > strings.Index(out, "g1") {
		t.Error("newest must come first")
	}
	if !strings.Contains(out, "2 artifact(s), server record for key k1") {
		t.Errorf("footer missing: %s", out)
	}
	if len(*auth) != 1 || (*auth)[0] != "Bearer L80_k1_secret" {
		t.Errorf("auth = %v", *auth)
	}
	_, out, _ = runHist(t, "--api-base", srv.URL, "--json")
	if !strings.Contains(out, `"source": "server"`) || !strings.Contains(out, `"key_id": "k1"`) {
		t.Errorf("json = %s", out)
	}
}

func TestHistoryAllPages(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("L80_TOKEN", "L80_k1_secret")
	srv, _ := fakeAPI(t, map[string][]map[string]any{
		"":                     {art("g3", "2026-09-06T10:00:00Z", "")},
		"2026-09-06T10:00:00Z": {art("g2", "2026-09-05T10:00:00Z", ""), art("g1", "2026-09-04T10:00:00Z", "")},
	})
	_, out, _ := runHist(t, "--api-base", srv.URL, "--limit", "1", "--all")
	for _, g := range []string{"g3", "g2", "g1"} {
		if !strings.Contains(out, "/a/"+g) {
			t.Errorf("missing %s in %s", g, out)
		}
	}
	if !strings.Contains(out, "3 artifact(s)") {
		t.Errorf("out = %s", out)
	}
}

func TestHistoryFallsBackToLocalWithoutTokenOrServer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("L80_TOKEN", "")
	_ = history.Append(history.Entry{GUID: "loc1", URL: "https://l80.test/a/loc1", TemplateID: "bench.report.v1",
		Title: "Local one", CreatedAt: "2026-09-01T00:00:00Z", SourcePath: "/tmp/r.json"})
	code, out, errOut := runHist(t, "--api-base", "http://127.0.0.1:9")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, "showing the local record") || !strings.Contains(out, "/a/loc1") || !strings.Contains(out, "from /tmp/r.json") {
		t.Errorf("out=%s err=%s", out, errOut)
	}
	// An unreachable server with a token also falls back.
	t.Setenv("L80_TOKEN", "L80_k1_secret")
	code, out, errOut = runHist(t, "--api-base", "http://127.0.0.1:9")
	if code != 0 || !strings.Contains(out, "/a/loc1") || !strings.Contains(errOut, "local record") {
		t.Errorf("exit %d out=%s err=%s", code, out, errOut)
	}
	// A rejected token does NOT fall back: that is an error the user must see.
	srv, _ := fakeAPI(t, nil)
	t.Setenv("L80_TOKEN", "L80_k1_wrong")
	code, _, errOut = runHist(t, "--api-base", srv.URL)
	if code == 0 || !strings.Contains(errOut, "E_UNAUTHORIZED") {
		t.Errorf("bad token: exit %d, %s", code, errOut)
	}
	// --local never touches the network.
	code, out, _ = runHist(t, "--local", "--api-base", srv.URL)
	if code != 0 || !strings.Contains(out, "local record") {
		t.Errorf("--local: exit %d, %s", code, out)
	}
}

func TestPublishRecordsHistory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("L80_TOKEN", "L80_k1_secret")
	srv, _ := fakeAPI(t, nil)
	dir := t.TempDir()
	payload := filepath.Join(dir, "p.json")
	_ = os.WriteFile(payload, []byte(`{"$template":"bench.betterbench.v1","env":{"model":"Qwen3-8B","notes":{"engine":"vLLM"}},"single_stream":{}}`), 0o644)
	var out, errOut bytes.Buffer
	code := runPublish(env{stdout: &out, stderr: &errOut}, []string{payload, "--api-base", srv.URL})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	got, _ := history.Read()
	if len(got) != 1 || got[0].URL != "https://l80.test/a/1111" || got[0].Title != "Qwen3-8B on vLLM" || got[0].SourcePath != payload {
		t.Fatalf("ledger = %+v", got)
	}
	// Provenance for a later verification request: the file's hash, the sent
	// body's hash, and the tier the server assigned.
	if len(got[0].SourceSHA256) != 64 || len(got[0].BodySHA256) != 64 || got[0].TrustTier != "unverified" {
		t.Errorf("provenance not recorded: %+v", got[0])
	}
	_, hist, _ := runHist(t, "--local")
	if !strings.Contains(hist, "https://l80.test/a/1111") || !strings.Contains(hist, "Qwen3-8B on vLLM") {
		t.Errorf("history = %s", hist)
	}
}
