package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixturePath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../betterbench/testdata/results_schema2.json")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Puts a fake betterbench on PATH that copies the fixture to --out.
func fakeOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n[ \"$1\" = --version ] && { echo 'BetterBench 0.6.1'; exit 0; }\nout=''; while [ $# -gt 0 ]; do if [ \"$1\" = --out ]; then out=$2; fi; shift; done\n" +
		"echo '[single] chat: warmup 1 + 5 runs'\ncp " + fixturePath(t) + " \"$out\"\n"
	if err := os.WriteFile(filepath.Join(dir, "betterbench"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("L80_BETTERBENCH", "")
}

func runBB(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runBetterbench(env{stdout: &out, stderr: &errOut}, args)
	return code, out.String(), errOut.String()
}

func TestRunsBenchmarkThenPreparesRawPayload(t *testing.T) {
	fakeOnPath(t)
	dir := t.TempDir()
	results := filepath.Join(dir, "r.json")
	payload := filepath.Join(dir, "p.json")
	code, out, errOut := runBB(t, "--endpoint", "http://h:8080/v1", "--model", "m", "--quick", "--note", "engine=vLLM 0.9",
		"--out", results, "--payload-out", payload, "--dry-run", "--title", "T", "--", "--seed", "1")
	if code != 0 {
		t.Fatalf("exit %d\nstdout %s\nstderr %s", code, out, errOut)
	}
	if !strings.Contains(errOut, "running BetterBench via PATH") || !strings.Contains(errOut, "--quick --note 'engine=vLLM 0.9' --seed 1") {
		t.Errorf("stderr should show the resolved command: %s", errOut)
	}
	if !strings.Contains(errOut, "[single] chat") {
		t.Error("harness output was not streamed to the terminal")
	}
	if !strings.Contains(out, "would publish "+results+" as bench.betterbench.v1") {
		t.Errorf("stdout = %s", out)
	}
	b, err := os.ReadFile(payload)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	_ = json.Unmarshal(b, &top)
	if string(top["$template"]) != `"bench.betterbench.v1"` || !strings.Contains(string(top["l80"]), `"title":"T"`) {
		t.Errorf("payload: template=%s l80=%s", top["$template"], top["l80"])
	}
	if strings.Contains(string(b), `"endpoint"`) || strings.Contains(string(b), "rig01") {
		t.Error("payload leaks endpoint/host")
	}
	if _, err := os.Stat(results); err != nil {
		t.Error("results.json must be kept for the user")
	}
}

func TestResultsFlagPublishesExistingFile(t *testing.T) {
	code, out, errOut := runBB(t, "--results", fixturePath(t), "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	if !strings.Contains(out, `"template": "bench.betterbench.v1"`) || !strings.Contains(out, `"dry_run": true`) {
		t.Errorf("json = %s", out)
	}
	// The 0.2.x bare-path form still works.
	if code, _, errOut := runBB(t, fixturePath(t), "--dry-run"); code != 0 {
		t.Errorf("bare path form failed: %s", errOut)
	}
}

func TestLegacyTemplateStillMaps(t *testing.T) {
	dir := t.TempDir()
	payload := filepath.Join(dir, "p.json")
	code, _, errOut := runBB(t, "--results", fixturePath(t), "--template", "bench.report.v1", "--engine", "vLLM", "--payload-out", payload, "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	b, _ := os.ReadFile(payload)
	if !strings.Contains(string(b), `"$template":"bench.report.v1"`) || !strings.Contains(string(b), `"engine":"vLLM"`) {
		t.Errorf("mapped payload: %.200s", b)
	}
}

func TestUsageErrors(t *testing.T) {
	if code, _, errOut := runBB(t, "--dry-run"); code != 2 || !strings.Contains(errOut, "--endpoint and --model are required") {
		t.Errorf("no input: exit %d, %s", code, errOut)
	}
	if code, _, errOut := runBB(t, "--results", "x.json", "--endpoint", "http://h", "--model", "m"); code != 2 || !strings.Contains(errOut, "not both") {
		t.Errorf("both modes: exit %d, %s", code, errOut)
	}
	if code, _, errOut := runBB(t, "--results", fixturePath(t), "--template", "nope", "--dry-run"); code == 0 || !strings.Contains(errOut, "E_TEMPLATE_UNKNOWN") {
		t.Errorf("bad template: exit %d, %s", code, errOut)
	}
}

func TestNoRunnerIsExplained(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("L80_BETTERBENCH", "")
	code, _, errOut := runBB(t, "--endpoint", "http://h", "--model", "m", "--dry-run")
	if code == 0 || !strings.Contains(errOut, "uvx") || !strings.Contains(errOut, "L80_BETTERBENCH") {
		t.Errorf("exit %d: %s", code, errOut)
	}
}

func TestDefaultResultsPath(t *testing.T) {
	got := defaultResultsPath("mlx-community/Qwen3.8-27B-oQ6", time.Date(2026, 9, 5, 22, 8, 0, 0, time.UTC))
	if got != "betterbench-mlx-community_Qwen3.8-27B-oQ6-20260905-2208.json" {
		t.Errorf("got %s", got)
	}
}

// `L80 publish results.json --template bench.betterbench.v1` must work too,
// and must strip the same fields.
func TestPublishRawTemplateStripsAndAllowsLargeFiles(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runPublish(env{stdout: &out, stderr: &errOut}, []string{fixturePath(t), "--template", "bench.betterbench.v1", "--dry-run"})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "would publish") {
		t.Errorf("stdout = %s", out.String())
	}
	// And the mapped-template path still refuses raw output.
	out.Reset()
	errOut.Reset()
	code = runPublish(env{stdout: &out, stderr: &errOut}, []string{fixturePath(t), "--template", "bench.report.v1", "--dry-run"})
	if code == 0 || !strings.Contains(errOut.String(), "E_INPUT_NOT_TEMPLATE") {
		t.Errorf("bench.report.v1 with raw output: exit %d, %s", code, errOut.String())
	}
}

// The endpoint key reaches BetterBench by environment and never appears in
// the echoed command; a key smuggled after -- is refused.
func TestAPIKeyIsPassedByEnvironmentAndNeverEchoed(t *testing.T) {
	dir := t.TempDir()
	envLog := filepath.Join(dir, "env")
	script := "#!/bin/sh\n[ \"$1\" = --version ] && { echo 'BetterBench 0.6.1'; exit 0; }\n" +
		"echo \"$BETTERBENCH_API_KEY|$BETTERBENCH_NO_UPDATE_CHECK\" > " + envLog + "\n" +
		"out=''; while [ $# -gt 0 ]; do if [ \"$1\" = --out ]; then out=$2; fi; shift; done\ncp " + fixturePath(t) + " \"$out\"\n"
	if err := os.WriteFile(filepath.Join(dir, "betterbench"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("L80_BETTERBENCH", "")

	code, _, errOut := runBB(t, "--endpoint", "http://h/v1", "--model", "m", "--api-key", "sk-very-secret",
		"--out", filepath.Join(dir, "r.json"), "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if strings.Contains(errOut, "sk-very-secret") {
		t.Error("key leaked to stderr")
	}
	got, _ := os.ReadFile(envLog)
	if strings.TrimSpace(string(got)) != "sk-very-secret|1" {
		t.Errorf("child env = %q", got)
	}

	code, _, errOut = runBB(t, "--endpoint", "http://h/v1", "--model", "m", "--out", filepath.Join(dir, "r2.json"), "--dry-run", "--", "--api-key", "x")
	if code != 2 || !strings.Contains(errOut, "L80_BETTERBENCH_API_KEY") {
		t.Errorf("key after -- must be refused: exit %d %s", code, errOut)
	}
	code, _, errOut = runBB(t, "--endpoint", "http://h/v1", "--model", "m", "--out", dir, "--dry-run")
	if code != 2 || !strings.Contains(errOut, "is a directory") {
		t.Errorf("--out dir must be refused: exit %d %s", code, errOut)
	}
}

func TestOldResultsFileIsWarnedAbout(t *testing.T) {
	_, _, errOut := runBB(t, "--results", fixturePath(t), "--dry-run")
	if !strings.Contains(errOut, "written by BetterBench 0.4.0") {
		t.Errorf("want a version warning: %s", errOut)
	}
	v061, _ := filepath.Abs("../betterbench/testdata/results_schema2_v061.json")
	_, _, errOut = runBB(t, "--results", v061, "--dry-run")
	if strings.Contains(errOut, "written by BetterBench") || !strings.Contains(errOut, "prefix cache") {
		t.Errorf("0.6.1 file: want only the cache warning: %s", errOut)
	}
}
