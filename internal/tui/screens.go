package tui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/betterbench"
	"github.com/launch80/L80-CLI/internal/history"
	"github.com/launch80/L80-CLI/internal/pwdet"
	"github.com/launch80/L80-CLI/internal/pytool"
	"github.com/launch80/L80-CLI/internal/qualitybench"
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

// --- home ----------------------------------------------------------------------

type homeScreen struct {
	cursor    int // in launch
	loaded    bool
	reachable bool
	reachErr  string
	templates []string
	tools     []app.ToolStatus
	recent    []history.Entry
}

// launch is Home's "What do you want to do?": each entry opens a screen.
var launch = []struct {
	title, desc string
	to          screenID
}{
	{"Benchmark speed", "BetterBench against an endpoint you serve", scrBench},
	{"Measure quality", "QualityBench on a local server, or a rented one", scrQuality},
	{"Compare two runs", "QualityBench: a reference run against a test run", scrQuality},
	{"Rent a GPU", "Serve and benchmark on Vast.ai, then tear it down", scrVast},
	{"Scan weights", "Check model weights for tampering", scrPwdet},
	{"Publish", "Your pages, and requesting verification", scrHistory},
	{"Verify", "Reproduce and review others' runs", scrVerify},
	{"Settings", "Token, server and updates", scrSettings},
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
		switch {
		case isBack(v):
			c.paneFocus = false // Home is the sidebar's own item
		case v.Code == input.Up || (v.Code == input.Rune && v.Rune == 'k'):
			h.cursor = (h.cursor + len(launch) - 1) % len(launch)
		case v.Code == input.Down || (v.Code == input.Rune && v.Rune == 'j'):
			h.cursor = (h.cursor + 1) % len(launch)
		case v.Code == input.Home:
			h.cursor = 0
		case v.Code == input.End:
			h.cursor = len(launch) - 1
		case v.Code == input.Enter:
			l := launch[h.cursor]
			if l.to == scrQuality {
				c.qb.ensure()
				c.qb.mode.Choice = modeRun
				c.qb.focus = qsScope
				if strings.HasPrefix(l.title, "Compare") {
					c.qb.mode.Choice, c.qb.focus = modeCompare, qsCmpRef
				}
			}
			return c.goTo(l.to, true)
		case v.Code == input.Rune && v.Rune == 'r':
			return h.load(c)
		}
	}
	return nil
}

// launcher draws the menu; the cursor shows only while Home has the keyboard.
func (h *homeScreen) launcher(c *Console, w int) []string {
	t := c.th
	out := []string{t.R(t.Title, "What do you want to do?")}
	tw := 0
	for _, l := range launch {
		tw = max(tw, len(l.title))
	}
	for i, l := range launch {
		name := style.PadRight(l.title, tw)
		if i == h.cursor && c.paneFocus {
			out = append(out, t.R(t.Accent, "▸ ")+t.R(t.Selected, name)+"  "+t.R(t.Muted, l.desc))
		} else {
			out = append(out, "  "+name+"  "+t.R(t.Muted, l.desc))
		}
	}
	return out
}

func (h *homeScreen) view(c *Console, w, ht int) ([]string, []widgets.Binding) {
	t := c.th
	st := h.launcher(c, w)
	st = append(st, "")
	if !h.loaded {
		st = append(st, t.R(t.Muted, widgets.Spin(c.tick)+" checking…"))
	} else {
		server := t.R(t.OK, "reachable")
		if !h.reachable {
			server = t.R(t.Bad, "unreachable") + t.R(t.Muted, " "+clip(h.reachErr, 40))
		}
		tok := t.R(t.Warn, "none") + t.R(t.Muted, " (tools are free; publishing needs one)")
		if c.d.Config.HasToken() {
			tok = t.R(t.OK, masked(c.d.Config)) + t.R(t.Muted, " from "+string(c.d.Config.TokenSource))
		}
		if len(st)+5 > ht {
			// Too short for the status block: one line of it, above the menu.
			st = append([]string{t.R(t.Muted, "server ") + server + t.R(t.Muted, " · token ") + tok}, st[:len(st)-1]...)
			for i := range st {
				st[i] = style.Truncate(st[i], w, "…")
			}
			return style.Fit(st, w, ht), homeKeys
		}
		st = append(st, kv(t, "launch80", c.d.Config.BaseURL), kv(t, "server", server))
		st = append(st, kv(t, "token", tok), "")
		if js := newestFirst(c); len(js) > 0 {
			st = append(st, t.R(t.Title, "This session")+t.R(t.Muted, "  (finished QualityBench runs: Bench - History)"))
			for _, j := range js[:min(3, len(js))] {
				st = append(st, "  "+jobLine(c, j))
			}
			st = append(st, "")
		}
		st = append(st, t.R(t.Title, "Tools"))
		for _, ts := range h.tools {
			st = append(st, kv(t, ts.Name, toolState(t, ts)))
		}
		st = append(st, "", t.R(t.Title, "Last publishes"))
		if len(h.recent) == 0 {
			st = append(st, t.R(t.Muted, "  nothing yet: open Bench Speed to publish your first run"))
		}
		for _, e := range h.recent {
			tier := e.TrustTier
			if tier == "" {
				tier = "unverified"
			}
			tierS := t.R(t.Muted, tier)
			if tier == "verified" {
				tierS = t.R(t.OK, tier)
			}
			st = append(st, "  "+t.R(t.OK, "✓")+" "+style.Sanitize(clip(orDash(e.Title), max(10, w-18)))+"  "+tierS)
			st = append(st, t.R(t.Muted, "    "+style.Sanitize(e.URL)))
		}
	}
	for i := range st {
		st[i] = style.Truncate(st[i], w, "…")
	}
	return style.Fit(st, w, ht), homeKeys
}

var homeKeys = []widgets.Binding{{Keys: "↑↓", Desc: "move"}, {Keys: "enter", Desc: "go"}, {Keys: "r", Desc: "refresh"}, {Keys: "esc", Desc: "back"}}

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

// Bench - History has two tabs: Published (pages this key published) and
// Unpublished (QualityBench results on this machine, read from disk).
const (
	histPublished = iota
	histUnpublished
)

type histScreen struct {
	tab     int
	table   widgets.Table // published pages
	ltable  widgets.Table // unpublished reports
	entries []history.Entry
	local   []qualitybench.ResultFile
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
	if !app.Verifiable(e.TemplateID) && e.TemplateID != pwdet.TemplateID {
		c.setFlash("only BetterBench runs and pwdet scans can be verified", true)
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
		c.confirm(&widgets.Dialog{Title: "Not verifiable yet", Body: body + "\n\nFix the failed checks and publish again.", Buttons: []string{"OK"}}, nil)
		return nil
	}
	if !c.d.Config.HasToken() {
		c.setFlash("requesting verification needs the key that published the page", true)
		return nil
	}
	c.confirm(&widgets.Dialog{Title: "Request verification", Body: body + "\n\nA launch80 reviewer checks it; if it holds up the page is marked verified.",
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

type visibilityResult struct {
	v   *api.Visibility
	err *api.Error
}

// toggleListed asks, then lists the selected page in the public directory or
// takes it out. Only the key that published it can; the server checks.
func (s *histScreen) toggleListed(c *Console, e history.Entry) Cmd {
	if !api.Listable(e.TemplateID) {
		c.setFlash("only benchmark and scan pages can be listed in the Directory", true)
		return nil
	}
	if !c.d.Config.HasToken() {
		c.setFlash("listing a page needs the key that published it", true)
		return nil
	}
	if s.source != "server" {
		c.setFlash("the server's record is not loaded (r to refresh); listing needs it", true)
		return nil
	}
	title, body, button := "List in the Directory",
		"List this page in the launch80 Directory (launch80.com/directory)?\n\nAnyone can find, search and compare it there. The page, its link and its content do not change.",
		"List"
	if e.Listed {
		title, body, button = "Unlist",
			"Take this page out of the launch80 Directory?\n\nThe link keeps working for anyone who has it.",
			"Unlist"
	}
	listed := !e.Listed
	c.confirm(&widgets.Dialog{Title: title, Body: body, Buttons: []string{button, "Cancel"}, Choice: 1}, func(ch int) Cmd {
		if ch != 0 {
			return nil
		}
		return func() Msg {
			v, err := c.d.Client.SetVisibility(e.GUID, listed)
			return visibilityResult{v: v, err: err}
		}
	})
	return nil
}

type histLoaded struct {
	entries []history.Entry
	local   []qualitybench.ResultFile
	source  string
	err     string
}

// unpublishedQuality is every QualityBench results file the QualityBench
// screen can find on this machine that this machine has not published.
var unpublishedQuality = func(c *Console) []qualitybench.ResultFile {
	published := map[string]bool{}
	if entries, err := history.Read(); err == nil {
		for _, e := range entries {
			if e.TemplateID == qualitybench.TemplateID && e.SourcePath != "" {
				published[e.SourcePath] = true
			}
		}
	}
	var out []qualitybench.ResultFile
	for _, r := range localResults(c) {
		if !published[r.Path] {
			out = append(out, r)
		}
	}
	return out
}

func (s *histScreen) load(c *Console) Cmd {
	s.loading = true
	return func() Msg {
		local := unpublishedQuality(c)
		page, apiErr := c.d.Client.History(100, "")
		if apiErr == nil {
			var out []history.Entry
			for _, a := range page.Artifacts {
				out = append(out, history.Entry{GUID: a.GUID, URL: a.URL, TemplateID: a.TemplateID, Title: a.Title, CreatedAt: a.CreatedAt, TrustTier: a.TrustTier, RunClass: a.RunClass, Listed: a.Listed})
			}
			return histLoaded{entries: out, local: local, source: "server"}
		}
		record, err := history.Read()
		msg := apiErr.Message + "; showing this machine's record"
		if err != nil {
			msg = err.Error()
		}
		return histLoaded{entries: record, local: local, source: "local", err: msg}
	}
}

// selected is the row under the cursor on the open tab: an unpublished
// report, or a published page.
func (s *histScreen) selected() (*qualitybench.ResultFile, *history.Entry) {
	if s.tab == histUnpublished {
		if i := s.ltable.Cursor; i >= 0 && i < len(s.local) {
			return &s.local[i], nil
		}
		return nil, nil
	}
	if i := s.table.Cursor; i >= 0 && i < len(s.entries) {
		return nil, &s.entries[i]
	}
	return nil, nil
}

// cur is the open tab's table.
func (s *histScreen) cur() *widgets.Table {
	if s.tab == histUnpublished {
		return &s.ltable
	}
	return &s.table
}

// publishLocal asks, then publishes a local QualityBench results file.
func (s *histScreen) publishLocal(c *Console, r qualitybench.ResultFile) Cmd {
	data, err := os.ReadFile(r.Path)
	if err != nil {
		c.setFlash("could not read "+r.Path+": "+err.Error(), true)
		return nil
	}
	res := &app.QualityResult{Path: r.Path, Report: qualitybench.ReportPath(r.Path), Data: data, Partial: qualitybench.Partials(data)}
	return publishQuality(c, res, r.Compare, false, false)
}

// listedMark is the History table's Dir column: ● listed, blank unlisted, - not listable.
func listedMark(e history.Entry) string {
	switch {
	case !api.Listable(e.TemplateID):
		return "-"
	case e.Listed:
		return "●"
	}
	return ""
}

func listedText(e history.Entry) string {
	switch {
	case !api.Listable(e.TemplateID):
		return "cannot be listed (" + e.TemplateID + ")"
	case e.Listed:
		return "listed"
	}
	return "unlisted"
}

func listKeyDesc(e history.Entry) string {
	if e.Listed {
		return "unlist"
	}
	return "list in Directory"
}

// whenOf is a results file's run time in the table's format.
func whenOf(r qualitybench.ResultFile) string {
	when := strings.Replace(r.Generated, "T", " ", 1)
	if len(when) > 16 {
		when = when[:16]
	}
	return when
}

func (s *histScreen) update(c *Console, m Msg) Cmd {
	switch v := m.(type) {
	case verifyResult:
		if v.err != nil {
			c.setFlash(v.err.Message, true)
		} else {
			c.setFlash("verification "+v.status, false)
		}
	case qbPublished:
		if v.err == nil {
			return s.load(c)
		}
	case visibilityResult:
		if v.err != nil {
			c.setFlash(v.err.Code+": "+v.err.Message+" — "+v.err.Remedy, true)
			return nil
		}
		for i := range s.entries {
			if s.entries[i].GUID == v.v.GUID {
				s.entries[i].Listed = v.v.Listed
				if i < len(s.table.Rows) {
					s.table.Rows[i][4] = listedMark(s.entries[i])
				}
			}
		}
		if v.v.Listed {
			c.setFlash("listed in the Directory: "+v.v.URL, false)
		} else {
			c.setFlash("unlisted (the link still works): "+v.v.URL, false)
		}
	case histLoaded:
		pc, lc := s.table.Cursor, s.ltable.Cursor
		s.loading, s.entries, s.local, s.source, s.err = false, v.entries, v.local, v.source, v.err
		s.table = widgets.Table{Columns: []widgets.Column{{Title: "When", Width: 16}, {Title: "Template", Width: 20}, {Title: "Tier", Width: 10}, {Title: "Class", Width: 6}, {Title: "Dir", Width: 3}, {Title: "Title"}}}
		for _, e := range v.entries {
			when := strings.Replace(e.CreatedAt, "T", " ", 1)
			if len(when) > 16 {
				when = when[:16]
			}
			s.table.Rows = append(s.table.Rows, []string{when, e.TemplateID, orDash(e.TrustTier), orDash(e.RunClass), listedMark(e), orDash(e.Title)})
		}
		s.ltable = widgets.Table{Columns: []widgets.Column{{Title: "Run", Width: 16}, {Title: "Class", Width: 6}, {Title: "Tasks", Width: 5, Right: true}, {Title: "Model", Width: 34}, {Title: "Title"}}}
		for _, r := range v.local {
			class := "full"
			if r.Quick {
				class = "quick"
			}
			s.ltable.Rows = append(s.ltable.Rows, []string{whenOf(r), class, fmt.Sprint(r.Tasks), r.Model, orDash(r.Title)})
		}
		s.table.Cursor = min(pc, max(0, len(s.table.Rows)-1))
		s.ltable.Cursor = min(lc, max(0, len(s.ltable.Rows)-1))
		if s.tab == histUnpublished && len(s.local) == 0 {
			s.detail = false
		}
	case KeyMsg:
		r, e := s.selected()
		switch {
		case v.Code == input.Rune && v.Rune == 'v' && e != nil:
			return s.verify(c, *e)
		case v.Code == input.Rune && v.Rune == 'l' && e != nil:
			return s.toggleListed(c, *e)
		case v.Code == input.Rune && v.Rune == 'p' && r != nil:
			return s.publishLocal(c, *r)
		case v.Code == input.Rune && v.Rune == 'o' && r != nil:
			rep := qualitybench.ReportPath(r.Path)
			if err := openFile(rep); err != nil {
				c.setFlash("could not open "+rep+": "+err.Error(), true)
			} else {
				c.setFlash("opened "+rep, false)
			}
			return nil
		}
		if s.detail {
			if isBack(v) || v.Code == input.Enter {
				s.detail = false
			}
			return nil
		}
		// tab and ←→ switch between Published and Unpublished.
		if (v.Code == input.Tab && !v.Ctrl) || v.Code == input.Backtab || v.Code == input.Left || v.Code == input.Right {
			s.tab = 1 - s.tab
			return nil
		}
		if s.cur().HandleKey(v) {
			return nil
		}
		switch {
		case isBack(v):
			c.scr = scrHome
		case v.Code == input.Enter && len(s.cur().Rows) > 0:
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
	tabs := []string{fmt.Sprintf(" Published (%d) ", len(s.entries)), fmt.Sprintf(" Unpublished (%d) ", len(s.local))}
	for i := range tabs {
		if i == s.tab {
			tabs[i] = t.R(t.Selected, tabs[i])
		} else {
			tabs[i] = t.R(t.Muted, tabs[i])
		}
	}
	head := strings.Join(tabs, " ")
	sub := t.R(t.Muted, "Pages this key published, from the "+s.source+" record.")
	if s.tab == histUnpublished {
		sub = t.R(t.Muted, "QualityBench reports not published yet, from "+clip(qualitybench.ResultsDir(), 60)+" and rental step folders.")
	}
	if s.err != "" && s.tab == histPublished {
		sub += t.R(t.Warn, "  "+clip(s.err, w-60))
	}
	r, sel := s.selected()
	if s.detail && r != nil {
		lines := []string{head, "", t.R(t.Title, style.Sanitize(orDash(r.Title))), "",
			kv(t, "model", style.Sanitize(r.Model)), kv(t, "run", orDash(whenOf(*r))), kv(t, "results", style.Sanitize(r.Path))}
		if _, err := os.Stat(qualitybench.ReportPath(r.Path)); err == nil {
			lines = append(lines, kv(t, "report", style.Sanitize(qualitybench.ReportPath(r.Path))))
		}
		if data, err := os.ReadFile(r.Path); err == nil {
			lines = append(lines, "")
			for _, l := range qualitybench.Headline(data) {
				lines = append(lines, "  "+style.Sanitize(l))
			}
		}
		lines = append(lines, "", t.R(t.Muted, "Not published. p publishes it as a launch80 page (endpoint and host removed) and asks whether to list it in the Directory."))
		return lines, []widgets.Binding{{Keys: "p", Desc: "publish"}, {Keys: "o", Desc: "open report"}, {Keys: "esc", Desc: "back"}}
	}
	if s.detail && sel != nil {
		e := *sel
		lines := []string{head, "", t.R(t.Title, style.Sanitize(orDash(e.Title))), "",
			kv(t, "url", style.Sanitize(e.URL)), kv(t, "template", e.TemplateID), kv(t, "tier", orDash(e.TrustTier)),
			kv(t, "class", orDash(e.RunClass)), kv(t, "directory", listedText(e)), kv(t, "published", e.CreatedAt), kv(t, "guid", e.GUID)}
		if e.SourcePath != "" {
			lines = append(lines, kv(t, "from file", style.Sanitize(e.SourcePath)))
		}
		if e.SourceSHA256 != "" {
			lines = append(lines, kv(t, "file sha256", e.SourceSHA256[:16]+"…"))
		}
		lines = append(lines, "", t.R(t.Muted, "Anyone with the link can read a page; a listed one is also in the launch80 Directory. A page is self-reported until launch80 verifies it."))
		return lines, []widgets.Binding{{Keys: "v", Desc: "request verification"}, {Keys: "l", Desc: listKeyDesc(e)}, {Keys: "esc", Desc: "back"}}
	}
	out := []string{head, sub, ""}
	keys := []widgets.Binding{{Keys: "tab", Desc: "published / unpublished"}, {Keys: "↑↓", Desc: "move"}, {Keys: "enter", Desc: "details"}}
	if s.tab == histUnpublished {
		if len(s.local) == 0 {
			out = append(out, t.R(t.Muted, "Nothing unpublished. Finished QualityBench runs show here until you publish them."))
		} else {
			out = append(out, s.ltable.View(t, w, h-len(out), true)...)
			keys = append(keys, widgets.Binding{Keys: "p", Desc: "publish"}, widgets.Binding{Keys: "o", Desc: "open report"})
		}
	} else {
		out = append(out, s.table.View(t, w, h-len(out), true)...)
		keys = append(keys, widgets.Binding{Keys: "v", Desc: "verify"}, widgets.Binding{Keys: "l", Desc: "list / unlist"})
	}
	return out, append(keys, widgets.Binding{Keys: "r", Desc: "refresh"}, widgets.Binding{Keys: "esc", Desc: "back"})
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
		return t.R(t.Accent, widgets.Spin(c.tick)+" "+j.label()) + t.R(t.Muted, "  "+elapsed(j)+"  (ctrl+x stops it)")
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
