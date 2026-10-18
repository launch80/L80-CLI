package tui

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	l80skills "github.com/launch80/L80-CLI"
	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/skillinstall"
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

// Onboarding is a short stepper for someone new to L80. It opens by itself on
// the first run; the sidebar item and `L80 onboard` run it again. Finishing or
// skipping both record it, so it never asks twice.

const (
	obWelcome = iota
	obMachine
	obToken
	obGoal
	obAgents
	obDone
	obSteps
)

var obTitles = [obSteps]string{"Welcome", "This machine", "Publishing token", "What brings you here", "Your AI agents", "All set"}

type agentChoice struct {
	t        skillinstall.Target
	dir      string
	detected bool
	on       bool
	result   string
	bad      bool
}

type onboardScreen struct {
	step      int
	tools     []app.ToolStatus
	loaded    bool
	token     widgets.TextInput
	tokenMsg  string
	tokenBad  bool
	goals     widgets.List
	agents    []agentChoice
	cur       int
	installed bool
}

type onboardTools struct{ rows []app.ToolStatus }

// startOnboarding opens onboarding with the keyboard, on its sidebar item.
func (c *Console) startOnboarding() Cmd {
	c.sel = max(0, slices.Index(c.side, scrOnboard))
	c.paneFocus = true
	return c.open(scrOnboard)
}

func (o *onboardScreen) start(c *Console) Cmd {
	*o = onboardScreen{}
	o.token = widgets.TextInput{Mask: true, Placeholder: "paste a token: L80_…"}
	o.goals = widgets.List{Items: []widgets.Item{
		{Title: "Benchmark a model I serve", Desc: "BetterBench against your endpoint, then publish the page", Value: scrBench},
		{Title: "Benchmark on a rented GPU", Desc: "Rent a Vast.ai box, serve, measure, publish, tear down", Value: scrVast},
		{Title: "Check a model for tampering", Desc: "Scan weights with the Poisoned Weights Detector", Value: scrPwdet},
		{Title: "Watch my GPUs", Desc: "Power, temperatures, VRAM and live inference telemetry", Value: scrDash},
		{Title: "Verify other people's runs", Desc: "Reproduce a published benchmark and review it", Value: scrVerify},
		{Title: "Just look around", Desc: "Start on Home", Value: scrHome},
	}}
	for _, t := range skillinstall.Targets {
		dir, err := t.Dir()
		if err != nil {
			continue
		}
		d := t.Detected()
		o.agents = append(o.agents, agentChoice{t: t, dir: dir, detected: d, on: d})
	}
	return func() Msg {
		rows, _ := app.ToolsCheck(nil, false)
		return onboardTools{rows}
	}
}

func (o *onboardScreen) update(c *Console, m Msg) Cmd {
	switch v := m.(type) {
	case onboardTools:
		o.tools, o.loaded = v.rows, true
	case PasteMsg:
		if o.step == obToken {
			o.token.HandlePaste(v)
		}
	case KeyMsg:
		return o.key(c, v)
	}
	return nil
}

func (o *onboardScreen) key(c *Console, k KeyMsg) Cmd {
	typing := o.step == obToken && !c.d.Config.HasToken()
	switch {
	case k.Code == input.Esc || (!typing && k.Code == input.Rune && k.Rune == 's'):
		return o.finish(c, true)
	case k.Code == input.Backtab || (!typing && (k.Code == input.Left || (k.Code == input.Rune && k.Rune == 'b'))):
		o.step = max(0, o.step-1)
		return nil
	}

	switch o.step {
	case obToken:
		if typing && k.Code != input.Enter && k.Code != input.Tab {
			o.token.HandleKey(k)
			return nil
		}
		if typing && strings.TrimSpace(o.token.String()) != "" && !o.saveToken(c) {
			return nil
		}
	case obGoal:
		if o.goals.HandleKey(k) {
			return nil
		}
	case obAgents:
		switch {
		case k.Code == input.Up || (k.Code == input.Rune && k.Rune == 'k'):
			o.cur = max(0, o.cur-1)
			return nil
		case k.Code == input.Down || (k.Code == input.Rune && k.Rune == 'j'):
			o.cur = min(len(o.agents)-1, o.cur+1)
			return nil
		case k.Code == input.Rune && k.Rune == ' ' && o.cur < len(o.agents):
			o.agents[o.cur].on = !o.agents[o.cur].on
			return nil
		case (k.Code == input.Enter || k.Code == input.Tab) && !o.installed &&
			slices.ContainsFunc(o.agents, func(a agentChoice) bool { return a.on }):
			o.install()
			return nil // stay to show what happened; the next enter moves on
		}
	case obDone:
		if k.Code == input.Enter {
			return o.finish(c, false)
		}
		return nil
	}

	if k.Code == input.Enter || k.Code == input.Tab || (k.Code == input.Right && !typing) {
		o.step++
		if o.step == obDone {
			if err := config.MarkOnboarded(false); err != nil {
				c.setFlash("could not record onboarding: "+err.Error(), true)
			}
		}
	}
	return nil
}

func (o *onboardScreen) saveToken(c *Console) bool {
	tok := strings.TrimSpace(o.token.String())
	if config.KeyIDOf(tok) == "" {
		o.tokenMsg, o.tokenBad = "That does not look like a launch80 token (L80_<key>_<secret>).", true
		return false
	}
	if err := config.SaveCredentials(tok); err != nil {
		o.tokenMsg, o.tokenBad = "Could not save it: "+err.Error(), true
		return false
	}
	cfg := &c.d.Config
	cfg.Token, cfg.KeyID, cfg.TokenSource = tok, config.KeyIDOf(tok), config.SourceFile
	c.d.Client = api.NewClient(cfg.BaseURL, tok, c.d.Version)
	o.token.Set("")
	o.tokenMsg, o.tokenBad = "Saved to "+config.CredentialsPath()+" (readable only by you).", false
	return true
}

// install puts every bundled skill into each chosen harness. A copy that is
// already there is refreshed; a --dev symlink is left alone.
func (o *onboardScreen) install() {
	o.installed = true
	for i := range o.agents {
		a := &o.agents[i]
		if !a.on {
			continue
		}
		var failed []string
		for _, n := range l80skills.SkillNames {
			dest := filepath.Join(a.dir, n)
			if skillinstall.Status(n, dest) == skillinstall.Linked {
				continue
			}
			if err := skillinstall.Install(n, dest, skillinstall.Opts{Force: true}); err != nil && !errors.Is(err, skillinstall.ErrExists) {
				failed = append(failed, n+": "+err.Error())
			}
		}
		if len(failed) > 0 {
			a.result, a.bad = strings.Join(failed, "; "), true
		} else {
			a.result = fmt.Sprintf("installed %d skills", len(l80skills.SkillNames))
		}
	}
}

// finish records onboarding and lands on the chosen screen (Home when skipped).
func (o *onboardScreen) finish(c *Console, skipped bool) Cmd {
	if err := config.MarkOnboarded(skipped); err != nil {
		c.setFlash("could not record onboarding: "+err.Error(), true)
	} else if skipped {
		c.setFlash("Onboarding skipped. Run it any time from the sidebar or with `L80 onboard`.", false)
	}
	dest := scrHome
	if it, ok := o.goals.Selected(); ok && !skipped {
		dest = it.Value.(screenID)
	}
	c.sel = max(0, slices.Index(c.side, dest))
	c.paneFocus = false
	return c.open(dest)
}

func (o *onboardScreen) view(c *Console, w, h int) ([]string, []widgets.Binding) {
	t := c.th
	var dots string
	for i := range obSteps {
		switch {
		case i == o.step:
			dots += t.R(t.Accent, "●")
		case i < o.step:
			dots += t.R(t.OK, "●")
		default:
			dots += t.R(t.Muted, "○")
		}
	}
	out := []string{dots + t.R(t.Muted, fmt.Sprintf("  Step %d of %d · ", o.step+1, obSteps)) + t.R(t.Title, obTitles[o.step]), ""}
	para := func(s string) { out = append(out, style.Wrap(s, w)...) }
	keys := []widgets.Binding{{Keys: "enter", Desc: "next"}, {Keys: "b", Desc: "back"}, {Keys: "esc", Desc: "skip onboarding"}}

	switch o.step {
	case obWelcome:
		para("L80 is the launch80 toolbox for people who run models: benchmark an inference endpoint with BetterBench, rent a GPU to test on, scan weights for tampering, watch your GPUs, and publish results as pages anyone can open.")
		out = append(out, "")
		para("Every tool is free and runs on this machine. Only publishing a page needs a launch80 token.")
		out = append(out, "")
		para("This takes about a minute: check this machine, set up publishing, pick where to start, and teach your AI agents to use L80 for you.")
		keys = keys[:1]
		keys = append(keys, widgets.Binding{Keys: "esc", Desc: "skip onboarding"})
	case obMachine:
		if !o.loaded {
			out = append(out, t.R(t.Muted, widgets.Spin(c.tick)+" checking the tools…"))
			break
		}
		ready := true
		for _, ts := range o.tools {
			out = append(out, kv(t, ts.Name, toolState(t, ts)))
			ready = ready && ts.Status == "ok"
		}
		out = append(out, "")
		if ready {
			para("Everything resolves. Tools download on first use and are cached after that.")
		} else {
			para("Tools that are not ready are fetched by uv the first time you use them. If uv is missing, install it:")
			out = append(out, "  "+t.R(t.Accent, "curl -LsSf https://astral.sh/uv/install.sh | sh"))
			for _, ts := range o.tools {
				if ts.Remedy != "" {
					out = append(out, "")
					para(ts.Name + ": " + ts.Remedy)
					break
				}
			}
		}
	case obToken:
		if c.d.Config.HasToken() {
			out = append(out, kv(t, "token", t.R(t.OK, masked(c.d.Config))+t.R(t.Muted, " from "+string(c.d.Config.TokenSource))))
			out = append(out, "")
			para("You can publish. Pages are public, and a page stays self-reported until launch80 verifies it.")
		} else {
			para("Publishing a page to launch80 needs a token. Get one from your account on www.launch80.com, then paste it here; it is saved to " + config.CredentialsPath() + ", readable only by you. Or press enter to skip: every tool works without one, and L80_TOKEN works too.")
			out = append(out, "", "  "+o.token.View(t, min(w-4, 60), true))
			keys = []widgets.Binding{{Keys: "enter", Desc: "save / skip"}, {Keys: "shift+tab", Desc: "back"}, {Keys: "esc", Desc: "skip onboarding"}}
		}
		if o.tokenMsg != "" {
			st := t.OK
			if o.tokenBad {
				st = t.Bad
			}
			out = append(out, "", t.R(st, style.Truncate(o.tokenMsg, w, "…")))
		}
	case obGoal:
		para("Pick where to start. Everything else stays one step away in the sidebar.")
		out = append(out, "")
		out = append(out, o.goals.View(t, w, h-len(out)-2, true)...)
		keys = append([]widgets.Binding{{Keys: "↑↓", Desc: "choose"}}, keys...)
	case obAgents:
		para("Let your coding agents run L80 for you: benchmark, rent, scan and publish, without you typing a command. L80 installs its skills where each agent looks for them.")
		out = append(out, "")
		for i, a := range o.agents {
			box := "[ ]"
			if a.on {
				box = "[x]"
			}
			found := t.R(t.Muted, "not found")
			if a.detected {
				found = t.R(t.OK, "detected")
			}
			line := fmt.Sprintf("%s %-12s ", box, a.t.Name) + found + t.R(t.Muted, "  "+a.t.Harness)
			if i == o.cur {
				line = t.R(t.Accent, "▸ ") + line
			} else {
				line = "  " + line
			}
			out = append(out, style.Truncate(line, w, "…"))
			if a.result != "" {
				st := t.OK
				if a.bad {
					st = t.Bad
				}
				out = append(out, t.R(st, style.Truncate("      "+a.result+" in "+a.dir, w, "…")))
			}
		}
		out = append(out, "")
		if o.installed {
			para("Agents pick the skills up on their next session. Later: `L80 skills targets` shows where they are, and `L80 update` keeps them current.")
			keys = []widgets.Binding{{Keys: "enter", Desc: "next"}, {Keys: "b", Desc: "back"}, {Keys: "esc", Desc: "skip onboarding"}}
		} else {
			para("Using another agent? `L80 skills link --dir <its skills dir> --all`, or paste `L80 skills agents-md` into its AGENTS.md.")
			keys = []widgets.Binding{{Keys: "↑↓", Desc: "move"}, {Keys: "space", Desc: "toggle"}, {Keys: "enter", Desc: "install"}, {Keys: "b", Desc: "back"}, {Keys: "esc", Desc: "skip"}}
		}
	case obDone:
		dest := "Home"
		if it, ok := o.goals.Selected(); ok {
			dest = labels[it.Value.(screenID)]
		}
		para("You are set up. A few things worth knowing:")
		out = append(out, "",
			"  "+t.R(t.Accent, "?")+"            keys, overrides and environment",
			"  "+t.R(t.Accent, "About")+"        our story, the Discord, and every command",
			"  "+t.R(t.Accent, "L80 onboard")+"  runs this again",
			"  "+t.R(t.Accent, "L80 update")+"   keeps L80, its tools and your agents' skills current",
			"")
		para("Questions, ideas and help: " + discordURL)
		keys = []widgets.Binding{{Keys: "enter", Desc: "open " + dest}, {Keys: "b", Desc: "back"}}
	}
	return style.Fit(out, w, h), keys
}
