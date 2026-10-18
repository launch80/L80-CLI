// Package term puts a terminal into raw mode, reports its size, and emits
// the control sequences the TUI uses. Standard library only: termios is set
// with ioctl through package syscall (Linux and macOS; see raw_*.go).
//
// The approach follows golang.org/x/term and Bubble Tea's tty handling
// (MIT, Copyright (c) 2020-2026 Charmbracelet, Inc.; see THIRD_PARTY_NOTICES.md).
package term

import (
	"errors"
	"os"
)

// ErrUnsupported is returned on platforms without a raw-mode implementation.
var ErrUnsupported = errors.New("the interactive console needs Linux or macOS")

// OpenTTY opens the controlling terminal, for when stdin is redirected.
func OpenTTY() (*os.File, error) { return os.OpenFile("/dev/tty", os.O_RDWR, 0) }

// Control sequences.
const (
	AltScreenOn     = "\x1b[?1049h"
	AltScreenOff    = "\x1b[?1049l"
	HideCursor      = "\x1b[?25l"
	ShowCursor      = "\x1b[?25h"
	ClearScreen     = "\x1b[2J"
	Home            = "\x1b[H"
	EraseLineRight  = "\x1b[K"
	EraseBelow      = "\x1b[J"
	MouseOn         = "\x1b[?1000h\x1b[?1006h" // button events, SGR encoding
	MouseOff        = "\x1b[?1006l\x1b[?1000l"
	PasteOn         = "\x1b[?2004h"
	PasteOff        = "\x1b[?2004l"
	SyncBegin       = "\x1b[?2026h" // synchronized output; ignored where unsupported
	SyncEnd         = "\x1b[?2026l"
	ResetAttributes = "\x1b[0m"
)
