// Package widgets holds the TUI's reusable components: list, table, text
// input, form, log pane, spinner, progress bar, help line and dialog.
//
// They are modelled on the bubbles components (MIT, Copyright (c) 2020-2026
// Charmbracelet, Inc.; see THIRD_PARTY_NOTICES.md) but simpler: each one is a
// value with a HandleKey method that reports whether it used the key, and a
// View that returns lines of an exact width.
package widgets

import (
	"github.com/launch80/L80-CLI/internal/tui/style"
)

// Theme is the palette every widget draws with.
type Theme struct {
	P                                                  style.Profile
	Border                                             style.Border
	Accent, Muted, Text, Selected, OK, Warn, Bad, Edge style.Style
	Title, Key                                         style.Style
}

// DefaultTheme picks colours for the profile and ASCII borders where
// line-drawing characters may not render.
func DefaultTheme(p style.Profile, unicodeOK bool) Theme {
	b := style.Rounded
	if !unicodeOK {
		b = style.ASCII
	}
	return Theme{
		P: p, Border: b,
		Accent:   style.Style{FG: "#ff9a52", Bold: true},
		Muted:    style.Style{FG: "#8a8a8a"},
		Text:     style.Style{},
		Selected: style.Style{FG: "#ffffff", BG: "#3a2415", Bold: true, Reverse: p == style.NoColor},
		OK:       style.Style{FG: "#5fd75f"},
		Warn:     style.Style{FG: "#ffaf00"},
		Bad:      style.Style{FG: "#ff5f5f", Bold: true},
		Edge:     style.Style{FG: "#4e4e4e"},
		Title:    style.Style{FG: "#ffffff", Bold: true},
		Key:      style.Style{FG: "#ff9a52", Bold: true},
	}
}

func (t Theme) R(s style.Style, text string) string { return s.Render(t.P, text) }
