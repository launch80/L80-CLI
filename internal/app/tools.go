package app

import (
	"errors"
	"os/exec"

	"github.com/launch80/L80-CLI/internal/pytool"
)

// ToolStatus is one row of `L80 tools check`.
type ToolStatus struct {
	Name     string   `json:"name"`
	Exe      string   `json:"exe"`
	Pinned   string   `json:"pinned"` // "<ref> (<sha12>)" or "unpinned"
	Status   string   `json:"status"` // ok | stale | missing
	Source   string   `json:"source,omitempty"`
	Version  string   `json:"version,omitempty"`
	Minimum  string   `json:"min_version,omitempty"`
	Argv     []string `json:"argv,omitempty"`
	Notes    []string `json:"notes,omitempty"`
	Override string   `json:"override_env"`
	Remedy   string   `json:"remedy,omitempty"`
}

// ToolsCheck resolves each named tool (all when names is empty). With probe,
// a runner that was not version-checked during resolution (the env override,
// the uvx bootstrap) is asked for its version too; for a uvx runner that
// downloads and builds the pinned commit on first use.
func ToolsCheck(names []string, probe bool) ([]ToolStatus, error) {
	return ToolsCheckSelective(names, func(pytool.Runner) bool { return probe })
}

// ToolsCheckSelective is ToolsCheck with the probe decided per runner, so a
// caller can ask an env override for its version without making a uvx
// bootstrap download the pinned commit.
func ToolsCheckSelective(names []string, probe func(pytool.Runner) bool) ([]ToolStatus, error) {
	if len(names) == 0 {
		names = pytool.Names()
	}
	out := make([]ToolStatus, 0, len(names))
	for _, name := range names {
		t, err := pytool.Lookup(name)
		if err != nil {
			return nil, err
		}
		st := ToolStatus{Name: name, Exe: t.Exe, Pinned: pinLabel(t), Minimum: t.MinVersion, Override: t.Env}
		r, err := pytool.Resolve(t)
		if err != nil {
			st.Status = "missing"
			var nr *pytool.ErrNoRunner
			if errors.As(err, &nr) {
				st.Remedy = nr.Remedy()
			}
			st.Notes = append(st.Notes, err.Error())
			out = append(out, st)
			continue
		}
		if r.Version == "" && len(t.VersionCmd) > 0 && probe(r) {
			if perr := r.Probe(); perr != nil {
				r.Notes = append(r.Notes, "version probe failed: "+perr.Error())
			}
		}
		st.Source, st.Version, st.Argv, st.Notes = r.Source, r.Version, r.Argv, append(st.Notes, r.Notes...)
		st.Status = "ok"
		if r.Source == "PATH (older than pinned)" || (r.Version != "" && t.MinVersion != "" && pytool.Compare(r.Version, t.MinVersion) < 0) {
			st.Status = "stale"
		}
		out = append(out, st)
	}
	return out, nil
}

func pinLabel(t pytool.Tool) string {
	if !t.Pinned() {
		return "unpinned"
	}
	if t.Ref != "" {
		return t.Ref + " (" + t.ShortCommit() + ")"
	}
	return t.ShortCommit()
}

// HasUvx reports whether uv's tool runner is on PATH.
func HasUvx() bool {
	_, err := exec.LookPath("uvx")
	return err == nil
}
