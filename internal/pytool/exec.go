package pytool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// ExecOpts controls how a tool process is run.
type ExecOpts struct {
	// Stdout/Stderr receive the child's output when Interactive is false. A
	// nil writer discards.
	Stdout, Stderr io.Writer
	// Stdin is the child's input when Interactive is false; nil means none.
	Stdin io.Reader
	// Interactive hands the child the real terminal (os.Stdin/out/err), for
	// ssh sessions and curses programs.
	Interactive bool
	// ExtraEnv is appended to the inherited environment, KEY=VALUE.
	ExtraEnv []string
	// Dir is the working directory; empty inherits.
	Dir string
	// GraceAfterCancel is how long the child gets after SIGINT, once ctx is
	// cancelled, before it is killed. Default 15s.
	GraceAfterCancel time.Duration
}

// ErrNotStarted wraps the reason a child could not be started at all.
var ErrNotStarted = errors.New("could not start")

// Exec runs the tool with args appended to the runner's argv and returns the
// child's exit status. A non-zero status is not an error: the caller decides
// what it means for that tool. err is non-nil only when the child could not
// be started or was killed by a signal (then code is -1).
//
// Cancelling ctx sends SIGINT and, after GraceAfterCancel, SIGKILL, so a
// benchmark or a rental run gets a chance to clean up.
func Exec(ctx context.Context, r Runner, args []string, o ExecOpts) (int, error) {
	argv := append(append([]string{}, r.Argv...), args...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = o.Dir
	cmd.Env = append(append(os.Environ(), r.Env...), o.ExtraEnv...)
	if !o.Interactive {
		// The tools are Python, which block-buffers a pipe: without this a
		// benchmark's progress would reach the log only when it finished.
		// First, so a value the user set still wins.
		cmd.Env = append([]string{"PYTHONUNBUFFERED=1"}, cmd.Env...)
	}
	if o.Interactive {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	} else {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = o.Stdin, o.Stdout, o.Stderr
	}
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = o.GraceAfterCancel
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = 15 * time.Second
	}

	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if code := exit.ExitCode(); code >= 0 {
			return code, nil
		}
		return -1, fmt.Errorf("%s was killed: %v", argv[0], exit)
	}
	return -1, fmt.Errorf("%w %s: %v", ErrNotStarted, argv[0], err)
}
