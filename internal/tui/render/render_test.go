package render

import (
	"bytes"
	"strings"
	"testing"
)

func TestOnlyChangedLinesAreWritten(t *testing.T) {
	var out bytes.Buffer
	r := New(&out, 10, 3)
	first, _ := r.Frame([]string{"one", "two", "three"})
	if !strings.Contains(first, "\x1b[2J") || !strings.Contains(first, "\x1b[3;1Hthree") {
		t.Errorf("first frame must paint everything: %q", first)
	}
	second, _ := r.Frame([]string{"one", "TWO", "three"})
	if strings.Contains(second, "one") || strings.Contains(second, "three") || !strings.Contains(second, "\x1b[2;1HTWO") {
		t.Errorf("second frame should only redraw line 2: %q", second)
	}
	third, _ := r.Frame([]string{"one", "TWO", "three"})
	if strings.Contains(third, "H") {
		t.Errorf("identical frame should write no lines: %q", third)
	}
	// Long lines are cut, never wrapped.
	r.Resize(5, 1)
	f, _ := r.Frame([]string{"abcdefghij"})
	if !strings.Contains(f, "abcde") || strings.Contains(f, "abcdef") {
		t.Errorf("cut: %q", f)
	}
}
