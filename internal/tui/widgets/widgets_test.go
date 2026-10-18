package widgets

import (
	"strings"
	"testing"

	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
)

var th = DefaultTheme(style.NoColor, true)

func key(r rune) input.Key        { return input.Key{Code: input.Rune, Rune: r} }
func code(c input.Code) input.Key { return input.Key{Code: c} }

func TestListFilterAndMove(t *testing.T) {
	l := &List{Filterable: true, Items: []Item{{Title: "Benchmark"}, {Title: "Rent a GPU"}, {Title: "Scan weights"}}}
	l.HandleKey(code(input.Down))
	if it, _ := l.Selected(); it.Title != "Rent a GPU" {
		t.Errorf("selected %v", it)
	}
	l.HandleKey(key('/'))
	for _, r := range "scan" {
		l.HandleKey(key(r))
	}
	if v := l.Visible(); len(v) != 1 || v[0].Title != "Scan weights" {
		t.Errorf("filtered %v", v)
	}
	view := strings.Join(l.View(th, 30, 5, true), "\n")
	if !strings.Contains(view, "/scan") || !strings.Contains(view, "▸ Scan weights") {
		t.Errorf("view:\n%s", view)
	}
	l.HandleKey(code(input.Esc))
	if len(l.Visible()) != 3 {
		t.Error("Esc clears the filter")
	}
}

func TestTextInputEditingAndMask(t *testing.T) {
	ti := &TextInput{}
	for _, r := range "hello world" {
		ti.HandleKey(key(r))
	}
	ti.HandleKey(input.Key{Code: input.Rune, Rune: 'w', Ctrl: true})
	if ti.String() != "hello " {
		t.Errorf("ctrl+w: %q", ti.String())
	}
	ti.HandlePaste(input.Paste{Text: "a\x1b[2Jb\nc"})
	if ti.String() != "hello ab c" {
		t.Errorf("paste must be sanitised: %q", ti.String())
	}
	ti.Mask = true
	if v := ti.View(th, 20, false); strings.Contains(v, "hello") || !strings.Contains(v, "••••") {
		t.Errorf("mask: %q", v)
	}
}

func TestFormNavigationAndValues(t *testing.T) {
	f := &Form{Submit: "Run", Fields: []*Field{
		{Key: "endpoint", Label: "Endpoint"},
		{Key: "quick", Label: "Quick", Kind: ToggleField},
		{Key: "phase", Label: "Phases", Kind: ChoiceField, Choices: []string{"all", "decode"}},
	}}
	for _, r := range "http://h/v1" {
		f.HandleKey(key(r))
	}
	f.HandleKey(code(input.Tab))
	f.HandleKey(key(' '))
	f.HandleKey(code(input.Tab))
	f.HandleKey(code(input.Right))
	f.HandleKey(code(input.Tab))
	_, submitted := f.HandleKey(code(input.Enter))
	v := f.Values()
	if !submitted || v["endpoint"] != "http://h/v1" || v["quick"] != "true" || v["phase"] != "decode" {
		t.Errorf("submitted %v values %v", submitted, v)
	}
	for _, l := range f.View(th, 40, true) {
		if style.Width(l) > 40 {
			t.Errorf("line too wide: %q", l)
		}
	}
}

func TestLogPaneFollowsAndSanitises(t *testing.T) {
	l := NewLogPane(3)
	l.Write([]byte("one\ntwo\r\nthree\x1b[31m red\nfour\npart"))
	got := l.Lines()
	if strings.Join(got, "|") != "three red|four|part" && strings.Join(got, "|") != "two|three red|four|part" {
		t.Errorf("lines %q", got)
	}
	if v := l.View(th, 20, 2); strings.Join(v, "|") != "four|part" {
		t.Errorf("follow view %q", v)
	}
	l.HandleKey(code(input.Up))
	if v := l.View(th, 40, 2); !strings.Contains(v[1], "more below") {
		t.Errorf("scrolled view %q", v)
	}
}

func TestDialog(t *testing.T) {
	d := &Dialog{Title: "Rent?", Body: "This bills $2.50/hr.", Buttons: []string{"Rent", "Cancel"}, Choice: 1, Danger: true}
	if !d.HandleKey(code(input.Esc)) || d.Choice != 1 {
		t.Error("Esc cancels")
	}
	d.HandleKey(code(input.Left))
	if d.Choice != 0 {
		t.Error("left moves")
	}
	v := d.View(th, 60)
	for _, l := range v {
		if style.Width(l) != 60 {
			t.Errorf("width %d: %q", style.Width(l), l)
		}
	}
	if !strings.Contains(strings.Join(v, "\n"), "$2.50/hr") {
		t.Error("body missing")
	}
}

func TestTableAndHelp(t *testing.T) {
	tb := &Table{Columns: []Column{{Title: "When", Width: 10}, {Title: "URL"}}, Rows: [][]string{{"2026-10-17", "https://launch80.com/a/1"}}}
	v := tb.View(th, 40, 5, true)
	if len(v) != 2 || !strings.Contains(v[1], "launch80.com/a/1") {
		t.Errorf("table %q", v)
	}
	h := HelpLine(th, 30, Binding{"q", "quit"}, Binding{"enter", "open"}, Binding{"/", "filter"}, Binding{"?", "help"})
	if style.Width(h) > 30 || !strings.Contains(h, "q quit") {
		t.Errorf("help %q", h)
	}
}
