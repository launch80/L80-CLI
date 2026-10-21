package style

import (
	"strings"
	"testing"
)

func TestWidth(t *testing.T) {
	cases := map[string]int{
		"abc": 3, "日本語": 6, "\x1b[1;31mred\x1b[0m": 3, "é": 1, "👍": 2, "👨‍👩‍👧": 2, "": 0,
		"\x1b]0;title\x07x": 1, "✅ ok": 5,
	}
	for s, want := range cases {
		if got := Width(s); got != want {
			t.Errorf("Width(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestTruncateKeepsEscapesAndWidth(t *testing.T) {
	s := "\x1b[32mhello 世界\x1b[0m"
	got := Truncate(s, 8, "…")
	if Width(got) > 8 || !strings.HasPrefix(got, "\x1b[32mhello") || !strings.HasSuffix(got, "…\x1b[0m") {
		t.Errorf("got %q (w=%d)", got, Width(got))
	}
	if Truncate("abc", 5, "…") != "abc" {
		t.Error("short strings are untouched")
	}
	// A wide rune that would straddle the limit is dropped, not split.
	if got := Truncate("ab日", 3, ""); got != "ab" {
		t.Errorf("got %q", got)
	}
}

func TestPadWrapCenter(t *testing.T) {
	if PadRight("ab", 4) != "ab  " || PadLeft("ab", 4) != "  ab" || Center("ab", 6) != "  ab  " {
		t.Error("padding")
	}
	got := Wrap("the quick brown fox jumps", 10)
	if strings.Join(got, "|") != "the quick|brown fox|jumps" {
		t.Errorf("wrap = %q", got)
	}
	if got := Wrap("abcdefghijkl", 5); strings.Join(got, "|") != "abcde|fghij|kl" {
		t.Errorf("long word = %q", got)
	}
}

func TestSanitizeRemovesControl(t *testing.T) {
	in := "ok\x1b[2J\x1b]0;pwned\x07\tend\r\x07"
	if got := Sanitize(in); got != "ok  end" {
		t.Errorf("got %q", got)
	}
}

func TestRenderAndProfiles(t *testing.T) {
	st := Style{FG: "#5fafff", Bold: true}
	if got := st.Render(NoColor, "x"); got != "x" {
		t.Errorf("no color: %q", got)
	}
	if got := st.Render(TrueColor, "x"); got != "\x1b[1;38;2;95;175;255mx\x1b[0m" {
		t.Errorf("truecolor: %q", got)
	}
	if got := st.Render(ANSI256, "x"); got != "\x1b[1;38;5;75mx\x1b[0m" {
		t.Errorf("256: %q", got)
	}
	if got := (Style{FG: "9"}).Render(ANSI16, "x"); got != "\x1b[91mx\x1b[0m" {
		t.Errorf("16: %q", got)
	}
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if DetectProfile(env(map[string]string{"NO_COLOR": "1", "COLORTERM": "truecolor"})) != NoColor ||
		DetectProfile(env(map[string]string{"TERM": "xterm-256color"})) != ANSI256 ||
		DetectProfile(env(map[string]string{"TERM": "xterm", "COLORTERM": "truecolor"})) != TrueColor ||
		DetectProfile(env(map[string]string{"TERM": "dumb"})) != NoColor {
		t.Error("profile detection")
	}
}

func TestBoxAndJoin(t *testing.T) {
	b := Box(Rounded, NoColor, Style{}, "Tools", []string{"betterbench ok", "pwdet missing"}, 20, 4)
	want := []string{"╭─ Tools ──────────╮", "│betterbench ok    │", "│pwdet missing     │", "╰──────────────────╯"}
	if strings.Join(b, "\n") != strings.Join(want, "\n") {
		t.Errorf("box:\n%s", strings.Join(b, "\n"))
	}
	for _, l := range b {
		if Width(l) != 20 {
			t.Errorf("line width %d: %q", Width(l), l)
		}
	}
	j := JoinH([]string{"ab", "c"}, []string{"1", "2", "3"})
	if strings.Join(j, "|") != "ab1|c 2|  3" {
		t.Errorf("join = %q", j)
	}
}

func TestWrapKeepsColumns(t *testing.T) {
	got := Wrap("figure      published\nCombined decode   79.8 t/s\n"+strings.Repeat("word ", 8), 30)
	want := []string{"figure      published", "Combined decode   79.8 t/s", "word word word word word word", "word word"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Wrap = %q", got)
	}
}
