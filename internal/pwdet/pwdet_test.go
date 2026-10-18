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
	if _, err := Parse([]byte(`{"schema_version":"2"}`)); err == nil {
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
