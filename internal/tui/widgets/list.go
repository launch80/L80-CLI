package widgets

import (
	"strings"

	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
)

// Item is one row of a List.
type Item struct {
	Title, Desc string
	Value       any
}

// List is a vertical menu with an optional "/" substring filter.
type List struct {
	Items      []Item
	Cursor     int
	offset     int
	Filter     string
	filtering  bool
	Filterable bool
}

// Visible is the items that match the filter.
func (l *List) Visible() []Item {
	if l.Filter == "" {
		return l.Items
	}
	f := strings.ToLower(l.Filter)
	var out []Item
	for _, it := range l.Items {
		if strings.Contains(strings.ToLower(it.Title+" "+it.Desc), f) {
			out = append(out, it)
		}
	}
	return out
}

// Selected is the item under the cursor, if any.
func (l *List) Selected() (Item, bool) {
	v := l.Visible()
	if l.Cursor < 0 || l.Cursor >= len(v) {
		return Item{}, false
	}
	return v[l.Cursor], true
}

// Filtering reports whether keystrokes are going to the filter.
func (l *List) Filtering() bool { return l.filtering }

// HandleKey moves the cursor or edits the filter.
func (l *List) HandleKey(k input.Key) bool {
	n := len(l.Visible())
	if l.filtering {
		switch k.Code {
		case input.Esc:
			l.filtering, l.Filter, l.Cursor = false, "", 0
		case input.Enter:
			l.filtering = false
		case input.Backspace:
			if r := []rune(l.Filter); len(r) > 0 {
				l.Filter = string(r[:len(r)-1])
			}
			l.Cursor = 0
		case input.Rune:
			if !k.Ctrl && !k.Alt {
				l.Filter += string(k.Rune)
				l.Cursor = 0
			}
		default:
			return l.move(k, n)
		}
		return true
	}
	if l.Filterable && k.Code == input.Rune && k.Rune == '/' && !k.Ctrl {
		l.filtering = true
		return true
	}
	return l.move(k, n)
}

func (l *List) move(k input.Key, n int) bool {
	switch {
	case k.Code == input.Up || (k.Code == input.Rune && k.Rune == 'k' && !l.filtering):
		if l.Cursor > 0 {
			l.Cursor--
		}
	case k.Code == input.Down || (k.Code == input.Rune && k.Rune == 'j' && !l.filtering):
		if l.Cursor < n-1 {
			l.Cursor++
		}
	case k.Code == input.Home:
		l.Cursor = 0
	case k.Code == input.End:
		l.Cursor = max(0, n-1)
	case k.Code == input.PageUp:
		l.Cursor = max(0, l.Cursor-10)
	case k.Code == input.PageDown:
		l.Cursor = max(0, min(n-1, l.Cursor+10))
	default:
		return false
	}
	return true
}

// View draws the list in w x h. Items with a description take two lines.
func (l *List) View(t Theme, w, h int, focused bool) []string {
	items := l.Visible()
	var out []string
	if l.filtering || l.Filter != "" {
		cur := ""
		if l.filtering {
			cur = "▏"
		}
		out = append(out, t.R(t.Accent, "/")+style.Sanitize(l.Filter)+cur)
		h--
	}
	if len(items) == 0 {
		return append(out, t.R(t.Muted, "  nothing to show"))
	}
	per := 1
	for _, it := range items {
		if it.Desc != "" {
			per = 2
			break
		}
	}
	rows := max(1, h/per)
	if l.Cursor < l.offset {
		l.offset = l.Cursor
	}
	if l.Cursor >= l.offset+rows {
		l.offset = l.Cursor - rows + 1
	}
	for i := l.offset; i < len(items) && i < l.offset+rows; i++ {
		it := items[i]
		title := style.PadRight("  "+style.Sanitize(it.Title), w)
		if i == l.Cursor {
			title = style.PadRight("▸ "+style.Sanitize(it.Title), w)
			if focused {
				title = t.R(t.Selected, title)
			} else {
				title = t.R(t.Accent, title)
			}
		}
		out = append(out, title)
		if per == 2 {
			out = append(out, t.R(t.Muted, style.PadRight("    "+style.Sanitize(it.Desc), w)))
		}
	}
	if more := len(items) - (l.offset + rows); more > 0 && len(out) > 0 {
		out[len(out)-1] = t.R(t.Muted, style.PadRight("  … "+itoa(more)+" more", w))
	}
	return out
}
