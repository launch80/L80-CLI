// Package pytool runs the Python tools L80 fronts (BetterBench, bbvastl80,
// pwdet, the dashboard) without the user installing them.
//
// Every tool is pinned to a git commit in manifest.json, which is embedded in
// the binary. Bumping a pin and tagging a release is how a change in one of
// those projects reaches users through `L80 update`. At run time a tool is
// found in a fixed order: an explicit env override, a copy on PATH that is new
// enough, or a `uvx` bootstrap of the pinned commit. uv caches the build, so
// the bootstrap costs a download once.
package pytool

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
)

//go:embed manifest.json
var manifestJSON []byte

// Tool is one entry of the manifest.
type Tool struct {
	// Name is the L80-facing name (the manifest key), e.g. "pwdet".
	Name string `json:"-"`
	// Repo is the HTTPS git URL; the runner adds the git+ prefix.
	Repo string `json:"repo"`
	// Ref is the tag or branch the commit was taken from. Informational, and
	// validated against the remote in CI; never used at run time.
	Ref string `json:"ref"`
	// Commit is the full 40-hex SHA that is actually run. Empty means the
	// tool is not pinned in this build and only env/PATH can supply it.
	Commit string `json:"commit"`
	// Package is the Python distribution name, Exe the console script.
	Package string `json:"package"`
	Exe     string `json:"exe"`
	// Env names the override variable, e.g. L80_PWDET. Its value is a command
	// line (split on whitespace) and is used unchecked.
	Env string `json:"env"`
	// Python is a uv --python constraint such as "3.12"; empty means any.
	Python string `json:"python,omitempty"`
	// Extras are the default package extras for the bootstrap.
	Extras []string `json:"extras,omitempty"`
	// MinVersion is the oldest version accepted from PATH. A PATH copy that is
	// older is skipped in favour of the pinned bootstrap when uvx exists.
	MinVersion string `json:"min_version,omitempty"`
	// VersionCmd prints the version, usually ["--version"]. Empty disables
	// the PATH version check.
	VersionCmd []string `json:"version_cmd,omitempty"`
	Notes      string   `json:"notes,omitempty"`
}

type manifest struct {
	Schema int             `json:"schema"`
	Tools  map[string]Tool `json:"tools"`
}

var (
	loadOnce sync.Once
	loaded   map[string]Tool
	loadErr  error
)

var commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Manifest returns every tool, keyed by name. The embedded file is parsed once.
func Manifest() (map[string]Tool, error) {
	loadOnce.Do(func() { loaded, loadErr = parseManifest(manifestJSON) })
	return loaded, loadErr
}

func parseManifest(data []byte) (map[string]Tool, error) {
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("tool manifest: %w", err)
	}
	if m.Schema != 1 {
		return nil, fmt.Errorf("tool manifest: unsupported schema %d", m.Schema)
	}
	out := make(map[string]Tool, len(m.Tools))
	for name, t := range m.Tools {
		t.Name = name
		switch {
		case t.Exe == "" || t.Package == "" || t.Repo == "" || t.Env == "":
			return nil, fmt.Errorf("tool manifest: %s needs repo, package, exe and env", name)
		case t.Commit != "" && !commitRE.MatchString(t.Commit):
			return nil, fmt.Errorf("tool manifest: %s commit %q is not a full lowercase sha", name, t.Commit)
		case !strings.HasPrefix(t.Env, "L80_"):
			return nil, fmt.Errorf("tool manifest: %s env override %q must start with L80_", name, t.Env)
		}
		out[name] = t
	}
	return out, nil
}

// ErrUnknownTool is returned by Lookup for a name the manifest lacks.
var ErrUnknownTool = errors.New("unknown tool")

// Lookup returns one tool by its L80 name.
func Lookup(name string) (Tool, error) {
	tools, err := Manifest()
	if err != nil {
		return Tool{}, err
	}
	t, ok := tools[name]
	if !ok {
		return Tool{}, fmt.Errorf("%w %q (known: %s)", ErrUnknownTool, name, strings.Join(Names(), ", "))
	}
	return t, nil
}

// Names lists the tools in the manifest, sorted.
func Names() []string {
	tools, err := Manifest()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(tools))
	for n := range tools {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Pinned reports whether this build carries a commit for the tool.
func (t Tool) Pinned() bool { return t.Commit != "" }

// ExtrasEnv is the variable that overrides the default extras, derived from
// the override variable: L80_PWDET -> L80_PWDET_EXTRAS. Comma-separated.
func (t Tool) ExtrasEnv() string { return t.Env + "_EXTRAS" }

// EffectiveExtras applies the ExtrasEnv override, if set, to the defaults.
func (t Tool) EffectiveExtras() []string {
	if v, ok := os.LookupEnv(t.ExtrasEnv()); ok {
		var out []string
		for _, x := range strings.Split(v, ",") {
			if x = strings.TrimSpace(x); x != "" {
				out = append(out, x)
			}
		}
		return out
	}
	return t.Extras
}

// Requirement is the PEP 508 string uv installs: "pkg[extras] @ git+repo@commit".
func (t Tool) Requirement() string {
	pkg := t.Package
	if extras := t.EffectiveExtras(); len(extras) > 0 {
		pkg += "[" + strings.Join(extras, ",") + "]"
	}
	return fmt.Sprintf("%s @ git+%s@%s", pkg, t.Repo, t.Commit)
}

// UvxArgv is the bootstrap command line for a pinned tool: uvx [--python X]
// --from <requirement> <exe>.
func (t Tool) UvxArgv(uvxPath string) []string {
	argv := []string{uvxPath}
	if t.Python != "" {
		argv = append(argv, "--python", t.Python)
	}
	return append(argv, "--from", t.Requirement(), t.Exe)
}

// ShortCommit is the first 12 characters of the pin, or "unpinned".
func (t Tool) ShortCommit() string {
	if t.Commit == "" {
		return "unpinned"
	}
	return t.Commit[:12]
}
