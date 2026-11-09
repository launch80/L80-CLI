package pwdet

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func load(t *testing.T) *Report {
	t.Helper()
	data, err := os.ReadFile("testdata/report_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestBuildFromRealReport(t *testing.T) {
	r := load(t)
	p, err := Build(r, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Template != TemplateID || !strings.HasPrefix(p.Title, "pwdet: Wrapzii/Swift-1.5") || !strings.HasSuffix(p.Title, "— clean") {
		t.Errorf("title = %q", p.Title)
	}
	body, _ := json.Marshal(p)
	for _, leak := range []string{"rig01-test", "/home/tester", ".cache/huggingface", "Linux-7.0"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("payload leaks %q", leak)
		}
	}
	// Limits of generic.report.v1.
	if utf8.RuneCountInString(p.Title) > maxTitle || utf8.RuneCountInString(p.Summary) > maxSummary || len(p.Metrics) > maxMetrics || len(p.Sections) > maxSections {
		t.Errorf("over a limit: %+v", p)
	}
	for _, m := range p.Metrics {
		if m.Label == "" || m.Value == "" || utf8.RuneCountInString(m.Value) > maxValue {
			t.Errorf("bad metric %+v", m)
		}
	}
	for _, s := range p.Sections {
		if len(s.Bullets) > maxBullets {
			t.Errorf("%s has %d bullets", s.Heading, len(s.Bullets))
		}
		for _, b := range s.Bullets {
			if utf8.RuneCountInString(b) > maxBullet {
				t.Errorf("bullet too long: %q", b)
			}
		}
	}
	if len(body) > 65536 {
		t.Errorf("payload %d bytes is over the 64 KB limit", len(body))
	}
	// Worst first: the medium findings lead, and disclosure is shown.
	f := p.Sections[0]
	if f.Heading != "Findings to review" || !strings.HasPrefix(f.Bullets[0], "[") || !strings.Contains(strings.Join(f.Bullets, "\n"), "(disclosed by the publisher)") {
		t.Errorf("findings section = %+v", f)
	}
}

func TestScrubber(t *testing.T) {
	sc := Scrubber{secrets: []string{"myrig"}}
	in := "read /home/alice/.cache/hub/models--x/snapshots/abc/model.safetensors via http://192.168.1.20:8000/v1 on myrig, also C:\\Users\\bob\\m.gguf"
	got := sc.Clean(in)
	if leaks := sc.Leaks(got); len(leaks) > 0 {
		t.Fatalf("leaks %v in %q", leaks, got)
	}
	if !strings.Contains(got, "…/model.safetensors") || !strings.Contains(got, "<local endpoint>") || !strings.Contains(got, "<host>") {
		t.Errorf("got %q", got)
	}
	if sc.Clean("Qwen/Qwen3-0.6B at https://huggingface.co/Qwen") != "Qwen/Qwen3-0.6B at https://huggingface.co/Qwen" {
		t.Error("public names and URLs must survive")
	}
}

func TestUserTextLimitsAreErrors(t *testing.T) {
	r := load(t)
	if _, err := Build(r, Options{Title: strings.Repeat("x", 121)}); err == nil {
		t.Error("long title must be refused")
	}
	p, err := Build(r, Options{Title: "My scan", Summary: "Checked before deploying."})
	if err != nil || p.Title != "My scan" || p.Summary != "Checked before deploying." {
		t.Errorf("%+v %v", p, err)
	}
	if _, err := Parse([]byte(`{"schema_version":"3","tool_version":"9"}`)); err == nil {
		t.Error("wrong schema must be refused")
	}
}

func TestEndpointSubjectNeverShowsURL(t *testing.T) {
	r := load(t)
	r.Subject = ModelRef{Key: "local", Kind: "endpoint", Endpoint: "http://10.0.0.5:8000/v1", ServedModel: "my-model"}
	p, err := Build(r, Options{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(p)
	if strings.Contains(string(body), "10.0.0.5") || !strings.Contains(p.Title, "my-model") {
		t.Errorf("title %q", p.Title)
	}
}

func loadV2(t *testing.T) (*Report, []byte) {
	t.Helper()
	data, err := os.ReadFile("testdata/report_v2.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return r, data
}

func TestV2ReportParsesAndShowsAssurance(t *testing.T) {
	r, _ := loadV2(t)
	if r.Level() != "standard" || r.Inputs == nil || r.Digest == "" || !r.Inputs.Subject.Fetchable() {
		t.Fatalf("v2 fields: level %q inputs %v digest %q", r.Level(), r.Inputs != nil, r.Digest)
	}
	p, err := Build(r, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p.Title, "— clean · standard") {
		t.Errorf("title = %q", p.Title)
	}
	heads := map[string]Section{}
	for _, s := range p.Sections {
		heads[s.Heading] = s
	}
	for _, h := range []string{"Threat coverage", "Coverage", "Measured accuracy", "Method"} {
		if _, ok := heads[h]; !ok {
			t.Errorf("no %s section in %v", h, p.Sections)
		}
	}
	tc := strings.Join(heads["Threat coverage"].Bullets, "\n")
	if !strings.Contains(tc, "Trained-in covert behaviour: not tested") {
		t.Errorf("threat coverage = %s", tc)
	}
	if !strings.Contains(heads["Method"].Body, "Standard scan") || !strings.Contains(heads["Coverage"].Bullets[0], "checks ran") {
		t.Errorf("method/coverage = %q / %q", heads["Method"].Body, heads["Coverage"].Bullets)
	}
}

func TestScanPayloadCarriesAScrubbedReport(t *testing.T) {
	r, raw := loadV2(t)
	sp, err := BuildScan(r, raw, Options{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(sp)
	if sp.Template != ScanTemplateID || len(body) > ScanMaxPayloadBytes {
		t.Fatalf("template %q, %d bytes", sp.Template, len(body))
	}
	for _, leak := range []string{"rig01-test", "/home/tester", ".cache/huggingface", "Linux-7.0", "manifest_path"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("payload leaks %q", leak)
		}
	}
	if !IsScanPayload(body) {
		t.Error("IsScanPayload")
	}
	_, back, err := ParseScan(body)
	if err != nil {
		t.Fatal(err)
	}
	if back.Digest != r.Digest || back.Inputs.Subject.Repo != r.Inputs.Subject.Repo || back.Subject.Path != "" {
		t.Errorf("round trip lost inputs: %+v", back.Subject)
	}
	if checks := Prechecks(back); Blocking(checks) {
		t.Errorf("a Standard scan of a Hub repo must be verifiable: %+v", checks)
	}
}

func TestPrechecks(t *testing.T) {
	v1 := load(t)
	if c := Prechecks(v1); !Blocking(c) || c[0].Name != "report" {
		t.Errorf("a v1 report cannot be re-run: %+v", c)
	}
	r, _ := loadV2(t)
	r.Assurance.Level = "basic"
	r.Inputs.Subject.Kind = "local"
	failed := map[string]bool{}
	for _, c := range Prechecks(r) {
		if c.Status == "fail" {
			failed[c.Name] = true
		}
	}
	if !failed["level"] || !failed["subject"] {
		t.Errorf("basic, local scan: failed %v", failed)
	}
}
