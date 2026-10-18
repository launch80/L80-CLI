// Package input decodes terminal input bytes into key, mouse and paste
// events.
//
// The sequence handling follows Bubble Tea's key.go and ultraviolet's
// decoder (MIT, Copyright (c) 2020-2026 Charmbracelet, Inc.; see
// THIRD_PARTY_NOTICES.md), reduced to what xterm-compatible terminals send:
// CSI and SS3 cursor and function keys with xterm modifiers, SGR (1006)
// mouse, bracketed paste and UTF-8 text. A lone ESC is only reported once the
// reader has seen no follow-up byte for EscTimeout.
package input

import (
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Code identifies a key.
type Code int

const (
	Rune Code = iota // a printable character, in Key.Rune
	Enter
	Tab
	Backtab
	Backspace
	Esc
	Up
	Down
	Left
	Right
	Home
	End
	PageUp
	PageDown
	Insert
	Delete
	F1
	F2
	F3
	F4
	F5
	F6
	F7
	F8
	F9
	F10
	F11
	F12
)

var codeNames = map[Code]string{
	Enter: "enter", Tab: "tab", Backtab: "shift+tab", Backspace: "backspace", Esc: "esc",
	Up: "up", Down: "down", Left: "left", Right: "right", Home: "home", End: "end",
	PageUp: "pgup", PageDown: "pgdown", Insert: "insert", Delete: "delete",
	F1: "f1", F2: "f2", F3: "f3", F4: "f4", F5: "f5", F6: "f6", F7: "f7", F8: "f8", F9: "f9", F10: "f10", F11: "f11", F12: "f12",
}

// Event is a Key, a Mouse or a Paste.
type Event interface{ isEvent() }

// Key is one key press. Ctrl+letter arrives as Rune with Ctrl set and the
// lower-case letter in Rune.
type Key struct {
	Code             Code
	Rune             rune
	Alt, Ctrl, Shift bool
}

func (Key) isEvent() {}

// String names the key the way bindings are written: "q", "ctrl+c",
// "alt+enter", "shift+tab", "up".
func (k Key) String() string {
	var b strings.Builder
	if k.Ctrl {
		b.WriteString("ctrl+")
	}
	if k.Alt {
		b.WriteString("alt+")
	}
	if k.Shift && k.Code != Rune && k.Code != Backtab {
		b.WriteString("shift+")
	}
	if k.Code == Rune {
		if k.Rune == ' ' {
			b.WriteString("space")
		} else {
			b.WriteRune(k.Rune)
		}
		return b.String()
	}
	b.WriteString(codeNames[k.Code])
	return b.String()
}

// Button is a mouse button or wheel direction.
type Button int

const (
	ButtonNone Button = iota
	ButtonLeft
	ButtonMiddle
	ButtonRight
	WheelUp
	WheelDown
)

// Mouse is a mouse event in 0-based cell coordinates.
type Mouse struct {
	X, Y    int
	Button  Button
	Release bool
	Motion  bool
}

func (Mouse) isEvent() {}

// Paste is text delivered by bracketed paste: it must be inserted, never
// interpreted as key presses.
type Paste struct{ Text string }

func (Paste) isEvent() {}

// EscTimeout is how long a lone ESC waits for the rest of a sequence.
const EscTimeout = 50 * time.Millisecond

// Parse decodes as many complete events from buf as it can and reports how
// many bytes it used. With final false, a buffer that ends inside a sequence
// leaves those bytes unconsumed for the next read; with final true they are
// decoded as best they can be (a lone ESC becomes Esc).
func Parse(buf []byte, final bool) ([]Event, int) {
	var out []Event
	i := 0
	for i < len(buf) {
		ev, n := parseOne(buf[i:], final)
		if n == 0 {
			break
		}
		if ev != nil {
			out = append(out, ev)
		}
		i += n
	}
	return out, i
}

// parseOne returns one event (or nil for a recognised-but-ignored sequence)
// and the bytes it consumed; 0 bytes means "need more input".
func parseOne(b []byte, final bool) (Event, int) {
	c := b[0]
	switch {
	case c == 0x1b:
		return parseEsc(b, final)
	case c == '\r' || c == '\n':
		return Key{Code: Enter}, 1
	case c == '\t':
		return Key{Code: Tab}, 1
	case c == 0x7f || c == 0x08:
		return Key{Code: Backspace}, 1
	case c == 0x00:
		return Key{Code: Rune, Rune: ' ', Ctrl: true}, 1
	case c < 0x20:
		return Key{Code: Rune, Rune: rune('a' + c - 1), Ctrl: true}, 1
	case c < 0x80:
		return Key{Code: Rune, Rune: rune(c)}, 1
	}
	if !utf8.FullRune(b) {
		if !final {
			return nil, 0
		}
		return Key{Code: Rune, Rune: utf8.RuneError}, 1
	}
	r, n := utf8.DecodeRune(b)
	return Key{Code: Rune, Rune: r}, n
}

func parseEsc(b []byte, final bool) (Event, int) {
	if len(b) == 1 {
		if final {
			return Key{Code: Esc}, 1
		}
		return nil, 0
	}
	switch b[1] {
	case '[':
		return parseCSI(b, final)
	case 'O':
		if len(b) < 3 {
			if final {
				return Key{Code: Rune, Rune: 'O', Alt: true}, 2
			}
			return nil, 0
		}
		if k, ok := ss3Keys[b[2]]; ok {
			return k, 3
		}
		return nil, 3
	case 0x1b:
		// ESC ESC: the first is a lone Esc.
		return Key{Code: Esc}, 1
	}
	// Alt + the next key.
	ev, n := parseOne(b[1:], final)
	if n == 0 {
		return nil, 0
	}
	if k, ok := ev.(Key); ok {
		k.Alt = true
		return k, n + 1
	}
	return ev, n + 1
}

var ss3Keys = map[byte]Key{
	'A': {Code: Up}, 'B': {Code: Down}, 'C': {Code: Right}, 'D': {Code: Left},
	'H': {Code: Home}, 'F': {Code: End}, 'P': {Code: F1}, 'Q': {Code: F2}, 'R': {Code: F3}, 'S': {Code: F4},
}

var csiLetters = map[byte]Code{'A': Up, 'B': Down, 'C': Right, 'D': Left, 'H': Home, 'F': End, 'P': F1, 'Q': F2, 'R': F3, 'S': F4}

var tildeKeys = map[int]Code{
	1: Home, 2: Insert, 3: Delete, 4: End, 5: PageUp, 6: PageDown, 7: Home, 8: End,
	11: F1, 12: F2, 13: F3, 14: F4, 15: F5, 17: F6, 18: F7, 19: F8, 20: F9, 21: F10, 23: F11, 24: F12,
}

const pasteStart, pasteEnd = "\x1b[200~", "\x1b[201~"

func parseCSI(b []byte, final bool) (Event, int) {
	// Bracketed paste: everything up to the end marker is text.
	if strings.HasPrefix(string(b), pasteStart) {
		rest := string(b[len(pasteStart):])
		if end := strings.Index(rest, pasteEnd); end >= 0 {
			return Paste{Text: rest[:end]}, len(pasteStart) + end + len(pasteEnd)
		}
		if !final {
			return nil, 0
		}
		return Paste{Text: rest}, len(b)
	}
	// Find the final byte (0x40-0x7e) after parameter/intermediate bytes.
	j := 2
	for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
		j++
	}
	if j >= len(b) {
		if !final {
			return nil, 0
		}
		return Key{Code: Rune, Rune: '[', Alt: true}, 2
	}
	params, fin := string(b[2:j]), b[j]
	n := j + 1

	if strings.HasPrefix(params, "<") && (fin == 'M' || fin == 'm') {
		return parseSGRMouse(params[1:], fin == 'm'), n
	}
	if fin == 'Z' {
		return Key{Code: Backtab, Shift: true}, n
	}
	if fin == 'I' || fin == 'O' { // focus in/out
		return nil, n
	}
	fields := strings.Split(params, ";")
	mod := 0
	if len(fields) > 1 {
		mod, _ = strconv.Atoi(fields[1])
	}
	apply := func(k Key) Key {
		if mod > 1 {
			m := mod - 1
			k.Shift, k.Alt, k.Ctrl = m&1 != 0, m&2 != 0, m&4 != 0
		}
		return k
	}
	if code, ok := csiLetters[fin]; ok {
		return apply(Key{Code: code}), n
	}
	if fin == '~' {
		num, _ := strconv.Atoi(fields[0])
		if code, ok := tildeKeys[num]; ok {
			return apply(Key{Code: code}), n
		}
	}
	return nil, n // a sequence we do not use
}

func parseSGRMouse(params string, release bool) Event {
	f := strings.Split(params, ";")
	if len(f) != 3 {
		return nil
	}
	b, _ := strconv.Atoi(f[0])
	x, _ := strconv.Atoi(f[1])
	y, _ := strconv.Atoi(f[2])
	m := Mouse{X: x - 1, Y: y - 1, Release: release, Motion: b&32 != 0}
	switch {
	case b&64 != 0 && b&1 == 0:
		m.Button = WheelUp
	case b&64 != 0:
		m.Button = WheelDown
	default:
		m.Button = [...]Button{ButtonLeft, ButtonMiddle, ButtonRight, ButtonNone}[b&3]
	}
	return m
}

// Read decodes events from r until it fails, sending each to out. Bytes
// that end inside a sequence wait EscTimeout for the rest. It returns the
// read error (io.EOF, or whatever closing r produced).
func Read(r io.Reader, out chan<- Event) error {
	type chunk struct {
		b   []byte
		err error
	}
	reads := make(chan chunk)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			b := append([]byte(nil), buf[:n]...)
			reads <- chunk{b, err}
			if err != nil {
				return
			}
		}
	}()
	var pending []byte
	var timer <-chan time.Time
	for {
		select {
		case c := <-reads:
			pending = append(pending, c.b...)
			evs, n := Parse(pending, false)
			for _, e := range evs {
				out <- e
			}
			pending = pending[n:]
			timer = nil
			if len(pending) > 0 {
				timer = time.After(EscTimeout)
			}
			if c.err != nil {
				evs, _ := Parse(pending, true)
				for _, e := range evs {
					out <- e
				}
				return c.err
			}
		case <-timer:
			evs, n := Parse(pending, true)
			for _, e := range evs {
				out <- e
			}
			pending = pending[n:]
			timer = nil
		}
	}
}
