package qualitybench

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestParseTasksOrdersAndDedupes(t *testing.T) {
	got, err := ParseTasks(" BFCL,gsm8k, bfcl ,,lcb")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "gsm8k,bfcl,lcb" {
		t.Errorf("got %v, want suite order without duplicates", got)
	}
	if _, err := ParseTasks("gsm8k,arc"); err == nil || !strings.Contains(err.Error(), `"arc"`) || !strings.Contains(err.Error(), "mmlu_pro") {
		t.Errorf("unknown task error should name it and list the valid ids: %v", err)
	}
	if _, err := ParseTasks(" , "); err == nil {
		t.Error("an empty list must be an error")
	}
}

func TestScopeAndArgs(t *testing.T) {
	cases := []struct {
		quick bool
		tasks string
		tier  Tier
		want  string // the scope part of argv
	}{
		{false, "", TierFull, ""},
		{true, "", TierQuick, "--quick"},
		{false, "ifeval,gsm8k", TierCustom, "--tasks gsm8k,ifeval"},
		{true, "bfcl", TierQuick, "--quick --tasks bfcl"},
		{false, "spec_parity", TierCustom, "--tasks spec_parity"},
	}
	for _, c := range cases {
		sc, err := ResolveScope(c.quick, c.tasks)
		if err != nil {
			t.Fatal(err)
		}
		if sc.Tier != c.tier {
			t.Errorf("%v %q: tier %s, want %s", c.quick, c.tasks, sc.Tier, c.tier)
		}
		args := strings.Join(RunOptions{Endpoint: "E", Model: "M", Out: "O", Scope: sc}.Args(), " ")
		if got := strings.TrimSpace(strings.TrimPrefix(args, "run --endpoint E --model M --out O")); got != c.want {
			t.Errorf("%v %q: scope args %q, want %q", c.quick, c.tasks, got, c.want)
		}
	}
	// A custom scope naming exactly the default set is a full run: the tool's
	// own suite stays the authority, so no --tasks is sent.
	sc, _ := ResolveScope(false, strings.Join(DefaultIDs(), ","))
	if a := strings.Join(RunOptions{Scope: sc}.Args(), " "); strings.Contains(a, "--tasks") {
		t.Errorf("default set sent --tasks: %s", a)
	}
}

func TestKeyStaysOffTheCommandLine(t *testing.T) {
	o := RunOptions{Endpoint: "E", Model: "M", Out: "O", APIKey: "sk-secret", Thinking: "on", Notes: []string{"engine=vLLM 0.30"}}
	if strings.Contains(strings.Join(o.Args(), " "), "sk-secret") {
		t.Error("API key leaked into argv")
	}
	if !strings.Contains(strings.Join(o.Env(), " "), "QUALITYBENCH_API_KEY=sk-secret") {
		t.Error("API key missing from env")
	}
	if a := strings.Join(o.Args(), " "); !strings.Contains(a, "--thinking on") || !strings.Contains(a, "--note engine=vLLM 0.30") {
		t.Errorf("args = %s", a)
	}
}

func TestEstimateMinutes(t *testing.T) {
	m, un := EstimateMinutes([]string{"gsm8k", "bfcl", "needle"}, false)
	if m != 23 || un != 1 {
		t.Errorf("full: %d min, %d unmeasured", m, un)
	}
	if q, _ := EstimateMinutes([]string{"gsm8k", "bfcl"}, true); q != 3 {
		t.Errorf("quick: %d min, want a tenth rounded up", q)
	}
}

func TestReportPath(t *testing.T) {
	if ReportPath("/x/run.json") != "/x/run.html" || ReportPath("/x/run") != "/x/run.html" {
		t.Error("report goes beside the results file")
	}
}

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/results-bf16.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPrepareStripsIdentityAndSetsTemplate(t *testing.T) {
	raw := fixture(t)
	if StripsIdentity(raw) {
		t.Fatal("fixture should carry env.endpoint and env.host")
	}
	body, err := Prepare(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !StripsIdentity(body) || strings.Contains(string(body), "127.0.0.1") || strings.Contains(string(body), "thelab-box") {
		t.Error("endpoint or host survived")
	}
	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatal(err)
	}
	if top["$template"] != TemplateID {
		t.Errorf("$template = %v", top["$template"])
	}
	if len(body) > MaxPayloadBytes {
		t.Errorf("real BF16 results are %d bytes, over the %d limit", len(body), MaxPayloadBytes)
	}
	// Prepared once is prepared: publishing the payload again is fine.
	if _, err := Prepare(body); err != nil {
		t.Errorf("re-prepare: %v", err)
	}
}

func TestPrepareRejects(t *testing.T) {
	cases := map[string]struct {
		in   string
		want error
	}{
		"syntax":      {`{`, ErrSyntax},
		"betterbench": {`{"$template":"bench.betterbench.v1","schema_version":1,"model":"m"}`, ErrAlreadyPayload},
		"no version":  {`{"model":"m"}`, ErrNotQualityBench},
		"new version": {`{"schema_version":2,"model":"m"}`, ErrNotQualityBench},
		"no model":    {`{"schema_version":1}`, ErrNotQualityBench},
	}
	for name, c := range cases {
		if _, err := Prepare([]byte(c.in)); !errors.Is(err, c.want) {
			t.Errorf("%s: err %v, want %v", name, err, c.want)
		}
	}
}

func TestPrepareRefusesMachineLeaks(t *testing.T) {
	for _, s := range []string{
		"/home/thelab/bake/results/bf16",
		"see /Users/me/run.json",
		"~/models/x",
		`C:\Users\me\run`,
		"http://192.168.1.20:8000/v1",
		"served at http://localhost:8080",
		"https://gpu-box.local/v1",
		"http://10.0.0.5:30000",
	} {
		in, _ := json.Marshal(map[string]any{"schema_version": 1, "model": "m", "caveats": []string{s}})
		_, err := Prepare(in)
		var le *LeakError
		if !errors.As(err, &le) || le.Path != "/caveats/0" {
			t.Errorf("%q: err %v, want a LeakError at /caveats/0", s, err)
		}
	}
	for _, s := range []string{"https://huggingface.co/Jackrong/Qwopus3.8-27B-Coder-Flash", "bf16-think/humaneval-PARTIAL-32of164", "ratio 3/4"} {
		in, _ := json.Marshal(map[string]any{"schema_version": 1, "model": "m", "caveats": []string{s}})
		if _, err := Prepare(in); err != nil {
			t.Errorf("%q flagged: %v", s, err)
		}
	}
}

func TestHeadline(t *testing.T) {
	h := Headline(fixture(t))
	if len(h) == 0 || !strings.HasPrefix(h[0], "GSM8K: 87.0% (1147/1319), thinking on") {
		t.Errorf("headline = %v", h)
	}
}
