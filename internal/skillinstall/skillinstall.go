// Package skillinstall puts the bundled skills where agent harnesses look for
// them. Every harness here reads the same open Agent Skills layout (a folder
// holding SKILL.md), so one copy of a skill works in all of them; only the
// directory differs. The CLI's `skills` command and the console's onboarding
// both install through this package, so they never disagree about paths.
package skillinstall

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	l80skills "github.com/launch80/L80-CLI"
)

// Target is one harness's user-level skills directory.
type Target struct {
	Name string // what --target takes
	// Harness names the agents that read this directory, for people.
	Harness string
	// Dir is the skills directory itself.
	Dir func() (string, error)
	// Markers are paths under $HOME whose presence means the harness is
	// installed here; `--target detected` installs only into those.
	Markers []string
}

// Targets lists every known harness, Claude Code first.
//
// ~/.agents/skills is the cross-harness location: Codex and Cursor both read
// it, so it is one target with those names as aliases rather than three
// copies of every skill showing up twice in Cursor.
var Targets = []Target{
	{Name: "claude-code", Harness: "Claude Code", Dir: claudeDir, Markers: []string{".claude"}},
	{Name: "agents", Harness: "Codex, Cursor and other Agent Skills harnesses", Dir: homeDir(".agents", "skills"), Markers: []string{".agents", ".codex", ".cursor"}},
	{Name: "gemini", Harness: "Gemini CLI", Dir: homeDir(".gemini", "skills"), Markers: []string{".gemini"}},
	{Name: "opencode", Harness: "OpenCode", Dir: opencodeDir, Markers: []string{".config/opencode"}},
}

// aliases map a harness name to the target that serves it.
var aliases = map[string]string{"codex": "agents", "cursor": "agents", "claude": "claude-code"}

// Names is every accepted --target value, for usage messages.
func Names() []string {
	out := []string{}
	for _, t := range Targets {
		out = append(out, t.Name)
	}
	return append(out, "codex", "cursor", "detected")
}

// Lookup resolves a --target value (or alias) to its Target.
func Lookup(name string) (Target, bool) {
	if a, ok := aliases[name]; ok {
		name = a
	}
	for _, t := range Targets {
		if t.Name == name {
			return t, true
		}
	}
	return Target{}, false
}

// Detected reports whether the harness appears to be installed for this user.
func (t Target) Detected() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	// The harness's own config directory (the skills directory's parent)
	// counts, so CLAUDE_CONFIG_DIR and XDG_CONFIG_HOME are honoured.
	if d, err := t.Dir(); err == nil {
		if _, err := os.Stat(filepath.Dir(d)); err == nil {
			return true
		}
	}
	for _, m := range t.Markers {
		if _, err := os.Stat(filepath.Join(home, m)); err == nil {
			return true
		}
	}
	return false
}

// Detected lists the targets whose harness is installed here.
func Detected() []Target {
	var out []Target
	for _, t := range Targets {
		if t.Detected() {
			out = append(out, t)
		}
	}
	return out
}

// SkillDir is where skill name lives under target t.
func (t Target) SkillDir(name string) (string, error) {
	dir, err := t.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// claudeDir resolves ~/.claude/skills, honouring CLAUDE_CONFIG_DIR.
func claudeDir() (string, error) {
	if root := os.Getenv("CLAUDE_CONFIG_DIR"); root != "" {
		return filepath.Join(root, "skills"), nil
	}
	return homeDir(".claude", "skills")()
}

func opencodeDir() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "opencode", "skills"), nil
	}
	return homeDir(".config", "opencode", "skills")()
}

func homeDir(parts ...string) func() (string, error) {
	return func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(append([]string{home}, parts...)...), nil
	}
}

// ErrExists is returned when the destination exists and Force is not set.
var ErrExists = errors.New("already installed")

// Opts controls one installation.
type Opts struct {
	// DevSource, when set, symlinks this directory instead of copying the
	// embedded skill, so edits in a source tree are live.
	DevSource string
	// Force replaces an existing installation.
	Force bool
}

// Install puts skill name at dest (a directory path ending in the skill name).
func Install(name, dest string, o Opts) error {
	if !Bundled(name) {
		return fmt.Errorf("no bundled skill named %q", name)
	}
	if info, err := os.Lstat(dest); err == nil {
		if !o.Force {
			kind := "directory"
			if info.Mode()&os.ModeSymlink != 0 {
				kind = "symlink"
				if link, e2 := os.Readlink(dest); e2 == nil {
					kind = "symlink -> " + link
				}
			}
			return fmt.Errorf("%w: %s (%s)", ErrExists, dest, kind)
		}
		if err := os.RemoveAll(dest); err != nil {
			return fmt.Errorf("could not replace %s: %v", dest, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if o.DevSource != "" {
		if err := os.Symlink(o.DevSource, dest); err != nil {
			return fmt.Errorf("could not symlink: %v", err)
		}
		return nil
	}
	return Materialize(name, dest)
}

// Bundled reports whether this binary ships skill name.
func Bundled(name string) bool {
	for _, n := range l80skills.SkillNames {
		if n == name {
			return true
		}
	}
	return false
}

// Materialize copies the embedded skill out of the binary, so a downloaded L80
// with no source tree installs exactly the same content.
func Materialize(name, dest string) error {
	root := filepath.Join("skills", name)

	return fs.WalkDir(l80skills.FS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out := filepath.Join(dest, rel)

		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		data, readErr := l80skills.FS.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, data, 0o644)
	})
}

// Matches reports whether every file of the embedded skill is on disk
// with the same content. Files the user added are ignored.
func Matches(name, dest string) bool {
	root := filepath.Join("skills", name)
	same := true
	_ = fs.WalkDir(l80skills.FS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !same || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		want, _ := l80skills.FS.ReadFile(path)
		got, rerr := os.ReadFile(filepath.Join(dest, rel))
		if rerr != nil || !bytes.Equal(got, want) {
			same = false
		}
		return nil
	})
	return same
}

// State describes one skill at one destination.
type State string

const (
	NotInstalled State = "not_installed"
	Linked       State = "linked" // a --dev symlink; never touched
	Current      State = "current"
	Outdated     State = "outdated"
)

// Status reports the state of skill name at dest.
func Status(name, dest string) State {
	info, err := os.Lstat(dest)
	switch {
	case err != nil:
		return NotInstalled
	case info.Mode()&os.ModeSymlink != 0:
		return Linked
	case Matches(name, dest):
		return Current
	}
	return Outdated
}
