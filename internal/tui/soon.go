package tui

import (
	"strings"

	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

// Pages for work that is on the way. They say what is coming and where to
// follow it, so the sidebar shows the shape of L80 before each part ships.

const discordURL = "https://discord.gg/launch80"

var soon = map[screenID][]string{
	scrRecs: {
		"Tell L80 a model and what you care about (throughput, latency, cost, VRAM) and it recommends what to run it on: GPU, serving engine, quantization and settings, drawn from the BetterBench results the community has published and verified.",
		"",
		"• Hardware and engine picks ranked by measured results, not spec sheets",
		"• The cheapest Vast.ai offer that meets a target, ready to rent",
		"• The same answers as JSON, for your agents",
	},
	scrTrain: {
		"One place to make a model your own, and to prove the result:",
		"",
		"• Train and fine-tune (LoRA, QLoRA, full) on your own GPUs or a rented one",
		"• Tune serving parameters (batching, context, KV cache) against BetterBench",
		"• Quantize (GGUF, AWQ, GPTQ, FP8) and compare quality and speed side by side",
		"• Scan every artifact with pwdet before you ship it",
		"• Publish the before-and-after as one launch80 page",
	},
}

func soonView(c *Console, s screenID, w, h int) ([]string, []widgets.Binding) {
	t := c.th
	out := []string{t.R(t.Accent, "◆ Coming soon"), ""}
	for _, l := range soon[s] {
		out = append(out, hang(l, w)...)
	}
	out = append(out, "",
		t.R(t.Muted, "Want a say in how it works? Follow along and shape it in the Discord:"),
		"  "+t.R(t.Accent, discordURL))
	return style.Fit(out, w, h), []widgets.Binding{{Keys: "esc", Desc: "back"}}
}

// hang wraps a paragraph; a "• " bullet's later lines line up under its text.
func hang(l string, w int) []string {
	rest, ok := strings.CutPrefix(l, "• ")
	if !ok {
		return style.Wrap(l, w)
	}
	out := style.Wrap(rest, w-2)
	for i := range out {
		if i == 0 {
			out[i] = "• " + out[i]
		} else {
			out[i] = "  " + out[i]
		}
	}
	return out
}
