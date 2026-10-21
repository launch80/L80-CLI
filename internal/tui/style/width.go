// Package style measures, cuts and decorates text for the terminal: display
// width that ignores escape sequences and knows wide characters, ANSI-aware
// truncation and padding, colours and attributes, borders, and joining blocks
// side by side or stacked.
//
// The semantics follow lipgloss (MIT, Copyright (c) 2020-2026 Charmbracelet,
// Inc.; see THIRD_PARTY_NOTICES.md). Width uses a small table of wide and
// zero-width ranges instead of the Unicode width tables lipgloss depends on;
// for the mostly-ASCII text L80 draws, a miss costs alignment, never
// corruption, because every cut re-measures.
package style

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// wide lists ranges drawn two cells wide: Hangul Jamo, CJK, Hangul syllables,
// fullwidth forms and the common emoji blocks.
var wide = [][2]rune{
	{0x1100, 0x115F}, {0x231A, 0x231B}, {0x2329, 0x232A}, {0x23E9, 0x23EC}, {0x23F0, 0x23F0}, {0x23F3, 0x23F3},
	{0x25FD, 0x25FE}, {0x2614, 0x2615}, {0x2648, 0x2653}, {0x267F, 0x267F}, {0x2693, 0x2693}, {0x26A1, 0x26A1},
	{0x26AA, 0x26AB}, {0x26BD, 0x26BE}, {0x26C4, 0x26C5}, {0x26CE, 0x26CE}, {0x26D4, 0x26D4}, {0x26EA, 0x26EA},
	{0x26F2, 0x26F3}, {0x26F5, 0x26F5}, {0x26FA, 0x26FA}, {0x26FD, 0x26FD}, {0x2705, 0x2705}, {0x270A, 0x270B},
	{0x2728, 0x2728}, {0x274C, 0x274C}, {0x274E, 0x274E}, {0x2753, 0x2755}, {0x2757, 0x2757}, {0x2795, 0x2797},
	{0x27B0, 0x27B0}, {0x27BF, 0x27BF}, {0x2B1B, 0x2B1C}, {0x2B50, 0x2B50}, {0x2B55, 0x2B55},
	{0x2E80, 0x303E}, {0x3041, 0x33FF}, {0x3400, 0x4DBF}, {0x4E00, 0x9FFF}, {0xA000, 0xA4CF}, {0xA960, 0xA97F},
	{0xAC00, 0xD7A3}, {0xF900, 0xFAFF}, {0xFE10, 0xFE19}, {0xFE30, 0xFE6F}, {0xFF00, 0xFF60}, {0xFFE0, 0xFFE6},
	{0x1F004, 0x1F004}, {0x1F0CF, 0x1F0CF}, {0x1F18E, 0x1F18E}, {0x1F191, 0x1F19A}, {0x1F200, 0x1F251},
	{0x1F300, 0x1F64F}, {0x1F680, 0x1F6FF}, {0x1F7E0, 0x1F7EB}, {0x1F90C, 0x1F9FF}, {0x1FA70, 0x1FAFF},
	{0x20000, 0x2FFFD}, {0x30000, 0x3FFFD},
}

// RuneWidth is the cells a rune occupies: 0, 1 or 2.
func RuneWidth(r rune) int {
	switch {
	case r == 0 || r == 0x200B || r == 0x200C || r == 0x200D || r == 0xFEFF:
		return 0
	case r >= 0xFE00 && r <= 0xFE0F, r >= 0x1F3FB && r <= 0x1F3FF, r >= 0xE0100 && r <= 0xE01EF:
		return 0 // variation selectors, skin-tone modifiers
	case r < 0x20 || (r >= 0x7F && r < 0xA0):
		return 0
	case r < 0x1100:
		if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) {
			return 0
		}
		return 1
	case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r):
		return 0
	}
	lo, hi := 0, len(wide)-1
	for lo <= hi {
		m := (lo + hi) / 2
		switch {
		case r < wide[m][0]:
			hi = m - 1
		case r > wide[m][1]:
			lo = m + 1
		default:
			return 2
		}
	}
	return 1
}

// escLen is the length of the escape sequence at the start of s (CSI, OSC
// or a two-byte escape), or 0 if s does not start with one.
func escLen(s string) int {
	if len(s) < 2 || s[0] != 0x1b {
		return 0
	}
	switch s[1] {
	case '[':
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']':
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	default:
		return 2
	}
}

// Width is the display width of s, ignoring escape sequences. A ZWJ joins
// the next rune into the previous cluster, so an emoji family counts once.
func Width(s string) int {
	w, join := 0, false
	for i := 0; i < len(s); {
		if n := escLen(s[i:]); n > 0 {
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r == 0x200D {
			join = true
			continue
		}
		if join {
			join = false
			continue
		}
		w += RuneWidth(r)
	}
	return w
}

// MaxWidth is the width of the widest line of s.
func MaxWidth(s string) int {
	m := 0
	for _, l := range strings.Split(s, "\n") {
		if w := Width(l); w > m {
			m = w
		}
	}
	return m
}

// Truncate cuts s to at most w cells, keeping every escape sequence (so
// colours stay balanced) and ending with tail when it had to cut. The
// result is reset at the end if s carried any attribute.
func Truncate(s string, w int, tail string) string {
	if Width(s) <= w {
		return s
	}
	tw := Width(tail)
	if w < tw {
		tail, tw = "", 0
	}
	limit := w - tw
	var b strings.Builder
	cur, styled := 0, false
	for i := 0; i < len(s); {
		if n := escLen(s[i:]); n > 0 {
			b.WriteString(s[i : i+n])
			styled = true
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		rw := RuneWidth(r)
		if cur+rw > limit {
			break
		}
		b.WriteString(s[i : i+size])
		cur += rw
		i += size
	}
	b.WriteString(tail)
	if styled {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// PadRight pads s with spaces to w cells (and truncates if it is wider).
func PadRight(s string, w int) string {
	sw := Width(s)
	if sw > w {
		return Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-sw)
}

// PadLeft right-aligns s in w cells.
func PadLeft(s string, w int) string {
	sw := Width(s)
	if sw > w {
		return Truncate(s, w, "…")
	}
	return strings.Repeat(" ", w-sw) + s
}

// Center centres s in w cells.
func Center(s string, w int) string {
	sw := Width(s)
	if sw >= w {
		return Truncate(s, w, "…")
	}
	left := (w - sw) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", w-sw-left)
}

// Wrap breaks plain text into lines of at most w cells, at spaces where it
// can and mid-word where a word is longer than a line. Existing newlines
// are kept. Escape sequences are not expected in wrapped text.
func Wrap(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		// A line that already fits keeps its spacing, so aligned columns stay aligned.
		if para = strings.TrimRight(para, " "); para != "" && Width(para) <= w {
			out = append(out, para)
			continue
		}
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := ""
		for _, word := range words {
			for Width(word) > w {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				cut := Truncate(word, w, "")
				out = append(out, cut)
				word = word[len(cut):]
			}
			switch {
			case line == "":
				line = word
			case Width(line)+1+Width(word) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	return out
}

// Strip removes escape sequences.
func Strip(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if n := escLen(s[i:]); n > 0 {
			i += n
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// Sanitize makes untrusted text (tool output, server strings) safe to draw:
// escape sequences and control characters are removed, tabs expand to
// spaces. Without it a subprocess could move the cursor or retitle the
// window from inside a pane.
func Sanitize(s string) string {
	s = Strip(s)
	var b strings.Builder
	col := 0
	for _, r := range s {
		switch {
		case r == '\t':
			n := 4 - col%4
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case r == '\n':
			b.WriteRune(r)
			col = 0
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xA0) || r == 0x1b:
		default:
			b.WriteRune(r)
			col += RuneWidth(r)
		}
	}
	return b.String()
}
