package betterbench

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRunnerUsesPinnedManifest(t *testing.T) {
	t.Setenv("L80_BETTERBENCH", "/x/.venv/bin/python -m betterbench")
	r, err := ResolveRunner()
	if err != nil || strings.Join(r.Argv, " ") != "/x/.venv/bin/python -m betterbench" || r.Source != "L80_BETTERBENCH" {
		t.Fatalf("env override: %+v %v", r, err)
	}
	if tool := Tool(); tool.MinVersion != "0.6.1" || !tool.Pinned() {
		t.Fatalf("betterbench must be pinned to 0.6.1: %+v", tool)
	}
	t.Setenv("L80_BETTERBENCH", "")
	t.Setenv("PATH", t.TempDir())
	if _, err := ResolveRunner(); !errors.Is(err, ErrNoRunner) {
		t.Fatalf("want ErrNoRunner, got %v", err)
	}
}

func TestRunOptionsEnvCarriesKeyNotArgv(t *testing.T) {
	o := RunOptions{Endpoint: "http://h/v1", Model: "m", Out: "/tmp/r.json", Warmup: -1, APIKey: "sk-secret"}
	if strings.Contains(strings.Join(o.Args(), " "), "sk-secret") {
		t.Fatal("API key must not be on the command line")
	}
	env := strings.Join(o.Env(), " ")
	if !strings.Contains(env, "BETTERBENCH_API_KEY=sk-secret") || !strings.Contains(env, "BETTERBENCH_NO_UPDATE_CHECK=1") {
		t.Fatalf("env = %s", env)
	}
}

func TestRunOptionsArgs(t *testing.T) {
	o := RunOptions{Endpoint: "http://h:8080/v1", Model: "m", Out: "/tmp/r.json", Quick: true, Passes: 7, Warmup: 0,
		Notes: []string{"engine=vLLM", "tp=2"}, Phases: []string{"decode", "prefill"}, NoConcurrency: true, Extra: []string{"--seed", "3"}}
	got := strings.Join(o.Args(), " ")
	want := "run --endpoint http://h:8080/v1 --model m --out /tmp/r.json --quick --passes 7 --warmup 0 --note engine=vLLM --note tp=2 --decode --prefill --no-concurrency --seed 3"
	if got != want {
		t.Fatalf("args =\n %s\nwant\n %s", got, want)
	}
	if strings.Contains(strings.Join(RunOptions{Warmup: -1}.Args(), " "), "--warmup") {
		t.Fatal("absent warmup must not be passed")
	}
}

// A fake betterbench: a shell script that records its argv and writes the
// fixture to --out. Exercises Execute end to end without Python.
func fakeBetterbench(t *testing.T, exit int) (Runner, string) {
	t.Helper()
	dir := t.TempDir()
	fixture, err := filepath.Abs("testdata/results_schema2.json")
	if err != nil {
		t.Fatal(err)
	}
	argvLog := filepath.Join(dir, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argvLog + "\n" +
		"out=''; while [ $# -gt 0 ]; do if [ \"$1\" = --out ]; then out=$2; fi; shift; done\n" +
		"echo 'BetterBench 0.4.0 · fake' \n" +
		"[ " + itoa(exit) + " -eq 0 ] && cp " + fixture + " \"$out\"\nexit " + itoa(exit) + "\n"
	path := filepath.Join(dir, "betterbench")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return Runner{Argv: []string{path}, Source: "test"}, argvLog
}

func TestExecuteRunsAndReadsResults(t *testing.T) {
	r, argvLog := fakeBetterbench(t, 0)
	out := filepath.Join(t.TempDir(), "r.json")
	var log strings.Builder
	data, err := Execute(context.Background(), r, RunOptions{Endpoint: "http://h/v1", Model: "m", Out: out, Warmup: -1}, &log, &log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "fake") {
		t.Error("harness stdout was not streamed")
	}
	argv, _ := os.ReadFile(argvLog)
	if !strings.Contains(string(argv), "--endpoint\nhttp://h/v1\n") || !strings.Contains(string(argv), "--out\n"+out+"\n") {
		t.Errorf("argv = %s", argv)
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(data, &top) != nil || top["single_stream"] == nil {
		t.Fatal("results not read back")
	}
}

func TestExecuteReportsHarnessFailure(t *testing.T) {
	r, _ := fakeBetterbench(t, 3)
	out := filepath.Join(t.TempDir(), "r.json")
	_, err := Execute(context.Background(), r, RunOptions{Out: out, Warmup: -1}, os.Stderr, os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "status 3") {
		t.Fatalf("want exit status in error, got %v", err)
	}
}

func TestPrepareRawStripsIdentityAndKeepsEverythingElseByteForByte(t *testing.T) {
	data, err := os.ReadFile("testdata/results_schema2.json")
	if err != nil {
		t.Fatal(err)
	}
	body, err := PrepareRaw(data, Prose{})
	if err != nil {
		t.Fatal(err)
	}
	if !StripsIdentity(body) {
		t.Fatal("endpoint/host survived")
	}
	for _, leak := range []string{"192.168", "rig01", `"endpoint"`, `"host"`} {
		if strings.Contains(string(body), leak) {
			t.Errorf("body leaks %q", leak)
		}
	}
	var in, out map[string]json.RawMessage
	_ = json.Unmarshal(data, &in)
	_ = json.Unmarshal(body, &out)
	if string(out["$template"]) != `"bench.betterbench.v1"` {
		t.Errorf("$template = %s", out["$template"])
	}
	// Every other top-level value is passed through as the same JSON value.
	for k, v := range in {
		if k == "env" {
			continue
		}
		var a, b any
		_ = json.Unmarshal(v, &a)
		_ = json.Unmarshal(out[k], &b)
		aj, _ := json.Marshal(a)
		bj, _ := json.Marshal(b)
		if string(aj) != string(bj) {
			t.Errorf("%s was altered", k)
		}
	}
	// The raw gap arrays must be present and unrounded: this is the whole point.
	if !strings.Contains(string(body), `"update_gaps_ms":[`) {
		t.Error("gap series missing")
	}
	if _, ok := out["l80"]; ok {
		t.Error("l80 must be absent when no prose was given")
	}
}

func TestPrepareRawAttachesProseAndValidatesIt(t *testing.T) {
	data, _ := os.ReadFile("testdata/results_schema2.json")
	body, err := PrepareRaw(data, Prose{Title: "My run", Summary: "Short.", Sections: []Section{{Heading: "h", Body: "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"l80":{"title":"My run","summary":"Short.","sections":[{"heading":"h","body":"b"}]}`) {
		t.Errorf("l80 block wrong: %s", body[len(body)-200:])
	}
	if _, err := PrepareRaw(data, Prose{Title: strings.Repeat("x", 121)}); err == nil {
		t.Error("over-long title must be refused")
	}
	if _, err := PrepareRaw([]byte(`{"$template":"bench.report.v1","title":"x"}`), Prose{}); !errors.Is(err, ErrAlreadyPayload) {
		t.Errorf("mapped payload should be ErrAlreadyPayload, got %v", err)
	}
	if _, err := PrepareRaw([]byte(`{"single_stream":{}}`), Prose{}); !errors.Is(err, ErrNotBetterBench) {
		t.Errorf("no env should be ErrNotBetterBench, got %v", err)
	}
	// Idempotent: a file that already declares the raw template is accepted again.
	if _, err := PrepareRaw(body, Prose{}); err != nil {
		t.Errorf("re-preparing a prepared body should work: %v", err)
	}
}
