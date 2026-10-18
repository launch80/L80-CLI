package widgets

import (
	"strconv"
	"strings"

	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
)

// Column is a table column; Width 0 takes the remaining space.
type Column struct {
	Title string
	Width int
	Right bool
}

// Table is a scrolling grid with a selectable row.
type Table struct {
	Columns []Column
	Rows    [][]string
	Cursor  int
	offset  int
}

func (tb *Table) HandleKey(k input.Key) bool {
	l := List{Items: make([]Item, len(tb.Rows)), Cursor: tb.Cursor}
	ok := l.move(k, len(tb.Rows))
	tb.Cursor = l.Cursor
	return ok
}

// Row is the selected row, if any.
func (tb *Table) Row() ([]string, bool) {
	if tb.Cursor < 0 || tb.Cursor >= len(tb.Rows) {
		return nil, false
	}
	return tb.Rows[tb.Cursor], true
}

func (tb *Table) widths(w int) []int {
	ws := make([]int, len(tb.Columns))
	used, flex := 0, -1
	for i, c := range tb.Columns {
		if c.Width == 0 {
			flex = i
			continue
		}
		ws[i] = c.Width
		used += c.Width + 1
	}
	if flex >= 0 {
		ws[flex] = max(4, w-used-1)
	}
	return ws
}

func (tb *Table) line(cells []string, ws []int) string {
	parts := make([]string, len(ws))
	for i, cw := range ws {
		c := ""
		if i < len(cells) {
			c = style.Sanitize(cells[i])
		}
		if tb.Columns[i].Right {
			parts[i] = style.PadLeft(c, cw)
		} else {
			parts[i] = style.PadRight(c, cw)
		}
	}
	return strings.Join(parts, " ")
}

// View draws the header and as many rows as fit.
func (tb *Table) View(t Theme, w, h int, focused bool) []string {
	ws := tb.widths(w)
	head := make([]string, len(tb.Columns))
	for i, c := range tb.Columns {
		head[i] = c.Title
	}
	out := []string{t.R(t.Muted, style.PadRight(tb.line(head, ws), w))}
	rows := max(1, h-1)
	if len(tb.Rows) == 0 {
		return append(out, t.R(t.Muted, "  nothing yet"))
	}
	if tb.Cursor < tb.offset {
		tb.offset = tb.Cursor
	}
	if tb.Cursor >= tb.offset+rows {
		tb.offset = tb.Cursor - rows + 1
	}
	for i := tb.offset; i < len(tb.Rows) && i < tb.offset+rows; i++ {
		l := style.PadRight(tb.line(tb.Rows[i], ws), w)
		if i == tb.Cursor && focused {
			l = t.R(t.Selected, l)
		}
		out = append(out, l)
	}
	return out
}

func itoa(i int) string { return strconv.Itoa(i) }
