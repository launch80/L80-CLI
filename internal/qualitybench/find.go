package qualitybench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ResultFile is a QualityBench results file on this machine that compare and
// --reference can use: its .qb folder is beside it.
type ResultFile struct {
	Path      string
	Model     string
	Title     string
	Quick     bool
	Thinking  string
	Generated string
	Tasks     int
	HasLogp   bool
	// Compare is true for a combined document (a run with --reference, or
	// `compare` output); it is a page, not a reference to compare against.
	Compare bool
	mod     int64
}

// Label is one line for a picker.
func (r ResultFile) Label() string {
	s := r.Model
	if r.Quick {
		s += " · quick"
	}
	if r.Thinking == "on" {
		s += " · thinking on"
	}
	if r.Generated != "" {
		s += " · " + r.Generated
	}
	return s
}

// ReadResult reads the summary fields of a results file; ok is false when it
// is not a QualityBench results file.
func ReadResult(path string) (ResultFile, bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 4<<20 {
		return ResultFile{}, false
	}
	var d struct {
		SchemaVersion int             `json:"schema_version"`
		QBVersion     string          `json:"qualitybench_version"`
		Title         string          `json:"title"`
		Model         string          `json:"model"`
		Generated     string          `json:"generated"`
		Caveats       []string        `json:"caveats"`
		Tasks         []any           `json:"tasks"`
		Logprobs      map[string]any  `json:"logprobs"`
		Arms          json.RawMessage `json:"arms"`
		Env           struct {
			Thinking string `json:"thinking"`
		} `json:"env"`
	}
	if json.Unmarshal(data, &d) != nil || d.Model == "" || (d.QBVersion == "" && (d.SchemaVersion != SchemaVersion || d.Tasks == nil)) {
		return ResultFile{}, false
	}
	r := ResultFile{Path: path, Model: d.Model, Title: d.Title, Generated: d.Generated, Tasks: len(d.Tasks),
		HasLogp: len(d.Logprobs) > 0, Thinking: d.Env.Thinking, Compare: len(d.Arms) > 2}
	for _, c := range d.Caveats {
		if strings.HasPrefix(c, "Quick run") {
			r.Quick = true
		}
	}
	if info, err := os.Stat(path); err == nil {
		r.mod = info.ModTime().UnixNano()
	}
	return r, true
}

// FindResults lists results files with their .qb folder, newest first, from
// the given files (e.g. history's source paths) and the *.json files in dirs
// and one level below them (a rental's step folders).
func FindResults(files []string, dirs ...string) []ResultFile {
	seen := map[string]bool{}
	var out []ResultFile
	add := func(p string) {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if seen[p] {
			return
		}
		seen[p] = true
		if CheckSidecar(p) != nil {
			return
		}
		if r, ok := ReadResult(p); ok {
			out = append(out, r)
		}
	}
	for _, f := range files {
		add(f)
	}
	for _, d := range dirs {
		for _, pat := range []string{"*.json", "*/*.json", "*/*/*.json"} {
			m, _ := filepath.Glob(filepath.Join(d, pat))
			for _, p := range m {
				add(p)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].mod > out[j].mod })
	return out
}
