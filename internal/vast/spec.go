package vast

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Spec is the part of a bbvastl80 run spec a person usually decides: which
// model, which engine, which GPUs and how much they may cost. Everything
// else takes bbvastl80's defaults, and the YAML it writes says so, so the
// file can be edited by hand afterwards.
type Spec struct {
	Name        string
	ModelRepo   string // Hugging Face repo with the weights
	ModelID     string // id served at /v1; defaults to ModelRepo
	Engine      string // vllm | llamacpp
	GPUNames    []string
	GPUCount    int
	MinGPURAMGB float64
	MaxDPH      float64 // $/hr ceiling
	BudgetUSD   float64 // whole-run budget check
	MaxModelLen int
	DiskGB      int
	Quick       bool // a short benchmark
}

// ParseGPUNames splits a comma-separated list of Vast.ai GPU names. Vast
// names have spaces ("RTX 4090", "H100 SXM") and are matched exactly, so
// the underscore form people often type (RTX_4090) is converted.
func ParseGPUNames(list string) []string {
	var out []string
	for _, g := range strings.Split(list, ",") {
		if g = strings.Join(strings.Fields(strings.ReplaceAll(g, "_", " ")), " "); g != "" {
			out = append(out, g)
		}
	}
	return out
}

// Engines are the serving presets bbvastl80 can provision.
var Engines = []string{"vllm", "llamacpp"}

// DefaultSpec is what the builder starts from.
func DefaultSpec() Spec {
	return Spec{Engine: "vllm", GPUCount: 1, MinGPURAMGB: 24, MaxDPH: 1.5, BudgetUSD: 5, MaxModelLen: 8192, DiskGB: 100}
}

var repoRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Validate reports the first thing bbvastl80 would reject, in plain words.
func (s Spec) Validate() error {
	switch {
	case !repoRe.MatchString(s.ModelRepo):
		return fmt.Errorf("model must be a Hugging Face repo like Qwen/Qwen3-8B, not %q", s.ModelRepo)
	case !validEngine(s.Engine):
		return fmt.Errorf("engine must be one of %s", strings.Join(Engines, ", "))
	case s.GPUCount < 1:
		return errors.New("GPU count must be at least 1")
	case s.MinGPURAMGB < 0:
		return errors.New("minimum GPU memory cannot be negative")
	case s.MaxDPH <= 0:
		return errors.New("max $/hr must be more than 0: it is the price ceiling for the rental")
	case s.BudgetUSD <= 0:
		return errors.New("budget must be more than 0")
	case s.MaxModelLen < 0:
		return errors.New("context length cannot be negative")
	case s.DiskGB < 1:
		return errors.New("disk must be at least 1 GB")
	case s.Name != "" && slug(s.Name) != s.Name:
		return fmt.Errorf("name %q may use only a-z, 0-9, '.', '_' and '-'", s.Name)
	}
	return nil
}

func validEngine(e string) bool {
	for _, v := range Engines {
		if v == e {
			return true
		}
	}
	return false
}

// FileName is the spec's file name, from its name or else its model.
func (s Spec) FileName() string {
	n := s.Name
	if n == "" {
		n = slug(s.ModelRepo[strings.LastIndex(s.ModelRepo, "/")+1:])
	}
	return n + ".yaml"
}

var notSlug = regexp.MustCompile(`[^a-z0-9._-]+`)

func slug(s string) string {
	return strings.Trim(notSlug.ReplaceAllString(strings.ToLower(s), "-"), "-.")
}

// YAML renders the spec as bbvastl80 reads it.
func (s Spec) YAML() string {
	id := s.ModelID
	if id == "" {
		id = s.ModelRepo
	}
	q := strconv.Quote
	num := func(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	w("# Written by `L80 vast spec`. Edit freely; every key is in bbvastl80's run.example.yaml.")
	w("name: %s", q(strings.TrimSuffix(s.FileName(), ".yaml")))
	w("")
	w("gpu:")
	if len(s.GPUNames) > 0 {
		names := make([]string, len(s.GPUNames))
		for i, n := range s.GPUNames {
			names[i] = q(n)
		}
		w("  names: [%s]   # the cheapest offer among these", strings.Join(names, ", "))
	} else {
		w("  # names: [\"RTX 4090\", \"A100 SXM4\"]   # any GPU when unset; `L80 vast search` shows offers")
	}
	w("  count: %d", s.GPUCount)
	if s.MinGPURAMGB > 0 {
		w("  min_ram_gb: %s   # total GPU memory across the GPUs", num(s.MinGPURAMGB))
	}
	w("  max_dph: %s   # $/hr ceiling: no offer above this is rented", num(s.MaxDPH))
	w("  type: ondemand")
	w("  verified: true")
	w("  min_reliability: 0.98")
	w("  pick: cheapest")
	w("")
	w("model:")
	w("  id: %s   # what BetterBench requests at /v1", q(id))
	w("  source: hf")
	w("  hf_repo: %s", q(s.ModelRepo))
	w("")
	w("serving:")
	w("  mode: engine")
	w("  port: 8000")
	w("  engine:")
	w("    engine: %s", s.Engine)
	if s.MaxModelLen > 0 {
		w("    max_model_len: %d", s.MaxModelLen)
	}
	if s.Engine == "vllm" {
		w("    gpu_mem_util: 0.90")
		if s.GPUCount > 1 {
			w("    extra_args: [\"--tensor-parallel-size\", \"%d\"]", s.GPUCount)
		}
	}
	w("")
	w("instance:")
	w("  disk_gb: %d", s.DiskGB)
	w("")
	w("benchmark:")
	w("  quick: %t", s.Quick)
	w("")
	w("budget:")
	w("  max_total_usd: %s   # the run refuses to start if its estimate is above this", num(s.BudgetUSD))
	w("  max_provision_min: 20")
	w("")
	w("lifecycle:")
	w("  teardown: auto   # destroy the instance when the run ends")
	return b.String()
}

// SpecsDir is where specs L80 writes are kept: next to the runs directory.
func SpecsDir() string { return filepath.Join(filepath.Dir(RunsDir()), "specs") }

// ErrSpecExists is returned by WriteSpec when the file is there and force is off.
var ErrSpecExists = errors.New("spec already exists")

// WriteSpec validates s and writes it to path, or to SpecsDir()/<name>.yaml
// when path is empty. It returns the path written.
func WriteSpec(s Spec, path string, force bool) (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	if path == "" {
		path = filepath.Join(SpecsDir(), s.FileName())
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if _, err := os.Stat(path); err == nil && !force {
		return path, fmt.Errorf("%w: %s", ErrSpecExists, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte(s.YAML()), 0o644)
}

// ListSpecs returns the .yaml files in dir, newest first.
func ListSpecs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type f struct {
		path string
		mod  int64
	}
	var fs []f
	for _, e := range entries {
		if e.IsDir() || !(strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".yml")) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		fs = append(fs, f{filepath.Join(dir, e.Name()), info.ModTime().UnixNano()})
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].mod > fs[j].mod })
	out := make([]string, len(fs))
	for i, x := range fs {
		out[i] = x.path
	}
	return out
}
