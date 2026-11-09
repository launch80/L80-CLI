package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func qualityFixture(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../qualitybench/testdata/results-bf16.json")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeQuality records its argv and environment, then writes the fixture to
// the --out path it was given, as the real tool writes its results file.
func fakeQuality(t *testing.T) (argsFile string) {
	t.Helper()
	argsFile = filepath.Join(t.TempDir(), "args")
	fakeTool(t, "L80_QUALITYBENCH", `echo "$*" > `+argsFile+`
echo "key:$QUALITYBENCH_API_KEY" >> `+argsFile+`
while [ $# -gt 0 ]; do [ "$1" = --out ] && out=$2; shift; done
cp `+qualityFixture(t)+` "$out"
echo "<html></html>" > "${out%.json}.html"
`)
	return argsFile
}

func TestQualitybenchRunDryRun(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	argsFile := fakeQuality(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "r.json")
	payload := filepath.Join(dir, "p.json")
	code, stdout, stderr := runCmd(t, runQualitybench, "--endpoint", "http://127.0.0.1:9/v1", "--model", "M",
		"--tasks", "ifeval,gsm8k", "--quick", "--thinking", "on", "--api-key", "sk-x", "--out", out,
		"--dry-run", "--payload-out", payload)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	got, _ := os.ReadFile(argsFile)
	args := string(got)
	for _, want := range []string{"run --endpoint http://127.0.0.1:9/v1 --model M --out " + out, "--quick", "--tasks gsm8k,ifeval", "--thinking on", "key:sk-x"} {
		if !strings.Contains(args, want) {
			t.Errorf("tool saw %q, missing %q", args, want)
		}
	}
	if strings.Contains(strings.SplitN(args, "\n", 2)[0], "sk-x") {
		t.Error("key on the command line")
	}
	if !strings.Contains(stdout, "dry run: would publish") || !strings.Contains(stderr, "report written to "+filepath.Join(dir, "r.html")) {
		t.Errorf("stdout %q\nstderr %q", stdout, stderr)
	}
	if !strings.Contains(stderr, "GSM8K: 87.0%") {
		t.Errorf("no headline in %q", stderr)
	}
	body, _ := os.ReadFile(payload)
	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatal(err)
	}
	env := top["env"].(map[string]any)
	if _, ok := env["endpoint"]; ok || env["host"] != nil || top["$template"] != "bench.qualitybench.v1" {
		t.Errorf("payload env %v, template %v", env, top["$template"])
	}
}

func TestQualitybenchNoPublish(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	fakeQuality(t)
	out := filepath.Join(t.TempDir(), "r.json")
	code, _, stderr := runCmd(t, runQualitybench, "--endpoint", "http://127.0.0.1:9/v1", "--model", "M", "--out", out, "--no-publish")
	if code != 0 || !strings.Contains(stderr, "publish later with `L80 qualitybench --results "+out+"`") {
		t.Errorf("exit %d\n%s", code, stderr)
	}
}

func TestQualitybenchResultsDryRun(t *testing.T) {
	code, stdout, stderr := runCmd(t, runQualitybench, "--results", qualityFixture(t), "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	var r map[string]any
	if err := json.Unmarshal([]byte(stdout), &r); err != nil || r["template"] != "bench.qualitybench.v1" || r["byte_size"].(float64) <= 0 {
		t.Errorf("%v %v", r, err)
	}
}

func TestQualitybenchUsageErrors(t *testing.T) {
	cases := map[string][]string{
		"no model":      {},
		"bad task":      {"--model", "M", "--endpoint", "http://x/v1", "--tasks", "arc"},
		"bad thinking":  {"--model", "M", "--thinking", "maybe"},
		"bad note":      {"--model", "M", "--note", "engine"},
		"results+model": {"--results", "r.json", "--model", "M"},
		"key after --":  {"--model", "M", "--endpoint", "http://x/v1", "--", "--api-key=k"},
	}
	for name, args := range cases {
		if code, _, _ := runCmd(t, runQualitybench, args...); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
}

func TestQualitybenchFindsEndpointForModel(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	argsFile := fakeQuality(t)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"l80-test/unique-model-7f3a","owned_by":"vllm"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer s.Close()
	out := filepath.Join(t.TempDir(), "r.json")
	code, _, stderr := runCmd(t, runQualitybench, "--model", "l80-test/unique-model-7f3a", "--scan", s.URL, "--out", out, "--no-publish")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	got, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(got), "--endpoint "+s.URL+"/v1 ") || !strings.Contains(stderr, "using "+s.URL+"/v1 (vLLM)") {
		t.Errorf("args %q\nstderr %q", got, stderr)
	}

	code, _, stderr = runCmd(t, runQualitybench, "--model", "l80-test/absent-model", "--scan", s.URL, "--out", out)
	if code != 2 || !strings.Contains(stderr, `no local server lists model "l80-test/absent-model"`) {
		t.Errorf("exit %d\n%s", code, stderr)
	}
}

func TestQualitybenchTasksJSON(t *testing.T) {
	code, stdout, _ := runCmd(t, runQualitybench, "tasks", "--json")
	var r struct {
		Tasks []struct {
			ID      string `json:"id"`
			Default bool   `json:"default"`
		} `json:"tasks"`
	}
	if code != 0 || json.Unmarshal([]byte(stdout), &r) != nil || len(r.Tasks) < 9 || r.Tasks[0].ID != "gsm8k" {
		t.Errorf("exit %d: %s", code, stdout)
	}
}

func TestPublishPreparesQualityResults(t *testing.T) {
	code, stdout, stderr := runCmd(t, runPublish, qualityFixture(t), "--template", "bench.qualitybench.v1", "--dry-run")
	if code != 0 || !strings.Contains(stdout, "would publish") {
		t.Errorf("exit %d\n%s\n%s", code, stdout, stderr)
	}
}
