//go:build linux

package term

import "syscall"

const (
	ioctlGet     = syscall.TCGETS
	ioctlSet     = syscall.TCSETS
	ioctlWinsize = syscall.TIOCGWINSZ
)
