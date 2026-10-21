package tui

import (
	"strconv"
	"strings"

	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

const supporterURL = "https://www.launch80.com/supporter"

// story is Launch80 in brief. Each string is a paragraph, wrapped to the pane.
var story = []string{
	"Launch80 started in late 2025 as a venture studio: bring us a SaaS idea, and we would build it and invest seed money to take it to market, in exchange for a small stake. But people were wary of handing their ideas to strangers, and it never caught on.",
	"In February 2026 we opened the Launch80 Discord for the new wave of builders shipping with AI, the vibe coders, hoping to help them past their blockers. Most of them weren't looking to invest the time or money a real build takes, so in April we pivoted again, to a more experienced audience, and began sharing some of our projects.",
	"With Mac Studios left over from local AI consulting and a Radeon R9700 rig, we started on RAG, inference engineering and model benchmarking. Brian posted the work on Reddit, and the Discord filled with sharp engineers working on their own problems and helping each other with theirs.",
	"We aren't building another AI SaaS app, and we aren't building an org chart. Launch80 is a registered business for payment and legal reasons, but its charter is deliberately blank. The Supporter role is closer to patronage than to a product: it keeps out spam, and it's how we switch features on.",
	"What Launch80 is, and always has been, is a place where engineers help other builders for the sake of helping: a mostly ungoverned community, closest in spirit to Stack Overflow, chasing technical curiosity on open-source principles. Its work has moved inference engineering forward for open-source developers around the world, and L80 is the toolbox that came out of it.",
}

type aboutScreen struct{ off int }

func (a *aboutScreen) lines(c *Console, w int) []string {
	t := c.th
	var out []string
	section := func(s string) { out = append(out, "", t.R(t.Title, s)) }

	out = append(out, t.R(t.Title, "Our story"))
	for i, p := range story {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, style.Wrap(p, w)...)
	}

	section("Community")
	out = append(out, "  Ask questions, share runs and help others: "+t.R(t.Accent, discordURL))
	section("Support us")
	out = append(out, "  If L80 helps you, become a Supporter: "+t.R(t.Accent, supporterURL))

	section("Command directory")
	out = append(out, t.R(t.Muted, "  Everything in this console is also a command. `L80 <command> --help` has the details."))
	for _, l := range strings.Split(strings.TrimRight(c.d.Usage, "\n"), "\n") {
		out = append(out, usageLine(c, l, w)...)
	}
	return out
}

// usageLine fits one line of `L80 help`. A command and its description are
// separated by a run of spaces; when both do not fit, the description moves
// under the command.
func usageLine(c *Console, l string, w int) []string {
	t := c.th
	trimmed := strings.TrimSpace(l)
	switch {
	case trimmed == "":
		return []string{""}
	case !strings.HasPrefix(l, " "):
		return []string{t.R(t.Title, trimmed)}
	case strings.HasSuffix(trimmed, ":"):
		return []string{t.R(t.Accent, l)}
	case style.Width(l) <= w:
		return []string{l}
	}
	indent := len(l) - len(strings.TrimLeft(l, " "))
	if i := strings.Index(l[indent:], "   "); i > 0 {
		cmd, desc := l[:indent+i], strings.TrimSpace(l[indent+i:])
		out := []string{style.Truncate(cmd, w, "…")}
		for _, d := range style.Wrap(desc, max(10, w-indent-4)) {
			out = append(out, t.R(t.Muted, strings.Repeat(" ", indent+4)+d))
		}
		return out
	}
	return []string{style.Truncate(l, w, "…")}
}

func (a *aboutScreen) update(c *Console, m Msg) Cmd {
	k, ok := m.(KeyMsg)
	if !ok {
		return nil
	}
	switch {
	case isBack(k):
		c.scr = scrHome
	case k.Code == input.Up || (k.Code == input.Rune && k.Rune == 'k'):
		a.off--
	case k.Code == input.Down || (k.Code == input.Rune && k.Rune == 'j'):
		a.off++
	case k.Code == input.PageUp:
		a.off -= 10
	case k.Code == input.PageDown, k.Code == input.Rune && k.Rune == ' ':
		a.off += 10
	case k.Code == input.Home || (k.Code == input.Rune && k.Rune == 'g'):
		a.off = 0
	case k.Code == input.End || (k.Code == input.Rune && k.Rune == 'G'):
		a.off = 1 << 20 // clamped in view
	}
	a.off = max(0, a.off)
	return nil
}

func (a *aboutScreen) view(c *Console, w, h int) ([]string, []widgets.Binding) {
	all := a.lines(c, w)
	a.off = max(0, min(a.off, len(all)-h))
	shown := all[a.off:]
	if more := len(all) - a.off - h; more > 0 && h > 0 {
		shown = append(append([]string{}, shown[:h-1]...), c.th.R(c.th.Muted, "  ↓ "+strconv.Itoa(more+1)+" more lines (↓ pgdn end)"))
	}
	return style.Fit(shown, w, h), []widgets.Binding{{Keys: "↑↓ pgup pgdn", Desc: "scroll"}, {Keys: "esc", Desc: "back"}}
}
