package widgets

import (
	"strings"
	"sync"

	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
)

// LogPane is a scrolling view over a bounded buffer of lines. It follows the
// end until the user scrolls up, and again once they scroll back down.
// Lines are sanitised on the way in: a subprocess cannot reach the terminal.
type LogPane struct {
	mu     sync.Mutex
	lines  []string
	max    int
	scroll int // lines up from the bottom
	part   string
}

func NewLogPane(maxLines int) *LogPane { return &LogPane{max: maxLines} }

// Write appends output, splitting on newlines and carriage returns (a
// progress line rewritten with \r replaces itself).
func (l *LogPane) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.part + string(p)
	for {
		i := strings.IndexAny(s, "\n\r")
		if i < 0 {
			break
		}
		l.push(s[:i])
		if s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n' {
			i++
		}
		s = s[i+1:]
	}
	l.part = s
	return len(p), nil
}

// Line appends one line.
func (l *LogPane) Line(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.push(s)
}

func (l *LogPane) push(s string) {
	l.lines = append(l.lines, style.Sanitize(s))
	if len(l.lines) > l.max {
		l.lines = l.lines[len(l.lines)-l.max:]
	}
}

// Lines is a copy of the buffer.
func (l *LogPane) Lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := append([]string(nil), l.lines...)
	if l.part != "" {
		out = append(out, style.Sanitize(l.part))
	}
	return out
}

func (l *LogPane) Clear() {
	l.mu.Lock()
	l.lines, l.part, l.scroll = nil, "", 0
	l.mu.Unlock()
}

// HandleKey scrolls. End/G returns to following.
func (l *LogPane) HandleKey(k input.Key) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case k.Code == input.Up || (k.Code == input.Rune && k.Rune == 'k'):
		l.scroll++
	case k.Code == input.Down || (k.Code == input.Rune && k.Rune == 'j'):
		l.scroll--
	case k.Code == input.PageUp:
		l.scroll += 10
	case k.Code == input.PageDown:
		l.scroll -= 10
	case k.Code == input.Home || (k.Code == input.Rune && k.Rune == 'g'):
		l.scroll = len(l.lines)
	case k.Code == input.End || (k.Code == input.Rune && k.Rune == 'G'):
		l.scroll = 0
	default:
		return false
	}
	l.scroll = max(0, min(l.scroll, len(l.lines)))
	return true
}

// HandleMouse scrolls with the wheel.
func (l *LogPane) HandleMouse(m input.Mouse) {
	switch m.Button {
	case input.WheelUp:
		l.HandleKey(input.Key{Code: input.Up})
		l.HandleKey(input.Key{Code: input.Up})
		l.HandleKey(input.Key{Code: input.Up})
	case input.WheelDown:
		l.HandleKey(input.Key{Code: input.Down})
		l.HandleKey(input.Key{Code: input.Down})
		l.HandleKey(input.Key{Code: input.Down})
	}
}

// View draws the last h lines (or the scrolled window), cut to w.
func (l *LogPane) View(t Theme, w, h int) []string {
	lines := l.Lines()
	l.mu.Lock()
	scroll := l.scroll
	l.mu.Unlock()
	end := len(lines) - scroll
	start := max(0, end-h)
	out := make([]string, 0, h)
	for _, s := range lines[start:max(start, end)] {
		out = append(out, style.Truncate(s, w, "…"))
	}
	if scroll > 0 && len(out) > 0 {
		out[len(out)-1] = t.R(t.Warn, style.PadRight("  ↓ "+itoa(scroll)+" more below (End to follow)", w))
	}
	return out
}

// Spinner frames.
var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spin is the spinner frame for tick n.
func Spin(n int) string { return spinFrames[n%len(spinFrames)] }

// Progress draws a bar for frac (0..1) in w cells.
func Progress(t Theme, frac float64, w int) string {
	frac = max(0, min(1, frac))
	full := int(frac * float64(w))
	return t.R(t.Accent, strings.Repeat("█", full)) + t.R(t.Edge, strings.Repeat("░", w-full))
}

// Binding is a key hint for the help line.
type Binding struct{ Keys, Desc string }

// HelpLine renders hints that fit in w, separated by dots.
func HelpLine(t Theme, w int, bs ...Binding) string {
	var parts []string
	used := 0
	for _, b := range bs {
		p := t.R(t.Key, b.Keys) + " " + t.R(t.Muted, b.Desc)
		pw := style.Width(b.Keys) + 1 + style.Width(b.Desc) + 3
		if used+pw > w {
			break
		}
		parts = append(parts, p)
		used += pw
	}
	return strings.Join(parts, t.R(t.Edge, " · "))
}

// Dialog is a modal question with buttons; Choice is the focused button.
type Dialog struct {
	Title, Body string
	Buttons     []string
	Choice      int
	Danger      bool
}

// HandleKey moves between buttons; done is true on Enter or Esc (Esc picks
// the last button, conventionally Cancel).
func (d *Dialog) HandleKey(k input.Key) (done bool) {
	switch k.Code {
	case input.Left, input.Backtab:
		d.Choice = (d.Choice + len(d.Buttons) - 1) % len(d.Buttons)
	case input.Right, input.Tab:
		d.Choice = (d.Choice + 1) % len(d.Buttons)
	case input.Enter:
		return true
	case input.Esc:
		d.Choice = len(d.Buttons) - 1
		return true
	case input.Rune:
		for i, b := range d.Buttons {
			if len(b) > 0 && strings.EqualFold(b[:1], string(k.Rune)) {
				d.Choice = i
				return true
			}
		}
	}
	return false
}

// View draws the dialog box, centred by the caller.
func (d *Dialog) View(t Theme, w int) []string {
	w = min(w, 72)
	inner := w - 4
	var body []string
	body = append(body, "")
	for _, l := range strings.Split(d.Body, "\n") {
		for _, wl := range style.Wrap(l, inner) {
			body = append(body, " "+wl)
		}
	}
	body = append(body, "")
	var btns []string
	for i, b := range d.Buttons {
		s := " " + b + " "
		switch {
		case i == d.Choice && d.Danger && i == 0:
			s = t.R(style.Style{FG: "#ffffff", BG: "#d70000", Bold: true, Reverse: t.P == style.NoColor}, s)
		case i == d.Choice:
			s = t.R(t.Selected, s)
		default:
			s = t.R(t.Muted, s)
		}
		btns = append(btns, s)
	}
	body = append(body, " "+strings.Join(btns, "  "), "")
	edge := t.Accent
	if d.Danger {
		edge = t.Bad
	}
	return style.Box(t.Border, t.P, edge, d.Title, body, w, len(body)+2)
}
