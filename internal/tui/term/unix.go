//go:build linux || darwin

package term

import (
	"errors"
	"syscall"
	"unsafe"
)

// State is a saved terminal mode, restored by Restore.
type State struct{ termios syscall.Termios }

// winsize mirrors struct winsize; package syscall does not define it.
type winsize struct{ Row, Col, Xpixel, Ypixel uint16 }

func ioctl(fd, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

// IsTerminal reports whether fd is a terminal.
func IsTerminal(fd uintptr) bool {
	var t syscall.Termios
	return ioctl(fd, ioctlGet, unsafe.Pointer(&t)) == nil
}

// MakeRaw switches fd to raw mode (no echo, no line buffering, no signal
// keys: Ctrl-C arrives as a byte) and returns the previous state, the same
// flags as cfmakeraw(3).
func MakeRaw(fd uintptr) (*State, error) {
	var t syscall.Termios
	if err := ioctl(fd, ioctlGet, unsafe.Pointer(&t)); err != nil {
		return nil, err
	}
	old := State{termios: t}
	t.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	t.Oflag &^= syscall.OPOST
	t.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	t.Cflag &^= syscall.CSIZE | syscall.PARENB
	t.Cflag |= syscall.CS8
	t.Cc[syscall.VMIN] = 1
	t.Cc[syscall.VTIME] = 0
	if err := ioctl(fd, ioctlSet, unsafe.Pointer(&t)); err != nil {
		return nil, err
	}
	return &old, nil
}

// Restore puts fd back into a saved state.
func Restore(fd uintptr, s *State) error {
	if s == nil {
		return nil
	}
	t := s.termios
	return ioctl(fd, ioctlSet, unsafe.Pointer(&t))
}

// Size returns the terminal's width and height in cells.
func Size(fd uintptr) (w, h int, err error) {
	var ws winsize
	if err := ioctl(fd, ioctlWinsize, unsafe.Pointer(&ws)); err != nil {
		return 0, 0, err
	}
	if ws.Col == 0 || ws.Row == 0 {
		return 0, 0, errors.New("terminal reports a zero size")
	}
	return int(ws.Col), int(ws.Row), nil
}
