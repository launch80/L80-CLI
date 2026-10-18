package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/launch80/L80-CLI/internal/history"
)

// verifiableFixture is the 0.6.1 fixture with its cache hit removed and a
// hardware note added, so every blocking check passes.
func verifiableFixture(t *testing.T) string {
	t.Helper()
	src, _ := filepath.Abs("../betterbench/testdata/results_schema2_v061.json")
	data, _ := os.ReadFile(src)
	var d map[string]any
	json.Unmarshal(data, &d)
	for _, p := range d["prefill"].([]any) {
		row := p.(map[string]any)
		if ct, ok := row["cached_tokens"].([]any); ok {
			for i := range ct {
				ct[i] = 0
			}
		}
	}
	d["env"].(map[string]any)["notes"].(map[string]any)["hardware"] = "1x RTX 4090"
	out := filepath.Join(t.TempDir(), "results.json")
	b, _ := json.Marshal(d)
	os.WriteFile(out, b, 0o644)
	return out
}

func TestVerifyCheckFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	code, out, _ := runCmd(t, runVerify, "check", fixturePath(t))
	if code != 4 || !strings.Contains(out, "FAIL  version") || !strings.Contains(out, "not verifiable yet") {
		t.Errorf("0.4.0 file: exit %d\n%s", code, out)
	}
	code, out, _ = runCmd(t, runVerify, "check", verifiableFixture(t), "--json")
	if code != 0 || !strings.Contains(out, `"verifiable": true`) {
		t.Errorf("clean 0.6.1 file: exit %d\n%s", code, out)
	}
}

func TestVerifyRequestFlow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("L80_TOKEN", "L80_k1_secret")
	file := verifiableFixture(t)
	data, _ := os.ReadFile(file)
	sum := fmt.Sprintf("%x", sha256Sum(data))
	guid := "11111111-2222-3333-4444-555555555555"
	history.Append(history.Entry{GUID: guid, URL: "https://l80.test/a/" + guid, TemplateID: "bench.betterbench.v1",
		SourcePath: file, SourceSHA256: sum, BodySHA256: "b", ContentHash: "c", TrustTier: "unverified"})

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/artifacts/"+guid+"/verification" || r.Header.Get("Authorization") != "Bearer L80_k1_secret" {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"ok":false,"error":{"code":"E_NOT_FOUND","message":"no such page","remedy":"check the id"}}`)
			return
		}
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &got)
		}
		fmt.Fprint(w, `{"ok":true,"data":{"id":"v1","artifact_guid":"`+guid+`","status":"requested","requested_at":"2026-10-17T12:00:00Z"}}`)
	}))
	defer srv.Close()

	code, out, errOut := runCmd(t, runVerify, "request", "https://l80.test/a/"+guid, "--note", "vLLM 0.9.3, default flags", "--api-base", srv.URL)
	if code != 0 || !strings.Contains(out, "verification requested") || !strings.Contains(out, "status requested") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	if got["note"] != "vLLM 0.9.3, default flags" || got["source_sha256"] != sum || got["l80_version"] == "" {
		t.Errorf("request body = %v", got)
	}
	checks, _ := got["client_checks"].([]any)
	var prov map[string]any
	for _, c := range checks {
		if m := c.(map[string]any); m["name"] == "provenance" {
			prov = m
		}
	}
	if prov == nil || prov["status"] != "pass" {
		t.Errorf("provenance check = %v", prov)
	}

	// Editing the file after publishing breaks provenance and blocks the request.
	os.WriteFile(file, append(data, ' '), 0o644)
	code, out, errOut = runCmd(t, runVerify, "request", guid, "--api-base", srv.URL)
	if code != 4 || !strings.Contains(out, "has changed since it was published") {
		t.Errorf("tampered file: exit %d\n%s\n%s", code, out, errOut)
	}

	code, out, _ = runCmd(t, runVerify, "status", guid, "--api-base", srv.URL, "--json")
	if code != 0 || !strings.Contains(out, `"status": "requested"`) {
		t.Errorf("status: %d %s", code, out)
	}
}

func TestVerifyAgainstServerWithoutSupport(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("L80_TOKEN", "L80_k1_secret")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html>Redirecting...</html>") }))
	defer srv.Close()
	code, _, errOut := runCmd(t, runVerify, "status", "11111111-2222-3333-4444-555555555555", "--api-base", srv.URL)
	if code != 8 || !strings.Contains(errOut, "does not accept verification requests yet") {
		t.Errorf("exit %d: %s", code, errOut)
	}
	code, _, errOut = runCmd(t, runReview, "reject", "v1", "--api-base", srv.URL)
	if code != 2 || !strings.Contains(errOut, "needs a reason") {
		t.Errorf("reject without reason: %d %s", code, errOut)
	}
}

func sha256Sum(b []byte) [32]byte { return sha256.Sum256(b) }
