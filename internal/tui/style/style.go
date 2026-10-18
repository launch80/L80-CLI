package style

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Profile is how much colour the terminal can show.
type Profile int

const (
	NoColor Profile = iota
	ANSI16
	ANSI256
	TrueColor
)

// DetectProfile reads the conventional environment variables: NO_COLOR and
// TERM=dumb turn colour off, CLICOLOR_FORCE keeps it on, COLORTERM names
// truecolor, a TERM ending in 256color means 256 colours.
func DetectProfile(getenv func(string) string) Profile {
	if getenv == nil {
		getenv = os.Getenv
	}
	if getenv("NO_COLOR") != "" {
		return NoColor
	}
	term := getenv("TERM")
	if term == "dumb" && getenv("CLICOLOR_FORCE") == "" {
		return NoColor
	}
	switch strings.ToLower(getenv("COLORTERM")) {
	case "truecolor", "24bit":
		return TrueColor
	}
	if strings.HasSuffix(term, "256color") || strings.Contains(term, "kitty") || strings.Contains(term, "alacritty") ||
		strings.Contains(term, "wezterm") || getenv("TERM_PROGRAM") == "iTerm.app" {
		return ANSI256
	}
	return ANSI16
}

// Color is a hex colour ("#5fafff") or an ANSI index ("12"); "" is none.
type Color string

// sgr is the SGR parameter for this colour as foreground (or background).
func (c Color) sgr(p Profile, bg bool) string {
	if c == "" || p == NoColor {
		return ""
	}
	base := 38
	if bg {
		base = 48
	}
	if n, err := strconv.Atoi(string(c)); err == nil {
		if p == ANSI16 || n < 16 {
			n %= 16
			if n < 8 {
				return strconv.Itoa(base - 8 + n)
			}
			return strconv.Itoa(base + 52 + n - 8)
		}
		return fmt.Sprintf("%d;5;%d", base, n)
	}
	r, g, b, ok := parseHex(string(c))
	if !ok {
		return ""
	}
	switch p {
	case TrueColor:
		return fmt.Sprintf("%d;2;%d;%d;%d", base, r, g, b)
	case ANSI256:
		return fmt.Sprintf("%d;5;%d", base, to256(r, g, b))
	default:
		return Color(strconv.Itoa(to16(r, g, b))).sgr(ANSI16, bg)
	}
}

func parseHex(s string) (r, g, b int, ok bool) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff), true
}

// to256 maps to the 6x6x6 cube or the grey ramp, whichever is closer.
func to256(r, g, b int) int {
	q := func(v int) int {
		if v < 48 {
			return 0
		}
		if v < 115 {
			return 1
		}
		return (v - 35) / 40
	}
	cr, cg, cb := q(r), q(g), q(b)
	levels := []int{0, 95, 135, 175, 215, 255}
	cube := 16 + 36*cr + 6*cg + cb
	dc := sq(r-levels[cr]) + sq(g-levels[cg]) + sq(b-levels[cb])
	grey := (r + g + b) / 3
	gi := 0
	if grey > 8 {
		gi = (grey - 8) / 10
		if gi > 23 {
			gi = 23
		}
	}
	gv := 8 + gi*10
	dg := sq(r-gv) + sq(g-gv) + sq(b-gv)
	if dg < dc {
		return 232 + gi
	}
	return cube
}

func to16(r, g, b int) int {
	n := 0
	if r > 127 {
		n |= 1
	}
	if g > 127 {
		n |= 2
	}
	if b > 127 {
		n |= 4
	}
	if r > 191 || g > 191 || b > 191 {
		n += 8
	}
	return n
}

func sq(x int) int { return x * x }

// Style is a set of attributes; the zero value draws text unchanged. Values
// are copied, so a Style can be derived from another without changing it.
type Style struct {
	FG, BG                                  Color
	Bold, Faint, Italic, Underline, Reverse bool
}

// Render wraps s in the style's SGR sequence and a reset. Multi-line text is
// styled line by line so a border or join never carries colour across.
func (st Style) Render(p Profile, s string) string {
	var codes []string
	if p != NoColor {
		if st.Bold {
			codes = append(codes, "1")
		}
		if st.Faint {
			codes = append(codes, "2")
		}
		if st.Italic {
			codes = append(codes, "3")
		}
		if st.Underline {
			codes = append(codes, "4")
		}
		if st.Reverse {
			codes = append(codes, "7")
		}
		if c := st.FG.sgr(p, false); c != "" {
			codes = append(codes, c)
		}
		if c := st.BG.sgr(p, true); c != "" {
			codes = append(codes, c)
		}
	} else if st.Reverse {
		// Without colour, reverse video is still how a selection shows.
		codes = append(codes, "7")
	}
	if len(codes) == 0 || s == "" {
		return s
	}
	open := "\x1b[" + strings.Join(codes, ";") + "m"
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = open + l + "\x1b[0m"
	}
	return strings.Join(lines, "\n")
}

// Border is the set of characters a box is drawn with.
type Border struct{ TL, T, TR, L, R, BL, B, BR string }

var (
	Rounded = Border{"╭", "─", "╮", "│", "│", "╰", "─", "╯"}
	Normal  = Border{"┌", "─", "┐", "│", "│", "└", "─", "┘"}
	Thick   = Border{"┏", "━", "┓", "┃", "┃", "┗", "━", "┛"}
	ASCII   = Border{"+", "-", "+", "|", "|", "+", "-", "+"}
)

// Box draws content inside a border of the given outer width and height,
// with an optional title set into the top edge. Content lines are padded or
// cut to fit; missing lines are blank.
func Box(b Border, p Profile, edge Style, title string, content []string, w, h int) []string {
	if w < 2 || h < 2 {
		return nil
	}
	inner := w - 2
	top := b.T
	if title != "" && inner > 4 {
		t := Truncate(" "+title+" ", inner-2, "… ")
		top = b.T + t + strings.Repeat(b.T, max(0, inner-1-Width(t)))
	} else {
		top = strings.Repeat(b.T, inner)
	}
	out := make([]string, 0, h)
	out = append(out, edge.Render(p, b.TL+top+b.TR))
	for i := 0; i < h-2; i++ {
		line := ""
		if i < len(content) {
			line = content[i]
		}
		out = append(out, edge.Render(p, b.L)+PadRight(line, inner)+edge.Render(p, b.R))
	}
	out = append(out, edge.Render(p, b.BL+strings.Repeat(b.B, inner)+b.BR))
	return out
}

// JoinH places blocks side by side, top-aligned, each padded to its own
// widest line.
func JoinH(blocks ...[]string) []string {
	h := 0
	widths := make([]int, len(blocks))
	for i, bl := range blocks {
		if len(bl) > h {
			h = len(bl)
		}
		for _, l := range bl {
			if w := Width(l); w > widths[i] {
				widths[i] = w
			}
		}
	}
	out := make([]string, h)
	for row := 0; row < h; row++ {
		var b strings.Builder
		for i, bl := range blocks {
			line := ""
			if row < len(bl) {
				line = bl[row]
			}
			if i < len(blocks)-1 {
				line = PadRight(line, widths[i])
			}
			b.WriteString(line)
		}
		out[row] = b.String()
	}
	return out
}

// Fit pads or cuts lines to exactly w cells and h lines.
func Fit(lines []string, w, h int) []string {
	out := make([]string, h)
	for i := 0; i < h; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		out[i] = PadRight(l, w)
	}
	return out
}

// Place centres a block of lines in a w x h area.
func Place(lines []string, w, h int) []string {
	bw := 0
	for _, l := range lines {
		if x := Width(l); x > bw {
			bw = x
		}
	}
	top := max(0, (h-len(lines))/2)
	left := max(0, (w-bw)/2)
	out := make([]string, 0, h)
	for i := 0; i < top; i++ {
		out = append(out, "")
	}
	for _, l := range lines {
		out = append(out, strings.Repeat(" ", left)+l)
	}
	return Fit(out, w, h)
}
