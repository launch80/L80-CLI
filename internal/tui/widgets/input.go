package widgets

import (
	"strings"

	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
)

// TextInput is a one-line editor. Mask hides the value (for keys).
type TextInput struct {
	Value       []rune
	Pos         int
	Placeholder string
	Mask        bool
	offset      int
}

func (ti *TextInput) String() string { return string(ti.Value) }

// Set replaces the value and puts the cursor at the end.
func (ti *TextInput) Set(s string) { ti.Value, ti.Pos = []rune(s), len([]rune(s)) }

func (ti *TextInput) insert(s string) {
	s = strings.NewReplacer("\n", " ", "\r", "", "\t", " ").Replace(style.Sanitize(s))
	r := []rune(s)
	ti.Value = append(ti.Value[:ti.Pos], append(r, ti.Value[ti.Pos:]...)...)
	ti.Pos += len(r)
}

// HandlePaste inserts pasted text.
func (ti *TextInput) HandlePaste(p input.Paste) { ti.insert(p.Text) }

// HandleKey edits; returns false for keys it does not use (Enter, Tab, ...).
func (ti *TextInput) HandleKey(k input.Key) bool {
	switch {
	case k.Code == input.Rune && !k.Ctrl && !k.Alt:
		ti.insert(string(k.Rune))
	case k.Code == input.Backspace:
		if ti.Pos > 0 {
			ti.Value = append(ti.Value[:ti.Pos-1], ti.Value[ti.Pos:]...)
			ti.Pos--
		}
	case k.Code == input.Delete || (k.Ctrl && k.Rune == 'd'):
		if ti.Pos < len(ti.Value) {
			ti.Value = append(ti.Value[:ti.Pos], ti.Value[ti.Pos+1:]...)
		}
	case k.Code == input.Left || (k.Ctrl && k.Rune == 'b'):
		if ti.Pos > 0 {
			ti.Pos--
		}
	case k.Code == input.Right || (k.Ctrl && k.Rune == 'f'):
		if ti.Pos < len(ti.Value) {
			ti.Pos++
		}
	case k.Code == input.Home || (k.Ctrl && k.Rune == 'a'):
		ti.Pos = 0
	case k.Code == input.End || (k.Ctrl && k.Rune == 'e'):
		ti.Pos = len(ti.Value)
	case k.Ctrl && k.Rune == 'u':
		ti.Value, ti.Pos = ti.Value[ti.Pos:], 0
	case k.Ctrl && k.Rune == 'k':
		ti.Value = ti.Value[:ti.Pos]
	case k.Ctrl && k.Rune == 'w':
		i := ti.Pos
		for i > 0 && ti.Value[i-1] == ' ' {
			i--
		}
		for i > 0 && ti.Value[i-1] != ' ' {
			i--
		}
		ti.Value, ti.Pos = append(ti.Value[:i], ti.Value[ti.Pos:]...), i
	default:
		return false
	}
	return true
}

// View draws the field in w cells with a block cursor when focused.
func (ti *TextInput) View(t Theme, w int, focused bool) string {
	if len(ti.Value) == 0 && !focused {
		return t.R(t.Muted, style.PadRight(ti.Placeholder, w))
	}
	shown := ti.Value
	if ti.Mask {
		shown = []rune(strings.Repeat("•", len(ti.Value)))
	}
	if w < 2 {
		w = 2
	}
	if ti.Pos < ti.offset {
		ti.offset = ti.Pos
	}
	for style.Width(string(shown[ti.offset:ti.Pos])) >= w-1 {
		ti.offset++
	}
	vis := shown[ti.offset:]
	if !focused {
		return style.PadRight(string(vis), w)
	}
	before := string(vis[:ti.Pos-ti.offset])
	at, after := " ", ""
	if ti.Pos < len(shown) {
		at = string(vis[ti.Pos-ti.offset])
		after = string(vis[ti.Pos-ti.offset+1:])
	}
	if len(ti.Value) == 0 && ti.Placeholder != "" {
		return t.R(style.Style{Reverse: true}, " ") + t.R(t.Muted, style.PadRight(ti.Placeholder, w-1))
	}
	line := before + t.R(style.Style{Reverse: true}, at) + after
	return style.PadRight(line, w)
}
