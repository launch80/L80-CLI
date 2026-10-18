package pytool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Runner is a resolved way to invoke a tool.
type Runner struct {
	// Argv prefix: the executable (and any fixed arguments) to put before the
	// tool's own arguments.
	Argv []string
	// Source says how it was found: the env variable name, "PATH",
	// "PATH (older than pinned)", or "uvx (pinned <sha>)".
	Source string
	// Version is what the tool reported, when it was probed.
	Version string
	// Notes are one-line caveats worth showing the user.
	Notes []string
	// Env is extra KEY=VALUE set for the child, e.g. a secret passed by
	// environment instead of argv.
	Env  []string
	Tool Tool
}

// Pinned reports whether the runner is the pinned bootstrap, i.e. the version
// this L80 release was built for.
func (r Runner) Pinned() bool { return strings.HasPrefix(r.Source, "uvx") }

// lookPath and probeVersion are swapped in tests.
var (
	lookPath     = exec.LookPath
	probeVersion = defaultProbeVersion
)

// ErrNoRunner is returned when a tool cannot be run on this machine.
type ErrNoRunner struct {
	Tool   Tool
	HasUvx bool
	// Stale is set when a PATH copy exists but is too old and nothing better
	// was available either.
	Stale string
}

func (e *ErrNoRunner) Error() string {
	switch {
	case !e.Tool.Pinned() && e.HasUvx:
		return fmt.Sprintf("%s is not installed, and this L80 build does not pin a version to bootstrap", e.Tool.Exe)
	case !e.HasUvx:
		return fmt.Sprintf("%s is not installed and uvx is not available to bootstrap it", e.Tool.Exe)
	default:
		return fmt.Sprintf("%s could not be resolved", e.Tool.Exe)
	}
}

// Remedy is the sentence an error message should carry.
func (e *ErrNoRunner) Remedy() string {
	var parts []string
	if !e.HasUvx {
		parts = append(parts, "Install uv (https://docs.astral.sh/uv/) so L80 can bootstrap the pinned version")
	}
	if e.Tool.Pinned() {
		parts = append(parts, fmt.Sprintf("or install it yourself (`pip install \"%s\"`) so `%s` is on PATH", e.Tool.Requirement(), e.Tool.Exe))
	} else {
		parts = append(parts, fmt.Sprintf("install %s so `%s` is on PATH (see %s)", e.Tool.Package, e.Tool.Exe, e.Tool.Repo))
	}
	parts = append(parts, fmt.Sprintf("%s=/path/to/%s also works. `L80 tools check` shows what L80 can see.", e.Tool.Env, e.Tool.Exe))
	return strings.Join(parts, ", ")
}

// Resolve finds how to run a tool, in this order:
//
//  1. The tool's env override, if set: a command line split on whitespace,
//     used unchecked. Lets a checkout's .venv be used.
//  2. The console script on PATH, if its version is at least MinVersion (or
//     the tool has no version check).
//  3. `uvx --from <pinned requirement> <exe>` when uvx is on PATH and the
//     tool is pinned.
//  4. A PATH copy that was too old, with a note, rather than nothing.
func Resolve(t Tool) (Runner, error) {
	if v := strings.TrimSpace(os.Getenv(t.Env)); v != "" {
		return Runner{Argv: strings.Fields(v), Source: t.Env, Tool: t}, nil
	}

	var stale *Runner
	if p, err := lookPath(t.Exe); err == nil {
		r := Runner{Argv: []string{p}, Source: "PATH", Tool: t}
		if len(t.VersionCmd) == 0 || t.MinVersion == "" {
			return r, nil
		}
		v, perr := probeVersion(append([]string{p}, t.VersionCmd...))
		switch {
		case perr != nil:
			r.Notes = append(r.Notes, fmt.Sprintf("%s on PATH did not report a version (%v)", t.Exe, perr))
			stale = &r
		case Compare(v, t.MinVersion) < 0:
			r.Version = v
			r.Notes = append(r.Notes, fmt.Sprintf("%s %s on PATH is older than the %s this L80 was built for", t.Exe, v, t.MinVersion))
			stale = &r
		default:
			r.Version = v
			return r, nil
		}
	}

	uvx, uerr := lookPath("uvx")
	if uerr == nil && t.Pinned() {
		r := Runner{Argv: t.UvxArgv(uvx), Source: "uvx (pinned " + t.ShortCommit() + ")", Tool: t}
		if stale != nil {
			r.Notes = append(r.Notes, stale.Notes...)
			r.Notes = append(r.Notes, "using the pinned bootstrap instead")
		}
		return r, nil
	}
	if stale != nil {
		stale.Source = "PATH (older than pinned)"
		stale.Notes = append(stale.Notes, "nothing newer is available; results may not match what launch80 renders")
		return *stale, nil
	}
	return Runner{}, &ErrNoRunner{Tool: t, HasUvx: uerr == nil}
}

// defaultProbeVersion runs the version command with a short timeout and
// parses the first version number it prints.
func defaultProbeVersion(argv []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		if v, ok := ParseVersion(out.String()); ok {
			return v, nil
		}
		return "", err
	}
	v, ok := ParseVersion(out.String())
	if !ok {
		return "", errors.New("no version number in output")
	}
	return v, nil
}

// Probe runs the resolved tool's version command and fills Runner.Version.
// Used by `L80 tools check` for runners that were not probed during Resolve
// (the env override and the uvx bootstrap).
func (r *Runner) Probe() error {
	if len(r.Tool.VersionCmd) == 0 {
		return errors.New("tool has no version command")
	}
	v, err := probeVersion(append(append([]string{}, r.Argv...), r.Tool.VersionCmd...))
	if err != nil {
		return err
	}
	r.Version = v
	return nil
}
