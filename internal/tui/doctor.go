package tui

import (
	"strings"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/pytool"
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

// Doctor: the checks `L80 doctor` runs, each with what to do about it.

const scrDoctor screenID = "doctor"

func init() {
	register(screenDef{id: scrDoctor, label: "Doctor", side: true, blurb: "What `L80 doctor` checks, and how to fix what fails.",
		get: func(c *Console) screenModel { return doctorOf(c) }, open: func(c *Console) Cmd { return doctorOf(c).load(c) }})
}

// doctorCheck is one line of the report.
type doctorCheck struct {
	name, status, detail, remedy string // status: ok | warn | fail
}

type doctorScreen struct {
	checks  []doctorCheck
	loading bool
}

type doctorDone struct{ checks []doctorCheck }

func doctorOf(c *Console) *doctorScreen { return stateOf[doctorScreen](c, scrDoctor) }

// doctorInputs is what `L80 doctor` gathers, kept apart so the judging is testable.
type doctorInputs struct {
	version   string
	cfg       config.Config
	health    *api.Health
	healthErr *api.Error
	uvx       bool
	tools     []app.ToolStatus
}

// doctorChecks turns what was gathered into checks with remedies.
func doctorChecks(in doctorInputs) []doctorCheck {
	out := []doctorCheck{{name: "L80", status: "ok", detail: in.version}}
	if in.healthErr != nil {
		r := in.healthErr.Remedy
		if r == "" {
			r = "Check the network, or the API base in Settings (L80_API_BASE)."
		}
		out = append(out, doctorCheck{"launch80", "fail", in.cfg.BaseURL + " unreachable: " + in.healthErr.Message, r})
	} else {
		d := in.cfg.BaseURL + " reachable"
		if in.health != nil && len(in.health.Templates) > 0 {
			d += "; templates " + strings.Join(in.health.Templates, ", ")
		}
		out = append(out, doctorCheck{name: "launch80", status: "ok", detail: d})
	}
	if in.cfg.HasToken() {
		out = append(out, doctorCheck{name: "token", status: "ok", detail: masked(in.cfg) + " from " + string(in.cfg.TokenSource)})
	} else {
		out = append(out, doctorCheck{"token", "warn", "none: tools work, publishing does not",
			"Log in from Settings, or set L80_TOKEN."})
	}
	if in.uvx {
		out = append(out, doctorCheck{name: "uvx", status: "ok", detail: "on PATH; pinned tools fetch on first use"})
	} else {
		out = append(out, doctorCheck{"uvx", "warn", "not on PATH", "Install uv (https://docs.astral.sh/uv/) so L80 can fetch the pinned tools, or set each tool's override."})
	}
	for _, t := range in.tools {
		ck := doctorCheck{name: t.Name, detail: t.Status}
		if t.Source != "" {
			ck.detail += " via " + t.Source
		}
		switch t.Status {
		case "ok":
			ck.status = "ok"
		case "unreleased":
			ck.status, ck.detail = "ok", "not released yet"
		case "stale":
			ck.status, ck.remedy = "warn", "Run `L80 update` (Settings) to bring it current."
		default:
			ck.status, ck.remedy = "fail", t.Remedy
			if ck.remedy == "" {
				ck.remedy = "Run `L80 update` (Settings); it fixes what it can and says what is left."
			}
		}
		out = append(out, ck)
	}
	return out
}

func (s *doctorScreen) load(c *Console) Cmd {
	s.loading = true
	return func() Msg {
		in := doctorInputs{version: c.d.Version, cfg: c.d.Config, uvx: app.HasUvx()}
		in.health, in.healthErr = c.d.Client.Health()
		// As `L80 doctor`: probe only overrides, since probing a uvx bootstrap may download it.
		in.tools, _ = app.ToolsCheckSelective(nil, func(r pytool.Runner) bool { return r.Source == r.Tool.Env })
		return doctorDone{doctorChecks(in)}
	}
}

func (s *doctorScreen) update(c *Console, m Msg) Cmd {
	switch v := m.(type) {
	case doctorDone:
		s.loading, s.checks = false, v.checks
	case KeyMsg:
		switch {
		case isBack(v):
			c.scr = scrHome
		case v.Code == input.Rune && v.Rune == 'r':
			return s.load(c)
		}
	}
	return nil
}

func (s *doctorScreen) view(c *Console, w, h int) ([]string, []widgets.Binding) {
	t := c.th
	keys := []widgets.Binding{{Keys: "r", Desc: "run again"}, {Keys: "esc", Desc: "back"}}
	if s.loading {
		return []string{t.R(t.Muted, widgets.Spin(c.tick)+" checking…")}, keys
	}
	var out []string
	bad := 0
	for _, ck := range s.checks {
		mark := t.R(t.OK, "✓")
		switch ck.status {
		case "warn":
			mark, bad = t.R(t.Warn, "!"), bad+1
		case "fail":
			mark, bad = t.R(t.Bad, "✗"), bad+1
		}
		out = append(out, mark+" "+kv(t, ck.name, style.Sanitize(ck.detail)))
		if ck.remedy == "" {
			continue
		}
		for _, l := range style.Wrap(style.Sanitize(ck.remedy), max(10, w-15)) {
			out = append(out, t.R(t.Muted, "               "+l))
		}
	}
	if len(s.checks) > 0 {
		sum := t.R(t.OK, "Everything checks out.")
		if bad > 0 {
			sum = t.R(t.Warn, "Some checks need attention; each says what to do.")
		}
		out = append(out, "", sum, t.R(t.Muted, "Tools probes versions; Settings runs `L80 update`."))
	}
	for i := range out {
		out[i] = style.Truncate(out[i], w, "…")
	}
	return style.Fit(out, w, h), keys
}
