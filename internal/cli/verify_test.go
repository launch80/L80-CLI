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

// fakeVerificationServer implements the verification contract for three
// keys: k1 publishes, k2 is a verifier, k3 holds a token but is not one.
type fakeVerificationServer struct {
	payload  []byte
	decision map[string]any
}

func (f *fakeVerificationServer) handler(t *testing.T) http.HandlerFunc {
	verifier := map[string]bool{"k2": true}
	owner := map[string]string{"v1": "k1", "v2": "k2"}
	item := func(id, caller string) string {
		return fmt.Sprintf(`{"id":%q,"artifact_guid":"g-%s","url":"https://l80.test/a/g-%s","title":"Qwen3-8B run","key_id":%q,"status":"requested","mine":%t}`,
			id, id, id, owner[id], owner[id] == caller)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		caller := strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer L80_"), "_")[0]
		deny := func(code, msg string) {
			w.WriteHeader(403)
			fmt.Fprintf(w, `{"ok":false,"error":{"code":%q,"message":%q,"remedy":"-"}}`, code, msg)
		}
		switch {
		case r.URL.Path == "/api/v1/keys/me":
			fmt.Fprintf(w, `{"ok":true,"data":{"key_id":%q,"is_verifier":%t}}`, caller, verifier[caller])
		case r.URL.Path == "/api/v1/verifications":
			var rows []string
			for _, id := range []string{"v1", "v2"} {
				mine := owner[id] == caller
				if (r.URL.Query().Get("scope") == "mine") == mine {
					rows = append(rows, item(id, caller))
				}
			}
			fmt.Fprintf(w, `{"ok":true,"data":[%s]}`, strings.Join(rows, ","))
		case strings.HasPrefix(r.URL.Path, "/api/v1/artifacts/") && strings.HasSuffix(r.URL.Path, "/payload"):
			fmt.Fprintf(w, `{"ok":true,"data":%s}`, f.payload)
		case strings.HasSuffix(r.URL.Path, "/decision"):
			id := strings.Split(r.URL.Path, "/")[4]
			if !verifier[caller] {
				deny("E_NOT_VERIFIER", "not a verifier")
				return
			}
			if owner[id] == caller {
				deny("E_OWN_REQUEST", "own request")
				return
			}
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &f.decision)
			fmt.Fprintf(w, `{"ok":true,"data":{"id":%q,"status":"verified","url":"https://l80.test/a/g-%s"}}`, id, id)
		case strings.HasPrefix(r.URL.Path, "/api/v1/verifications/"):
			fmt.Fprintf(w, `{"ok":true,"data":%s}`, item(strings.TrimPrefix(r.URL.Path, "/api/v1/verifications/"), caller))
		default:
			w.WriteHeader(404)
		}
	}
}

// fakeReproducer puts a betterbench on L80_BETTERBENCH that writes results
// to --out.
func fakeReproducer(t *testing.T, results string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n[ \"$1\" = --version ] && { echo 'BetterBench 0.6.1'; exit 0; }\n" +
		"out=''; while [ $# -gt 0 ]; do [ \"$1\" = --out ] && out=$2; shift; done\ncp " + results + " \"$out\"\n"
	os.WriteFile(filepath.Join(dir, "betterbench"), []byte(script), 0o755)
	t.Setenv("L80_BETTERBENCH", filepath.Join(dir, "betterbench"))
}

func TestVerificationWorkflow(t *testing.T) {
	published := verifiableFixture(t)
	payload, _ := os.ReadFile(published)
	f := &fakeVerificationServer{payload: payload}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	wd, _ := os.Getwd()
	os.Chdir(t.TempDir())
	defer os.Chdir(wd)
	// Each key is its own machine, with its own config and reproductions.
	homes := map[string]string{"k1": t.TempDir(), "k2": t.TempDir(), "k3": t.TempDir()}
	as := func(key string) {
		t.Setenv("L80_TOKEN", "L80_"+key+"_secret")
		t.Setenv("XDG_CONFIG_HOME", homes[key])
	}
	base := []string{"--api-base", srv.URL}

	// Any key sees other keys' requests; the publisher sees its own under mine.
	as("k3")
	if code, out, _ := runCmd(t, runVerify, append([]string{"queue"}, base...)...); code != 0 || !strings.Contains(out, "v1") || !strings.Contains(out, "requested by k1") {
		t.Fatalf("k3 queue: %d\n%s", code, out)
	}
	as("k1")
	if _, out, _ := runCmd(t, runVerify, append([]string{"queue"}, base...)...); strings.Contains(out, "v1 ") {
		t.Errorf("a key must not see its own request in the queue:\n%s", out)
	}
	if _, out, _ := runCmd(t, runVerify, append([]string{"mine"}, base...)...); !strings.Contains(out, "v1") {
		t.Errorf("k1 mine:\n%s", out)
	}

	// A non-verifier can reproduce and compare, but not stamp.
	as("k3")
	fakeReproducer(t, published)
	code, out, errOut := runCmd(t, runVerify, append([]string{"reproduce", "v1", "--endpoint", "http://h/v1"}, base...)...)
	if code != 0 || !strings.Contains(out, "matches the published figures") || !strings.Contains(out, "Combined decode") {
		t.Fatalf("k3 reproduce: %d\n%s\n%s", code, out, errOut)
	}
	if code, _, errOut := runCmd(t, runVerify, append([]string{"approve", "v1"}, base...)...); code != 5 || !strings.Contains(errOut, "E_NOT_VERIFIER") {
		t.Errorf("non-verifier approve: %d %s", code, errOut)
	}

	// A verifier may stamp on review alone; no reproduction is sent.
	as("k2")
	if code, _, errOut := runCmd(t, runVerify, append([]string{"approve", "v1"}, base...)...); code != 0 || !strings.Contains(errOut, "review alone") || f.decision["reproduction"] != nil {
		t.Errorf("approve without reproducing: %d %s %v", code, errOut, f.decision)
	}
	// A reproduction that disagrees blocks the stamp unless overridden.
	fakeReproducer(t, slowedCopy(t, published, 0.7))
	if code, out, _ := runCmd(t, runVerify, append([]string{"reproduce", "v1", "--endpoint", "http://h/v1"}, base...)...); code != 4 || !strings.Contains(out, "outside") {
		t.Errorf("different results must fall outside tolerance: %d\n%s", code, out)
	}
	if code, _, errOut := runCmd(t, runVerify, append([]string{"approve", "v1"}, base...)...); code != 4 || !strings.Contains(errOut, "E_OUTSIDE_TOLERANCE") {
		t.Errorf("approve outside tolerance: %d %s", code, errOut)
	}
	fakeReproducer(t, published)
	runCmd(t, runVerify, append([]string{"reproduce", "v1", "--endpoint", "http://h/v1"}, base...)...)
	code, out, errOut = runCmd(t, runVerify, append([]string{"approve", "v1", "--note", "matched on 1x RTX 4090"}, base...)...)
	if code != 0 || !strings.Contains(out, "verified") {
		t.Fatalf("verifier approve: %d\n%s\n%s", code, out, errOut)
	}
	rep, _ := f.decision["reproduction"].(map[string]any)
	if f.decision["decision"] != "verified" || rep == nil || rep["within_tolerance"] != true || rep["results_sha256"] == "" {
		t.Errorf("decision sent = %v", f.decision)
	}

	// Nobody stamps their own request.
	if code, _, errOut := runCmd(t, runVerify, append([]string{"approve", "v2"}, base...)...); code != 5 || !strings.Contains(errOut, "E_OWN_REQUEST") {
		t.Errorf("own request: %d %s", code, errOut)
	}
	if code, _, errOut := runCmd(t, runVerify, append([]string{"reject", "v1"}, base...)...); code != 2 || !strings.Contains(errOut, "needs a reason") {
		t.Errorf("reject without reason: %d %s", code, errOut)
	}
}

// slowedCopy is the run as a slower machine would measure it: every decode
// rate scaled by f and every gap and TTFT stretched by 1/f.
func slowedCopy(t *testing.T, path string, f float64) string {
	t.Helper()
	data, _ := os.ReadFile(path)
	var d map[string]any
	json.Unmarshal(data, &d)
	for _, runs := range d["single_stream"].(map[string]any) {
		for _, r := range runs.([]any) {
			run := r.(map[string]any)
			if v, ok := run["decode_tps"].(float64); ok {
				run["decode_tps"] = v * f
			}
			if v, ok := run["ttft_ms"].(float64); ok {
				run["ttft_ms"] = v / f
			}
			if gaps, ok := run["update_gaps_ms"].([]any); ok {
				for i, g := range gaps {
					gaps[i] = g.(float64) / f
				}
			}
		}
	}
	out := filepath.Join(t.TempDir(), "slow.json")
	b, _ := json.Marshal(d)
	os.WriteFile(out, b, 0o644)
	return out
}
