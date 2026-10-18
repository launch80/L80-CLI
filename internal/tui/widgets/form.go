package widgets

import (
	"strings"

	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
)

// FieldKind is what a form field holds.
type FieldKind int

const (
	TextField FieldKind = iota
	ToggleField
	ChoiceField
)

// Field is one labelled form row.
type Field struct {
	Key, Label, Help string
	Kind             FieldKind
	Input            TextInput
	On               bool
	Choices          []string
	Choice           int
}

// Value is the field's value as a string ("true"/"false" for a toggle).
func (f *Field) Value() string {
	switch f.Kind {
	case ToggleField:
		if f.On {
			return "true"
		}
		return "false"
	case ChoiceField:
		if f.Choice < len(f.Choices) {
			return f.Choices[f.Choice]
		}
		return ""
	}
	return strings.TrimSpace(f.Input.String())
}

// Form is a list of fields with one in focus, plus a submit button below.
type Form struct {
	Fields []*Field
	Focus  int // len(Fields) is the submit button
	Submit string
}

// Values maps each field key to its value.
func (f *Form) Values() map[string]string {
	out := map[string]string{}
	for _, fl := range f.Fields {
		out[fl.Key] = fl.Value()
	}
	return out
}

// Get is one field's value.
func (f *Form) Get(key string) string {
	for _, fl := range f.Fields {
		if fl.Key == key {
			return fl.Value()
		}
	}
	return ""
}

// HandleKey moves focus with Tab/arrows and edits the focused field. It
// returns submitted=true when Enter is pressed on the submit button (or on
// the last text field).
func (f *Form) HandleKey(k input.Key) (used, submitted bool) {
	n := len(f.Fields)
	switch k.Code {
	case input.Tab, input.Down:
		f.Focus = (f.Focus + 1) % (n + 1)
		return true, false
	case input.Backtab, input.Up:
		f.Focus = (f.Focus + n) % (n + 1)
		return true, false
	case input.Enter:
		if f.Focus == n {
			return true, true
		}
		f.Focus++
		return true, false
	}
	if f.Focus == n {
		return false, false
	}
	fl := f.Fields[f.Focus]
	switch fl.Kind {
	case ToggleField:
		if k.Code == input.Rune && k.Rune == ' ' || k.Code == input.Left || k.Code == input.Right {
			fl.On = !fl.On
			return true, false
		}
	case ChoiceField:
		switch {
		case k.Code == input.Left:
			fl.Choice = (fl.Choice + len(fl.Choices) - 1) % len(fl.Choices)
			return true, false
		case k.Code == input.Right || (k.Code == input.Rune && k.Rune == ' '):
			fl.Choice = (fl.Choice + 1) % len(fl.Choices)
			return true, false
		}
	default:
		return fl.Input.HandleKey(k), false
	}
	return false, false
}

// HandlePaste pastes into the focused text field.
func (f *Form) HandlePaste(p input.Paste) {
	if f.Focus < len(f.Fields) && f.Fields[f.Focus].Kind == TextField {
		f.Fields[f.Focus].Input.HandlePaste(p)
	}
}

// View draws one row per field: label, value, and the focused field's help.
func (f *Form) View(t Theme, w int, focused bool) []string {
	lw := 0
	for _, fl := range f.Fields {
		lw = max(lw, style.Width(fl.Label))
	}
	lw = min(lw+2, w/3)
	var out []string
	var help string
	for i, fl := range f.Fields {
		on := focused && f.Focus == i
		label := style.PadRight(fl.Label, lw)
		if on {
			label = t.R(t.Accent, label)
			help = fl.Help
		} else {
			label = t.R(t.Muted, label)
		}
		vw := w - lw
		var val string
		switch fl.Kind {
		case ToggleField:
			box := "[ ]"
			if fl.On {
				box = "[x]"
			}
			val = box
			if on {
				val = t.R(t.Selected, box) + t.R(t.Muted, "  space toggles")
			}
		case ChoiceField:
			var parts []string
			for j, c := range fl.Choices {
				if j == fl.Choice {
					c = "‹" + c + "›"
					if on {
						c = t.R(t.Selected, c)
					} else {
						c = t.R(t.Accent, c)
					}
				}
				parts = append(parts, c)
			}
			val = strings.Join(parts, " ")
		default:
			val = fl.Input.View(t, vw, on)
		}
		out = append(out, style.PadRight(label+val, w))
	}
	btn := "[ " + f.Submit + " ]"
	if focused && f.Focus == len(f.Fields) {
		btn = t.R(t.Selected, btn)
		help = "Enter to " + strings.ToLower(f.Submit)
	} else {
		btn = t.R(t.Accent, btn)
	}
	out = append(out, "", strings.Repeat(" ", lw)+btn)
	if help != "" {
		out = append(out, "")
		for _, l := range style.Wrap(help, w) {
			out = append(out, t.R(t.Muted, l))
		}
	}
	return out
}
