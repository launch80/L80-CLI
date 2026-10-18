package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/output"
	"github.com/launch80/L80-CLI/internal/pytool"
)

// The wrapper commands (vast, pwdet, dashboard) hand almost everything to
// the Python tool, which stays the source of truth for its own flags. L80
// takes a short, documented list of its own flags out of the arguments, and
// stops looking at the first "--": everything after it is forwarded
// verbatim, so a tool flag that happens to share a name with an L80 one can
// still be passed.

// ownedFlags names the flags L80 consumes for one verb: true = takes a value.
type ownedFlags map[string]bool

// splitOwned separates L80's flags from the tool's. It accepts --name,
// -name, --name=value and --name value.
func splitOwned(args []string, owned ownedFlags) (map[string]string, []string, error) {
	got := map[string]string{}
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return got, append(rest, args[i+1:]...), nil
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			rest = append(rest, a)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		takes, ok := owned[name]
		if !ok {
			rest = append(rest, a)
			continue
		}
		switch {
		case !takes && hasVal:
			got[name] = val
		case !takes:
			got[name] = "true"
		case hasVal:
			got[name] = val
		case i+1 < len(args):
			got[name] = args[i+1]
			i++
		default:
			return nil, nil, fmt.Errorf("--%s needs a value", name)
		}
	}
	return got, rest, nil
}

func isTrue(v string) bool { return v == "true" || v == "1" || v == "yes" }

// resolveTool finds a tool or explains, as an L80 error, why it cannot run.
func resolveTool(name string) (pytool.Runner, *api.Error) {
	t, err := pytool.Lookup(name)
	if err != nil {
		return pytool.Runner{}, api.Newf("E_INTERNAL", "Reinstall L80.", "%v", err)
	}
	r, err := pytool.Resolve(t)
	if err != nil {
		var nr *pytool.ErrNoRunner
		remedy := "Run `L80 tools check " + name + "`."
		if errors.As(err, &nr) {
			remedy = nr.Remedy()
		}
		return pytool.Runner{}, api.Newf("E_TOOL_UNAVAILABLE", remedy, "%v", err)
	}
	return r, nil
}

// childRun is the outcome of running a wrapped tool.
type childRun struct {
	code        int
	interrupted bool
}

// runChild runs the tool attached to this process's stdin and to e's
// writers (the real terminal when e is os.Stdout/os.Stderr, so prompts,
// colours and curses work). It returns the child's exit status.
//
// Ctrl-C reaches the child directly, since it shares the terminal's process
// group, so the first one is only noted: the tool gets to clean up (a rental
// run tears its box down) and L80 waits. A second Ctrl-C, or SIGTERM,
// interrupts it explicitly and kills it after a grace period. stop, if
// non-nil, cancels the child the same way (used by the vast watchdog).
func runChild(e env, r pytool.Runner, args []string, extraEnv []string, stop <-chan struct{}) (childRun, error) {
	e = synced(e)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)

	var res childRun
	var interrupted atomic.Bool
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigs:
				if s == os.Interrupt && !interrupted.Load() {
					interrupted.Store(true)
					fmt.Fprintln(e.stderr, "\n  L80: interrupt passed to the tool; waiting for it to stop (Ctrl-C again to force)")
					continue
				}
				interrupted.Store(true)
				cancel()
			case <-stop:
				cancel()
			case <-done:
				return
			}
		}
	}()
	code, err := pytool.Exec(ctx, r, args, pytool.ExecOpts{
		Stdin: os.Stdin, Stdout: e.stdout, Stderr: e.stderr, ExtraEnv: extraEnv, GraceAfterCancel: 60 * time.Second,
	})
	close(done)
	res.code, res.interrupted = code, interrupted.Load()
	return res, err
}

// exitFromChild turns a child outcome into L80's exit status: the child's own
// status, verbatim; a start failure is E_TOOL_UNAVAILABLE.
func exitFromChild(e env, name string, res childRun, err error) int {
	if err != nil {
		if errors.Is(err, pytool.ErrNotStarted) {
			return fail(e, api.Newf("E_TOOL_UNAVAILABLE", "Run `L80 tools check "+name+"`.", "%v", err))
		}
		fmt.Fprintf(e.stderr, "  L80: %v\n", err)
		return 130
	}
	return res.code
}

// toolHeader is printed above a tool's own --help.
func toolHeader(e env, r pytool.Runner, lines ...string) {
	for _, l := range lines {
		fmt.Fprintln(e.stderr, l)
	}
	output.Detailf(e.stderr, "runs %s via %s", r.Tool.Exe, r.Source)
	fmt.Fprintln(e.stderr)
}

func wantsHelp(args []string) bool {
	return len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help"
}

// stateDir is $XDG_STATE_HOME/L80, else ~/.local/state/L80.
func stateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "L80")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "L80-state")
	}
	return filepath.Join(home, ".local", "state", "L80")
}

// selfPath is this binary, symlinks resolved, for tools that call back into L80.
func selfPath() string {
	p, err := currentExecutable()
	if err != nil {
		return "L80"
	}
	return p
}

// toolShim writes an executable that runs a pinned tool through this L80,
// for programs that take a single executable path (bbvastl80 runs
// $L80_BETTERBENCH directly, so it cannot be handed a uvx command line).
//
// The shim pins the tool's override variable to the value it has now, so
// the user's own override is honoured and the shim can never resolve to
// itself.
func toolShim(name string) (string, error) {
	t, err := pytool.Lookup(name)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(stateDir(), "shims")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, t.Exe)
	body := fmt.Sprintf("#!/bin/sh\n# Written by L80: runs the %s pinned in this L80 release.\n%s=%s exec %s tools run %s \"$@\"\n",
		name, t.Env, shQuote(os.Getenv(t.Env)), shQuote(selfPath()), name)
	if old, err := os.ReadFile(path); err == nil && string(old) == body {
		return path, nil
	}
	tmp, err := os.CreateTemp(dir, "."+t.Exe+"-*")
	if err != nil {
		return "", err
	}
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return path, os.Rename(tmp.Name(), path)
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// lockedWriter serialises writes from goroutines that share one writer (a
// child's output copier and L80's own notes).
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// synced returns e with writers safe for concurrent use. An *os.File is left
// alone: it is already safe, and wrapping it would make exec hand the child a
// pipe instead of the terminal, which breaks prompts, colour and curses.
// stdout and stderr that are the same writer share one lock.
func synced(e env) env {
	wrap := func(w io.Writer) io.Writer {
		switch w.(type) {
		case *os.File, *lockedWriter:
			return w
		}
		return &lockedWriter{w: w}
	}
	if e.stdout == e.stderr {
		w := wrap(e.stderr)
		return env{stdout: w, stderr: w}
	}
	return env{stdout: wrap(e.stdout), stderr: wrap(e.stderr)}
}
