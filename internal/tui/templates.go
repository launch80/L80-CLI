package tui

import (
	"fmt"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/betterbench"
	"github.com/launch80/L80-CLI/internal/pwdet"
	"github.com/launch80/L80-CLI/internal/qualitybench"
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

// Templates: `L80 templates list`, with each template's details.

const scrTemplates screenID = "templates"

func init() {
	register(screenDef{id: scrTemplates, label: "Templates", side: true, blurb: "The page templates this launch80 server accepts.",
		get: func(c *Console) screenModel { return templatesOf(c) }, open: func(c *Console) Cmd { return templatesOf(c).load(c) }})
}

type templatesScreen struct {
	table   widgets.Table
	rows    []api.Template
	loading bool
	err     *api.Error
	detail  bool
}

type templatesLoaded struct {
	rows []api.Template
	err  *api.Error
}

func templatesOf(c *Console) *templatesScreen { return stateOf[templatesScreen](c, scrTemplates) }

func (s *templatesScreen) load(c *Console) Cmd {
	s.loading = true
	return func() Msg {
		rows, err := c.d.Client.Templates()
		return templatesLoaded{rows, err}
	}
}

// templateMaker names what in L80 writes a payload for a template.
var templateMaker = map[string]string{
	betterbench.TemplateID:    "Bench Speed, or `L80 betterbench`",
	betterbench.RawTemplateID: "Bench Speed, or `L80 betterbench`",
	pwdet.ScanTemplateID:      "Bench Risk, or `L80 pwdet scan --publish`",
	qualitybench.TemplateID:   "QualityBench, or `L80 qualitybench`",
}

func (s *templatesScreen) update(c *Console, m Msg) Cmd {
	switch v := m.(type) {
	case templatesLoaded:
		s.loading, s.rows, s.err = false, v.rows, v.err
		s.table = widgets.Table{Columns: []widgets.Column{{Title: "Template", Width: 24}, {Title: "Version", Width: 7, Right: true}, {Title: "Label"}}}
		for _, r := range v.rows {
			s.table.Rows = append(s.table.Rows, []string{r.ID, fmt.Sprintf("v%d", r.Version), orDash(r.Label)})
		}
	case KeyMsg:
		if s.detail {
			if isBack(v) || v.Code == input.Enter {
				s.detail = false
			}
			return nil
		}
		if s.table.HandleKey(v) {
			return nil
		}
		switch {
		case isBack(v):
			c.scr = scrHome
		case v.Code == input.Enter && len(s.rows) > 0:
			s.detail = true
		case v.Code == input.Rune && v.Rune == 'r':
			return s.load(c)
		}
	}
	return nil
}

func (s *templatesScreen) view(c *Console, w, h int) ([]string, []widgets.Binding) {
	t := c.th
	switch {
	case s.loading:
		return []string{t.R(t.Muted, widgets.Spin(c.tick)+" loading…")}, nil
	case s.err != nil:
		out := []string{t.R(t.Bad, clip(s.err.Message, w))}
		if s.err.Remedy != "" {
			out = append(out, t.R(t.Muted, clip(s.err.Remedy, w)))
		}
		return out, []widgets.Binding{{Keys: "r", Desc: "retry"}, {Keys: "esc", Desc: "back"}}
	}
	if s.detail && s.table.Cursor < len(s.rows) {
		r := s.rows[s.table.Cursor]
		out := []string{t.R(t.Title, style.Sanitize(orDash(r.Label))), "",
			kv(t, "id", r.ID), kv(t, "version", fmt.Sprint(r.Version)), kv(t, "schema", style.Sanitize(orDash(r.SchemaURL)))}
		if mk, ok := templateMaker[r.ID]; ok {
			out = append(out, kv(t, "written by", mk))
		}
		out = append(out, "", t.R(t.Muted, "Publish a payload you wrote yourself:"),
			"  L80 publish payload.json --template "+r.ID+" --dry-run",
			t.R(t.Muted, "--dry-run checks it here and publishes nothing. Published pages are public."))
		for i := range out {
			out[i] = style.Truncate(out[i], w, "…")
		}
		return out, []widgets.Binding{{Keys: "esc", Desc: "back"}}
	}
	head := t.R(t.Muted, fmt.Sprintf("%d template(s) on %s", len(s.rows), c.d.Config.BaseURL))
	return append([]string{style.Truncate(head, w, "…"), ""}, s.table.View(t, w, h-2, true)...),
		[]widgets.Binding{{Keys: "↑↓", Desc: "move"}, {Keys: "enter", Desc: "details"}, {Keys: "r", Desc: "refresh"}, {Keys: "esc", Desc: "back"}}
}
