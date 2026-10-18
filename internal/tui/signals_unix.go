//go:build unix

package tui

import (
	"os"
	"syscall"
)

var watchedSignals = []os.Signal{syscall.SIGWINCH, syscall.SIGTERM, syscall.SIGHUP}

func isResize(s os.Signal) bool { return s == syscall.SIGWINCH }
