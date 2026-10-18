//go:build darwin

package term

import "syscall"

const (
	ioctlGet     = syscall.TIOCGETA
	ioctlSet     = syscall.TIOCSETA
	ioctlWinsize = syscall.TIOCGWINSZ
)
