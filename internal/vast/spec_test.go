package vast

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseGPUNames(t *testing.T) {
	got := ParseGPUNames(" RTX_4090, H100  SXM ,,A100_SXM4 ")
	if want := []string{"RTX 4090", "H100 SXM", "A100 SXM4"}; !slices.Equal(got, want) {
		t.Errorf("got %q", got)
	}
}

func TestSpecValidate(t *testing.T) {
	ok := DefaultSpec()
	ok.ModelRepo = "Qwen/Qwen3-8B"
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*Spec){
		"repo":   func(s *Spec) { s.ModelRepo = "qwen3" },
		"engine": func(s *Spec) { s.Engine = "tgi" },
		"count":  func(s *Spec) { s.GPUCount = 0 },
		"dph":    func(s *Spec) { s.MaxDPH = 0 },
		"budget": func(s *Spec) { s.BudgetUSD = -1 },
		"name":   func(s *Spec) { s.Name = "My Spec" },
	} {
		s := ok
		mut(&s)
		if s.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSpecYAMLAndWrite(t *testing.T) {
	s := DefaultSpec()
	s.ModelRepo, s.GPUNames, s.GPUCount, s.MaxDPH = "Qwen/Qwen3-32B", []string{"H100 SXM"}, 2, 4.25
	y := s.YAML()
	for _, want := range []string{`name: "qwen3-32b"`, `names: ["H100 SXM"]`, "count: 2", "max_dph: 4.25",
		`hf_repo: "Qwen/Qwen3-32B"`, "engine: vllm", `"--tensor-parallel-size", "2"`, "max_total_usd: 5"} {
		if !strings.Contains(y, want) {
			t.Errorf("YAML missing %q:\n%s", want, y)
		}
	}
	path := filepath.Join(t.TempDir(), "specs", s.FileName())
	if _, err := WriteSpec(s, path, false); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSpec(s, path, false); !errors.Is(err, ErrSpecExists) {
		t.Errorf("second write: %v", err)
	}
	if _, err := WriteSpec(s, path, true); err != nil {
		t.Errorf("force: %v", err)
	}
	if got := ListSpecs(filepath.Dir(path)); len(got) != 1 || got[0] != path {
		t.Errorf("ListSpecs = %q", got)
	}
}
