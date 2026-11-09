package widgets

import (
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
)

// Check is one row of a Checklist.
type Check struct {
	Title, Desc string
	On          bool
	Value       any
}

// Checklist is a vertical list of toggles: arrows move, space toggles, a sets
// every row and none clears them.
type Checklist struct {
	Items  []Check
	Cursor int
	offset int
}

// Checked is the values of the rows that are on, in list order.
func (c *Checklist) Checked() []any {
	var out []any
	for _, it := range c.Items {
		if it.On {
			out = append(out, it.Value)
		}
	}
	return out
}

// HandleKey moves the cursor or toggles rows.
func (c *Checklist) HandleKey(k input.Key) bool {
	n := len(c.Items)
	switch {
	case k.Code == input.Up || (k.Code == input.Rune && k.Rune == 'k'):
		if c.Cursor > 0 {
			c.Cursor--
		}
	case k.Code == input.Down || (k.Code == input.Rune && k.Rune == 'j'):
		if c.Cursor < n-1 {
			c.Cursor++
		}
	case k.Code == input.Rune && k.Rune == ' ':
		if c.Cursor < n {
			c.Items[c.Cursor].On = !c.Items[c.Cursor].On
		}
	case k.Code == input.Rune && k.Rune == 'a':
		all := true
		for _, it := range c.Items {
			all = all && it.On
		}
		for i := range c.Items {
			c.Items[i].On = !all
		}
	default:
		return false
	}
	return true
}

// AtEdge reports whether moving in k's direction would leave the list, so a
// form can pass focus on instead.
func (c *Checklist) AtEdge(k input.Key) bool {
	return (k.Code == input.Up && c.Cursor == 0) || (k.Code == input.Down && c.Cursor >= len(c.Items)-1)
}

// View draws the list in w x h, one row per item with its description to the right.
func (c *Checklist) View(t Theme, w, h int, focused bool) []string {
	if len(c.Items) == 0 {
		return []string{t.R(t.Muted, "  nothing to show")}
	}
	rows := max(1, h)
	if c.Cursor < c.offset {
		c.offset = c.Cursor
	}
	if c.Cursor >= c.offset+rows {
		c.offset = c.Cursor - rows + 1
	}
	tw := 0
	for _, it := range c.Items {
		tw = max(tw, style.Width(it.Title))
	}
	var out []string
	for i := c.offset; i < len(c.Items) && i < c.offset+rows; i++ {
		it := c.Items[i]
		box := "[ ] "
		if it.On {
			box = "[x] "
		}
		cur := "  "
		if focused && i == c.Cursor {
			cur = "▸ "
		}
		title := style.PadRight(style.Sanitize(it.Title), tw)
		line := cur + box + title
		if focused && i == c.Cursor {
			line = t.R(t.Selected, line)
		} else if it.On {
			line = t.R(t.Text, line)
		} else {
			line = t.R(t.Muted, line)
		}
		if it.Desc != "" {
			line += "  " + t.R(t.Muted, style.Sanitize(it.Desc))
		}
		out = append(out, style.Truncate(line, w, "…"))
	}
	if more := len(c.Items) - (c.offset + rows); more > 0 && len(out) > 0 {
		out[len(out)-1] = t.R(t.Muted, style.PadRight("  … "+itoa(more)+" more", w))
	}
	return out
}
