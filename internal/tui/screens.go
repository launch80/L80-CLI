package tui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/betterbench"
	"github.com/launch80/L80-CLI/internal/history"
	"github.com/launch80/L80-CLI/internal/pytool"
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

// --- home ----------------------------------------------------------------------

type homeScreen struct {
	loaded    bool
	reachable bool
	reachErr  string
	templates []string
	tools     []app.ToolStatus
	recent    []history.Entry
}

type homeLoaded struct {
	reachable bool
	err       string
	templates []string
	tools     []app.ToolStatus
	recent    []history.Entry
}

func (h *homeScreen) load(c *Console) Cmd {
	return func() Msg {
		var out homeLoaded
		if hl, err := c.d.Client.Health(); err == nil {
			out.reachable, out.templates = true, hl.Templates
		} else {
			out.err = err.Message
		}
		out.tools, _ = app.ToolsCheck(nil, false)
		if entries, err := history.Read(); err == nil {
			if len(entries) > 5 {
				entries = entries[:5]
			}
			out.recent = entries
		}
		return out
	}
}

func (h *homeScreen) update(c *Console, m Msg) Cmd {
	switch v := m.(type) {
	case homeLoaded:
		h.loaded, h.reachable, h.reachErr, h.templates, h.tools, h.recent = true, v.reachable, v.err, v.templates, v.tools, v.recent
	case KeyMsg:
		if c.menu.HandleKey(v) {
			return nil
		}
		switch {
		case v.Code == input.Enter || v.Code == input.Right:
			if it, ok := c.menu.Selected(); ok {
				return c.open(it.Value.(screenID))
			}
		case v.Code == input.Rune && v.Rune >= '1' && v.Rune <= '7':
			c.menu.Cursor = int(v.Rune - '1')
			return c.open(c.menu.Items[c.menu.Cursor].Value.(screenID))
		case v.Code == input.Rune && v.Rune == 'q':
			return Quit
		case v.Code == input.Rune && v.Rune == 'r':
			return h.load(c)
		case v.Code == input.Rune && v.Rune == '?':
			c.scr = scrHelp
		}
	}
	return nil
}

func (h *homeScreen) view(c *Console, w, ht int) ([]string, []widgets.Binding) {
	t := c.th
	mw := min(46, w/2)
	menu := []string{t.R(t.Title, "What do you want to do?"), ""}
	menu = append(menu, c.menu.View(t, mw-2, ht-2, true)...)

	sw := w - mw - 3
	var st []string
	st = append(st, t.R(t.Title, "This machine"), "")
	if !h.loaded {
		st = append(st, t.R(t.Muted, widgets.Spin(c.tick)+" checking…"))
	} else {
		server := t.R(t.OK, "reachable")
		if !h.reachable {
			server = t.R(t.Bad, "unreachable") + t.R(t.Muted, " "+clip(h.reachErr, 40))
		}
		st = append(st, kv(t, "launch80", c.d.Config.BaseURL), kv(t, "server", server))
		tok := t.R(t.Warn, "none") + t.R(t.Muted, " (tools are free; publishing needs one)")
		if c.d.Config.HasToken() {
			tok = t.R(t.OK, masked(c.d.Config)) + t.R(t.Muted, " from "+string(c.d.Config.TokenSource))
		}
		st = append(st, kv(t, "token", tok), "")
		st = append(st, t.R(t.Title, "Tools"))
		for _, ts := range h.tools {
			st = append(st, kv(t, ts.Name, toolState(t, ts)))
		}
		if len(h.recent) > 0 {
			st = append(st, "", t.R(t.Title, "Recently published"))
			for _, e := range h.recent {
				when := e.CreatedAt
				if len(when) >= 10 {
					when = when[:10]
				}
				st = append(st, t.R(t.Muted, when+" ")+style.Sanitize(orDash(e.Title)))
				st = append(st, t.R(t.Muted, "           ")+style.Sanitize(e.URL))
			}
		}
	}
	for i := range st {
		st[i] = style.Truncate(st[i], sw, "…")
	}
	body := style.JoinH(style.Fit(menu, mw, ht), style.Fit([]string{}, 2, ht), st)
	return body, []widgets.Binding{{Keys: "↑↓", Desc: "move"}, {Keys: "enter", Desc: "open"}, {Keys: "1-7", Desc: "jump"}, {Keys: "r", Desc: "refresh"}, {Keys: "?", Desc: "help"}, {Keys: "q", Desc: "quit"}}
}

func kv(t widgets.Theme, k, v string) string { return t.R(t.Muted, style.PadRight(k, 13)) + v }

func toolState(t widgets.Theme, ts app.ToolStatus) string {
	switch ts.Status {
	case "ok":
		s := t.R(t.OK, "ready") + t.R(t.Muted, " via "+ts.Source)
		if ts.Version != "" {
			s += t.R(t.Muted, ", "+ts.Version)
		}
		return s
	case "stale":
		return t.R(t.Warn, "older than pinned") + t.R(t.Muted, " via "+ts.Source)
	}
	if ts.Pinned == "unpinned" {
		return t.R(t.Warn, "not installed") + t.R(t.Muted, " (not pinned in this release; set "+ts.Override+")")
	}
	return t.R(t.Bad, "unavailable") + t.R(t.Muted, " (install uv)")
}

// --- help --------------------------------------------------------------------------

func helpView(c *Console, w, h int) ([]string, []widgets.Binding) {
	t := c.th
	lines := []string{
		t.R(t.Title, "Keys"),
		"  ↑ ↓ j k     move            enter   open / run / confirm",
		"  tab         next field       esc q   back (q only outside text fields)",
		"  /           filter a list    ctrl+c  quit (asks if a job is running)",
		"  pgup pgdn   scroll a log     end     follow the log again",
		"",
		t.R(t.Title, "Everything here is also a command"),
		"  L80 betterbench --endpoint URL --model M     L80 vast run --spec run.yaml --max-hours 2",
		"  L80 pwdet scan org/model --json              L80 dashboard",
		"  L80 history                                  L80 tools check",
		"",
		t.R(t.Title, "What is free"),
		"  Every tool is free. Only publishing a page to launch80 needs a token",
		"  (L80_TOKEN, or ~/.config/L80/credentials.json).",
		"",
		t.R(t.Title, "Overrides"),
		"  L80_BETTERBENCH, L80_VAST, L80_PWDET, L80_DASHBOARD   run a local copy instead of the pinned one",
		"  L80_BETTERBENCH_API_KEY   endpoint key, never recorded      VAST_API_KEY   Vast.ai key",
		"  NO_COLOR                  plain output",
		"",
		t.R(t.Muted, "Docs: https://github.com/launch80/L80-CLI"),
	}
	return style.Fit(lines, w, h), []widgets.Binding{{Keys: "esc", Desc: "back"}}
}

// --- history --------------------------------------------------------------------------

type histScreen struct {
	table   widgets.Table
	entries []history.Entry
	source  string
	err     string
	loading bool
	detail  bool
}

type verifyResult struct {
	status string
	err    *api.Error
}

// verify runs the pre-checks for the selected page, shows them, and on
// confirmation asks launch80 to review it.
func (s *histScreen) verify(c *Console, e history.Entry) Cmd {
	if e.TemplateID != betterbench.RawTemplateID {
		c.setFlash("only BetterBench pages (bench.betterbench.v1) can be verified", true)
		return nil
	}
	t, apiErr := app.ResolveTarget(e.GUID)
	if apiErr != nil {
		c.setFlash(apiErr.Message, true)
		return nil
	}
	t.RunChecks()
	var lines []string
	for _, ck := range t.Checks {
		lines = append(lines, map[string]string{"pass": "ok   ", "warn": "warn ", "fail": "FAIL "}[ck.Status]+ck.Name+": "+ck.Detail)
	}
	blocked := betterbench.Blocking(t.Checks)
	body := strings.Join(lines, "\n")
	if blocked {
		c.confirm(&widgets.Dialog{Title: "Not verifiable yet", Body: body + "\n\nFix the failed checks and publish a new run.", Buttons: []string{"OK"}}, nil)
		return nil
	}
	if !c.d.Config.HasToken() {
		c.setFlash("requesting verification needs the key that published the page", true)
		return nil
	}
	c.confirm(&widgets.Dialog{Title: "Request verification", Body: body + "\n\nA launch80 reviewer checks the run; if it holds up the page is marked verified.",
		Buttons: []string{"Request", "Cancel"}, Choice: 1}, func(ch int) Cmd {
		if ch != 0 {
			return nil
		}
		return func() Msg {
			v, err := c.d.Client.RequestVerification(t.GUID, t.Request("", c.d.Version))
			if err != nil {
				if api.NotSupported(err) {
					err = api.Newf("E_NOT_SUPPORTED", "", "this launch80 server does not accept verification requests yet")
				}
				return verifyResult{err: err}
			}
			return verifyResult{status: v.Status}
		}
	})
	return nil
}

type histLoaded struct {
	entries []history.Entry
	source  string
	err     string
}

func (s *histScreen) load(c *Console) Cmd {
	s.loading = true
	return func() Msg {
		page, apiErr := c.d.Client.History(100, "")
		if apiErr == nil {
			var out []history.Entry
			for _, a := range page.Artifacts {
				out = append(out, history.Entry{GUID: a.GUID, URL: a.URL, TemplateID: a.TemplateID, Title: a.Title, CreatedAt: a.CreatedAt, TrustTier: a.TrustTier})
			}
			return histLoaded{entries: out, source: "server"}
		}
		local, err := history.Read()
		msg := apiErr.Message + "; showing this machine's record"
		if err != nil {
			msg = err.Error()
		}
		return histLoaded{entries: local, source: "local", err: msg}
	}
}

func (s *histScreen) update(c *Console, m Msg) Cmd {
	switch v := m.(type) {
	case verifyResult:
		if v.err != nil {
			c.setFlash(v.err.Message, true)
		} else {
			c.setFlash("verification "+v.status, false)
		}
	case histLoaded:
		s.loading, s.entries, s.source, s.err = false, v.entries, v.source, v.err
		s.table = widgets.Table{Columns: []widgets.Column{{Title: "When", Width: 16}, {Title: "Template", Width: 20}, {Title: "Tier", Width: 10}, {Title: "Title"}}}
		for _, e := range v.entries {
			when := strings.Replace(e.CreatedAt, "T", " ", 1)
			if len(when) > 16 {
				when = when[:16]
			}
			s.table.Rows = append(s.table.Rows, []string{when, e.TemplateID, orDash(e.TrustTier), orDash(e.Title)})
		}
	case KeyMsg:
		if v.Code == input.Rune && v.Rune == 'v' && s.table.Cursor < len(s.entries) {
			return s.verify(c, s.entries[s.table.Cursor])
		}
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
		case v.Code == input.Enter && len(s.entries) > 0:
			s.detail = true
		case v.Code == input.Rune && v.Rune == 'r':
			return s.load(c)
		}
	}
	return nil
}

func (s *histScreen) view(c *Console, w, h int) ([]string, []widgets.Binding) {
	t := c.th
	if s.loading {
		return []string{t.R(t.Muted, widgets.Spin(c.tick)+" loading…")}, nil
	}
	head := t.R(t.Muted, fmt.Sprintf("%d page(s), from the %s record", len(s.entries), s.source))
	if s.err != "" {
		head += t.R(t.Warn, "  "+clip(s.err, w-40))
	}
	if s.detail && s.table.Cursor < len(s.entries) {
		e := s.entries[s.table.Cursor]
		lines := []string{head, "", t.R(t.Title, style.Sanitize(orDash(e.Title))), "",
			kv(t, "url", style.Sanitize(e.URL)), kv(t, "template", e.TemplateID), kv(t, "tier", orDash(e.TrustTier)),
			kv(t, "published", e.CreatedAt), kv(t, "guid", e.GUID)}
		if e.SourcePath != "" {
			lines = append(lines, kv(t, "from file", style.Sanitize(e.SourcePath)))
		}
		if e.SourceSHA256 != "" {
			lines = append(lines, kv(t, "file sha256", e.SourceSHA256[:16]+"…"))
		}
		lines = append(lines, "", t.R(t.Muted, "Pages are public. A page is self-reported until launch80 verifies it."))
		return lines, []widgets.Binding{{Keys: "v", Desc: "request verification"}, {Keys: "esc", Desc: "back"}}
	}
	return append([]string{head, ""}, s.table.View(t, w, h-2, true)...),
		[]widgets.Binding{{Keys: "↑↓", Desc: "move"}, {Keys: "enter", Desc: "details"}, {Keys: "v", Desc: "verify"}, {Keys: "r", Desc: "refresh"}, {Keys: "esc", Desc: "back"}}
}

// --- tools --------------------------------------------------------------------------

type toolsScreen struct {
	table   widgets.Table
	rows    []app.ToolStatus
	loading bool
	job     *job
}

type toolsLoaded struct{ rows []app.ToolStatus }

func (s *toolsScreen) load(c *Console, probe bool) Cmd {
	s.loading = true
	return func() Msg {
		rows, _ := app.ToolsCheck(nil, probe)
		return toolsLoaded{rows}
	}
}

func (s *toolsScreen) update(c *Console, m Msg) Cmd {
	switch v := m.(type) {
	case toolsLoaded:
		s.loading, s.rows = false, v.rows
		s.table = widgets.Table{Columns: []widgets.Column{{Title: "Tool", Width: 12}, {Title: "State", Width: 8}, {Title: "Pinned", Width: 22}, {Title: "Version", Width: 8}, {Title: "Resolves via"}}}
		for _, r := range v.rows {
			s.table.Rows = append(s.table.Rows, []string{r.Name, r.Status, r.Pinned, orDash(r.Version), orDash(r.Source)})
		}
	case jobDone:
		if v.j == s.job {
			return s.load(c, false)
		}
	case KeyMsg:
		if s.job != nil && s.job.log.HandleKey(v) {
			return nil
		}
		if s.table.HandleKey(v) {
			return nil
		}
		switch {
		case isBack(v):
			c.scr = scrHome
		case v.Code == input.Rune && v.Rune == 'v':
			return s.load(c, true)
		case (v.Code == input.Rune && v.Rune == 'p') || v.Code == input.Enter:
			if s.job != nil && s.job.running {
				return nil
			}
			if row, ok := s.table.Row(); ok {
				name := row[0]
				s.job = newJob("prefetch " + name)
				return c.start(s.job, func(ctx context.Context, out io.Writer) (any, int, error) {
					t, _ := pytool.Lookup(name)
					args := t.VersionCmd
					if len(args) == 0 {
						args = []string{"--help"}
					}
					code, err := runTool(ctx, name, args, nil, out)
					return nil, code, err
				})
			}
		}
	}
	return nil
}

func (s *toolsScreen) view(c *Console, w, h int) ([]string, []widgets.Binding) {
	t := c.th
	if s.loading {
		return []string{t.R(t.Muted, widgets.Spin(c.tick)+" resolving tools…")}, nil
	}
	tableH := min(len(s.rows)+2, h/2)
	out := s.table.View(t, w, tableH, true)
	if row, ok := s.table.Row(); ok {
		for _, r := range s.rows {
			if r.Name == row[0] {
				out = append(out, "")
				for _, n := range r.Notes {
					out = append(out, t.R(t.Muted, "  "+clip(n, w-2)))
				}
				if r.Remedy != "" {
					for _, l := range style.Wrap(r.Remedy, w-4) {
						out = append(out, t.R(t.Warn, "  "+l))
					}
				}
				out = append(out, t.R(t.Muted, "  override: "+r.Override+"=/path/to/"+r.Exe))
			}
		}
	}
	if s.job != nil {
		out = append(out, "", jobHeader(c, s.job, w))
		out = append(out, s.job.log.View(t, w, max(3, h-len(out)))...)
	}
	return out, []widgets.Binding{{Keys: "enter/p", Desc: "fetch & run --version"}, {Keys: "v", Desc: "probe all versions"}, {Keys: "esc", Desc: "back"}}
}

func jobHeader(c *Console, j *job, w int) string {
	t := c.th
	switch {
	case j.running:
		return t.R(t.Accent, widgets.Spin(c.tick)+" "+j.title) + t.R(t.Muted, "  "+elapsed(j)+"  (ctrl+x stops it)")
	case j.err != nil:
		return t.R(t.Bad, "✗ "+j.title+": "+clip(j.err.Error(), w-len(j.title)-6))
	case j.code != 0:
		return t.R(t.Bad, fmt.Sprintf("✗ %s exited with status %d", j.title, j.code)) + t.R(t.Muted, "  "+elapsed(j))
	default:
		return t.R(t.OK, "✓ "+j.title) + t.R(t.Muted, "  "+elapsed(j))
	}
}

// --- dashboard --------------------------------------------------------------------------

type dashScreen struct {
	menu    widgets.List
	serving bool
	checked bool
	msg     string
}

type dashChecked struct{ serving bool }
type dashExited struct{ err error }

const dashPort = "8787"

func (s *dashScreen) load(c *Console) Cmd {
	s.menu = widgets.List{Items: []widgets.Item{
		{Title: "Open the terminal dashboard", Desc: "Takes over this terminal; q in the dashboard comes back here", Value: "tui"},
		{Title: "Run the sampler and web page here", Desc: "http://127.0.0.1:" + dashPort + "  (Ctrl-C stops it and comes back)", Value: "serve"},
	}}
	return func() Msg {
		cl := &http.Client{Timeout: 700 * time.Millisecond}
		resp, err := cl.Get("http://127.0.0.1:" + dashPort + "/api/snapshot")
		if err == nil {
			resp.Body.Close()
		}
		return dashChecked{serving: err == nil && resp.StatusCode == 200}
	}
}

func (s *dashScreen) update(c *Console, m Msg) Cmd {
	switch v := m.(type) {
	case dashChecked:
		s.checked, s.serving = true, v.serving
	case dashExited:
		s.msg = ""
		if v.err != nil {
			s.msg = v.err.Error()
		}
		return s.load(c)
	case KeyMsg:
		if s.menu.HandleKey(v) {
			return nil
		}
		switch {
		case isBack(v):
			c.scr = scrHome
		case v.Code == input.Enter:
			it, ok := s.menu.Selected()
			if !ok {
				return nil
			}
			verb := it.Value.(string)
			if verb == "serve" && s.serving {
				c.setFlash("a sampler is already serving on :"+dashPort+"; open the terminal dashboard to attach to it", true)
				return nil
			}
			env := []string{"AI_DASHBOARD_STATE=" + c.d.DashboardState}
			return c.interactive("dashboard", []string{verb}, env, func(err error) Msg { return dashExited{err} })
		}
	}
	return nil
}

func (s *dashScreen) view(c *Console, w, h int) ([]string, []widgets.Binding) {
	t := c.th
	out := []string{t.R(t.Title, "AI command dashboard"), t.R(t.Muted, "Read-only telemetry: GPU power, temperatures, VRAM, engines, live vLLM and gateway state."), ""}
	switch {
	case !s.checked:
		out = append(out, t.R(t.Muted, widgets.Spin(c.tick)+" looking for a running sampler…"))
	case s.serving:
		out = append(out, t.R(t.OK, "A sampler is serving on http://127.0.0.1:"+dashPort)+t.R(t.Muted, " — the terminal dashboard will attach to it."))
	default:
		out = append(out, t.R(t.Muted, "No sampler is running; the terminal dashboard will sample by itself."))
	}
	if s.msg != "" {
		out = append(out, t.R(t.Bad, clip(s.msg, w)))
	}
	out = append(out, "")
	out = append(out, s.menu.View(t, w, h-len(out), true)...)
	return out, []widgets.Binding{{Keys: "enter", Desc: "start"}, {Keys: "esc", Desc: "back"}}
}
