package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/betterbench"
	"github.com/launch80/L80-CLI/internal/pwdet"
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

// verifyScreen is the review side of "Verified by Launch80": every key sees
// the requests other keys made and can reproduce them; verifier keys
// (publisher_keys.is_verifier) stamp them, never their own. Reproducing is
// optional; when the verifier did, the comparison goes with the stamp.
type verifyScreen struct {
	tab         int // 0 queue, 1 my requests
	phase       int // 0 list, 1 detail, 2 reproduce form, 3 reject form
	loading     bool
	unsupported bool
	err         string
	me          *api.Key
	queue, mine []api.Verification
	table       widgets.Table

	cur     *api.Verification
	payload []byte
	plan    app.Plan
	scan    *app.ScanPlan // set when the page is a pwdet scan
	detErr  string
	form    widgets.Form
	reason  widgets.Form
	job     *job
	rep     *api.Reproduction
}

type verifyLoaded struct {
	me          *api.Key
	queue, mine []api.Verification
	err         *api.Error
}

type verifyDetail struct {
	v       *api.Verification
	payload []byte
	plan    app.Plan
	scan    *app.ScanPlan
	err     string
}

type verifyDecided struct {
	v   *api.Verification
	err *api.Error
}

func (s *verifyScreen) load(c *Console) Cmd {
	if !c.d.Config.HasToken() {
		return nil
	}
	s.loading = true
	return func() Msg {
		var out verifyLoaded
		out.me, _ = c.d.Client.Me()
		out.queue, out.err = c.d.Client.Verifications("queue", "requested")
		if out.err == nil {
			out.mine, out.err = c.d.Client.Verifications("mine", "")
		}
		return out
	}
}

func (s *verifyScreen) rows() []api.Verification {
	if s.tab == 1 {
		return s.mine
	}
	return s.queue
}

func (s *verifyScreen) fillTable() {
	s.table = widgets.Table{Cursor: min(s.table.Cursor, max(0, len(s.rows())-1)), Columns: []widgets.Column{
		{Title: "Requested", Width: 16}, {Title: "Status", Width: 10}, {Title: "Class", Width: 6}, {Title: "By", Width: 14}, {Title: "Page"}}}
	for _, v := range s.rows() {
		when := v.RequestedAt
		if len(when) >= 16 {
			when = strings.Replace(when[:16], "T", " ", 1)
		}
		by := v.Requester()
		if v.Mine {
			by = "you"
		}
		s.table.Rows = append(s.table.Rows, []string{orDash(when), v.Status, orDash(v.RunClass), style.Sanitize(orDash(by)), style.Sanitize(orDash(v.Title))})
	}
}

func (s *verifyScreen) open(c *Console, v api.Verification) Cmd {
	s.phase, s.cur, s.payload, s.scan, s.detErr, s.rep, s.job = 1, &v, nil, nil, "", nil, nil
	if r, err := app.LoadReproduction(v.ID); err == nil {
		s.rep = r
	}
	return func() Msg {
		payload, apiErr := c.d.Client.ArtifactPayload(v.ArtifactGUID)
		if apiErr != nil {
			return verifyDetail{v: &v, err: apiErr.Message}
		}
		if pwdet.IsScanPayload(payload) {
			sp, err := app.ScanPlanFrom(payload)
			if err != nil {
				return verifyDetail{v: &v, err: err.Error()}
			}
			return verifyDetail{v: &v, payload: payload, scan: &sp}
		}
		plan, err := app.PlanFrom(payload)
		if err != nil {
			return verifyDetail{v: &v, err: err.Error()}
		}
		return verifyDetail{v: &v, payload: payload, plan: plan}
	}
}

func (s *verifyScreen) startForm(c *Console) {
	if s.scan != nil {
		s.form = widgets.Form{Submit: "Re-scan", Fields: []*widgets.Field{
			{Key: "model", Label: "Model files", Help: "Optional: a local folder with " + s.scan.Subject + ". Empty: pwdet downloads the pinned revision.",
				Input: widgets.TextInput{Placeholder: "(download)"}},
			{Key: "base", Label: "Reference files", Help: "Optional: a local folder with the reference. Empty: pwdet downloads it.",
				Input: widgets.TextInput{Placeholder: "(download)"}},
		}}
		s.phase = 2
		return
	}
	s.form = widgets.Form{Submit: "Reproduce", Fields: []*widgets.Field{
		{Key: "endpoint", Label: "Endpoint", Help: "Your OpenAI-compatible server running the same model, on your hardware. It never leaves this machine.",
			Input: widgets.TextInput{Placeholder: "http://127.0.0.1:8000/v1"}},
		{Key: "model", Label: "Model", Help: "The model name your server answers to.", Input: widgets.TextInput{Placeholder: s.plan.Model}},
		{Key: "apikey", Label: "API key", Help: "Only if your endpoint needs one. Passed by environment, never recorded.", Input: widgets.TextInput{Mask: true, Placeholder: "(none)"}},
	}}
	s.form.Fields[1].Input.Set(s.plan.Model)
	if k := os.Getenv("L80_BETTERBENCH_API_KEY"); k != "" {
		s.form.Fields[2].Input.Set(k)
	}
	s.phase = 2
}

func (s *verifyScreen) reproduce(c *Console) Cmd {
	vals := s.form.Values()
	if s.scan != nil {
		id, payload := s.cur.ID, s.payload
		o := app.ScanOptions{Model: vals["model"], Base: vals["base"]}
		s.phase, s.rep = 1, nil
		s.job = newJob("re-scan " + id)
		return c.start(s.job, func(ctx context.Context, out io.Writer) (any, int, error) {
			rep, apiErr := app.ReproduceScan(ctx, id, payload, o, c.d.Version, app.WriterSink{W: out})
			if apiErr != nil {
				fmt.Fprintf(out, "%s: %s\n%s\n", apiErr.Code, apiErr.Message, apiErr.Remedy)
				return nil, 1, nil
			}
			if err := app.SaveReproduction(id, rep); err != nil {
				fmt.Fprintf(out, "warning: could not save the re-scan: %v\n", err)
			}
			return rep, 0, nil
		})
	}
	if vals["endpoint"] == "" {
		c.setFlash("your endpoint is required", true)
		return nil
	}
	id, payload := s.cur.ID, s.payload
	cwd, _ := os.Getwd()
	o := s.plan.Options(vals["endpoint"], vals["model"], vals["apikey"], fmt.Sprintf("%s/reproduce-%s-%s.json", cwd, id, time.Now().Format("20060102-1504")))
	s.phase, s.rep = 1, nil
	s.job = newJob("reproduce " + id)
	return c.start(s.job, func(ctx context.Context, out io.Writer) (any, int, error) {
		rep, apiErr := app.Reproduce(ctx, payload, o, c.d.Version, app.WriterSink{W: out})
		if apiErr != nil {
			fmt.Fprintf(out, "%s: %s\n%s\n", apiErr.Code, apiErr.Message, apiErr.Remedy)
			return nil, 1, nil
		}
		if err := app.SaveReproduction(id, rep); err != nil {
			fmt.Fprintf(out, "warning: could not save the reproduction: %v\n", err)
		}
		return rep, 0, nil
	})
}

// canStamp explains why this key may not decide on the open request.
func (s *verifyScreen) canStamp() string {
	switch {
	case s.cur == nil:
		return "open a request first"
	case s.cur.Mine:
		return "this is your own request: another verifier has to review it"
	case s.me != nil && !s.me.IsVerifier:
		return "stamping needs a verifier key; you can still reproduce and compare"
	}
	return ""
}

func (s *verifyScreen) decide(c *Console, d api.Decision) Cmd {
	id := s.cur.ID
	return func() Msg {
		v, err := c.d.Client.Decide(id, d)
		return verifyDecided{v, err}
	}
}

func (s *verifyScreen) approve(c *Console) Cmd {
	if why := s.canStamp(); why != "" {
		c.setFlash(why, true)
		return nil
	}
	if s.scan != nil {
		// A scan is stamped only against a re-run within tolerance.
		if s.rep == nil || s.rep.Scan == nil {
			c.setFlash("a scan needs a re-scan before it can be verified: press p", true)
			return nil
		}
		if !s.rep.WithinTolerance {
			c.setFlash("your re-scan differs from the published report: reject it or re-scan", true)
			return nil
		}
	} else if s.payload != nil && s.plan.Class == betterbench.ClassQuick {
		// A quick run is stamped only against a reproduction within tolerance.
		if s.rep == nil {
			c.setFlash("a quick run needs a reproduction before it can be verified: run it on your endpoint", true)
			s.startForm(c)
			return nil
		}
		if !s.rep.WithinTolerance {
			c.setFlash("your reproduction of this quick run is outside tolerance: reject it or reproduce again", true)
			return nil
		}
	}
	title, danger := "Verify", false
	body := "Stamp this run Verified by Launch80 on your review alone?\n\nYou have not reproduced it. Press p first to run it yourself and attach the comparison."
	if s.rep != nil && s.rep.Scan != nil {
		body = "Stamp this scan Verified by Launch80 · " + scanLevel(s.scan) + "scan reproduced?\n\nYour re-scan goes with the stamp:\n" + strings.Join(app.FormatScan(s.rep.Scan), "\n")
	} else if s.rep != nil {
		body = "Stamp this run Verified by Launch80?\n\nYour reproduction goes with the stamp:\n" + strings.Join(app.FormatDeltas(s.rep.Metrics), "\n")
	}
	if s.rep != nil && !s.rep.WithinTolerance {
		title, danger = "Verify despite differences", true
		body += "\n\nSome figures fall outside tolerance. Stamp only if you can explain why."
	}
	c.confirm(&widgets.Dialog{Title: title, Body: body, Buttons: []string{"Verify", "Cancel"}, Choice: 1, Danger: danger}, func(ch int) Cmd {
		if ch != 0 {
			return nil
		}
		return s.decide(c, api.Decision{Decision: "verified", Reproduction: s.rep})
	})
	return nil
}

func (s *verifyScreen) update(c *Console, m Msg) Cmd {
	switch v := m.(type) {
	case verifyLoaded:
		s.loading, s.me, s.err, s.unsupported = false, v.me, "", false
		if v.err != nil {
			if api.NotSupported(v.err) {
				s.unsupported = true
			} else {
				s.err = v.err.Code + ": " + v.err.Message
			}
		}
		s.queue, s.mine = v.queue, v.mine
		s.fillTable()
	case verifyDetail:
		if s.cur != nil && v.v.ID == s.cur.ID {
			s.cur, s.payload, s.plan, s.scan, s.detErr = v.v, v.payload, v.plan, v.scan, v.err
		}
	case verifyDecided:
		if v.err != nil {
			c.setFlash(v.err.Code+": "+v.err.Message, true)
			return nil
		}
		c.setFlash(v.v.Status+": "+orDash(v.v.URL), false)
		s.phase = 0
		return s.load(c)
	case jobDone:
		if v.j == s.job {
			if r, ok := v.result.(*api.Reproduction); ok {
				s.rep = r
			}
		}
	case PasteMsg:
		switch s.phase {
		case 2:
			s.form.HandlePaste(v)
		case 3:
			s.reason.HandlePaste(v)
		}
	case MouseMsg:
		if s.job != nil {
			s.job.log.HandleMouse(v)
		}
	case KeyMsg:
		busy := s.job != nil && s.job.running
		if v.Ctrl && v.Rune == 'x' && busy {
			s.job.cancel()
			return nil
		}
		switch s.phase {
		case 2, 3:
			f := &s.form
			if s.phase == 3 {
				f = &s.reason
			}
			if v.Code == input.Esc {
				s.phase = 1
				return nil
			}
			if _, submit := f.HandleKey(v); submit {
				if s.phase == 2 {
					return s.reproduce(c)
				}
				reason := strings.TrimSpace(s.reason.Get("reason"))
				if reason == "" {
					c.setFlash("say why; the publisher sees it", true)
					return nil
				}
				s.phase = 1
				return s.decide(c, api.Decision{Decision: "rejected", Note: reason})
			}
			return nil
		case 1:
			if s.job != nil && s.job.log.HandleKey(v) {
				return nil
			}
			switch {
			case isBack(v):
				s.phase = 0
			case busy:
			case v.Code == input.Rune && v.Rune == 'p':
				if s.cur.Mine {
					c.setFlash("this is your own request: another key reproduces it", true)
				} else if s.payload == nil {
					c.setFlash("the published run is not loaded yet", true)
				} else {
					s.startForm(c)
				}
			case v.Code == input.Rune && v.Rune == 'a':
				return s.approve(c)
			case v.Code == input.Rune && v.Rune == 'x':
				if why := s.canStamp(); why != "" {
					c.setFlash(why, true)
					return nil
				}
				s.reason = widgets.Form{Submit: "Reject", Fields: []*widgets.Field{{Key: "reason", Label: "Reason", Help: "Why the run does not hold up. The publisher sees this."}}}
				s.phase = 3
			}
			return nil
		}
		if s.table.HandleKey(v) {
			return nil
		}
		switch {
		case isBack(v):
			c.scr = scrHome
		case v.Code == input.Tab || v.Code == input.Right || v.Code == input.Left:
			s.tab = 1 - s.tab
			s.fillTable()
		case v.Code == input.Rune && v.Rune == 'r':
			return s.load(c)
		case v.Code == input.Enter:
			if rows := s.rows(); s.table.Cursor < len(rows) {
				return s.open(c, rows[s.table.Cursor])
			}
		}
	}
	return nil
}

func (s *verifyScreen) view(c *Console, w, h int) ([]string, []widgets.Binding) {
	t := c.th
	var out []string
	switch {
	case !c.d.Config.HasToken():
		return []string{t.R(t.Warn, "Reviewing needs a launch80 key."), t.R(t.Muted, "Set L80_TOKEN or ~/.config/L80/credentials.json, then press r.")}, []widgets.Binding{{Keys: "esc", Desc: "back"}}
	case s.loading && s.phase == 0:
		return []string{t.R(t.Muted, widgets.Spin(c.tick)+" loading…")}, nil
	case s.unsupported:
		return []string{t.R(t.Warn, "Verification is not enabled on this launch80 server yet."), "",
			t.R(t.Muted, "You can still check your own runs: History, then v, or `L80 verify check <results.json>`.")}, []widgets.Binding{{Keys: "r", Desc: "retry"}, {Keys: "esc", Desc: "back"}}
	}
	role := t.R(t.Muted, "your key can reproduce and compare; stamping needs a verifier key")
	if s.me != nil && s.me.IsVerifier {
		role = t.R(t.OK, "verifier") + t.R(t.Muted, ": you can stamp runs; scans and quick runs need your re-run first")
	}
	if s.phase == 0 {
		tabs := []string{fmt.Sprintf(" Queue (%d) ", len(s.queue)), fmt.Sprintf(" My requests (%d) ", len(s.mine))}
		for i := range tabs {
			st := t.Muted
			if i == s.tab {
				st = t.Selected
			}
			tabs[i] = t.R(st, tabs[i])
		}
		out = append(out, strings.Join(tabs, " ")+"   "+role, "")
		if s.err != "" {
			out = append(out, t.R(t.Bad, clip(s.err, w)), "")
		}
		if len(s.rows()) == 0 {
			if s.tab == 0 {
				out = append(out, t.R(t.Muted, "Nothing is waiting for review."))
			} else {
				out = append(out, t.R(t.Muted, "You have not requested verification. Open History, select a BetterBench run or pwdet scan and press v."))
			}
		} else {
			out = append(out, s.table.View(t, w, min(len(s.rows())+2, h-4), true)...)
		}
		return fitW(out, w), []widgets.Binding{{Keys: "↑↓", Desc: "move"}, {Keys: "enter", Desc: "open"}, {Keys: "tab", Desc: "queue / mine"}, {Keys: "r", Desc: "refresh"}, {Keys: "esc", Desc: "back"}}
	}
	if s.phase == 2 || s.phase == 3 {
		f, what := &s.form, "Reproduce "+s.cur.ID+" on your hardware"
		if s.scan != nil {
			what = "Re-scan " + s.cur.ID + " with pwdet on this machine"
		}
		if s.phase == 3 {
			f, what = &s.reason, "Reject "+s.cur.ID
		}
		out = append(out, t.R(t.Title, what), "")
		out = append(out, f.View(t, w, true)...)
		return fitW(out, w), []widgets.Binding{{Keys: "tab", Desc: "next field"}, {Keys: "enter", Desc: "next / submit"}, {Keys: "esc", Desc: "back"}}
	}
	v := s.cur
	by := v.Requester()
	if v.Mine {
		by = "you"
	}
	requested := "   requested " + v.RequestedAt
	if by != "" {
		requested = "   requested by " + style.Sanitize(by) + " " + v.RequestedAt
	}
	out = append(out, t.R(t.Title, style.Sanitize(orDash(v.Title))), t.R(t.Muted, orDash(v.URL)),
		kv(t, "status", v.Status)+t.R(t.Muted, requested))
	if r := v.Reviewer(); r != "" {
		out = append(out, kv(t, "reviewed by", style.Sanitize(r)+t.R(t.Muted, " "+v.ReviewedAt)))
	}
	if v.Note != "" {
		out = append(out, kv(t, "publisher", style.Sanitize(v.Note)))
	}
	if v.DecisionNote != "" {
		out = append(out, kv(t, "verifier", style.Sanitize(v.DecisionNote)))
	}
	for _, ck := range v.Checks() {
		if ck.Status != "pass" {
			out = append(out, t.R(t.Warn, ck.Status+" "+ck.Name+": ")+t.R(t.Muted, ck.Detail))
		}
	}
	out = append(out, "")
	switch {
	case s.detErr != "":
		out = append(out, t.R(t.Bad, "could not load the published run: "+s.detErr))
	case s.payload == nil:
		out = append(out, t.R(t.Muted, widgets.Spin(c.tick)+" loading the published run…"))
	case s.scan != nil:
		sp := s.scan
		out = append(out, t.R(t.Title, "To re-scan"), kv(t, "model", orDash(sp.Subject)))
		for _, r := range sp.References {
			out = append(out, kv(t, "reference", r))
		}
		prof := orDash(sp.Profile)
		if sp.Level != "" && sp.Level != sp.Profile {
			prof += " (" + sp.Level + " level)"
		}
		out = append(out, kv(t, "profile", prof+", pwdet "+sp.ToolVersion),
			kv(t, "published", orDash(sp.Verdict)+t.R(t.Muted, "   digest "+clip(sp.Digest, 12))),
			t.R(t.Muted, "Stamped only after a re-run within tolerance: same file hashes, same deterministic findings, same significant probe findings."))
		if sp.Serve {
			out = append(out, t.R(t.Muted, "This scan probed behaviour: the re-run serves both models on this machine's GPU."))
		}
		for _, ck := range sp.Checks {
			if ck.Status != "pass" {
				out = append(out, t.R(t.Warn, ck.Status+" "+ck.Name+": ")+t.R(t.Muted, ck.Detail))
			}
		}
	default:
		p := s.plan
		out = append(out, t.R(t.Title, "To reproduce"), kv(t, "model", p.Model))
		for _, k := range []string{"engine", "hardware"} {
			if p.Notes[k] != "" {
				out = append(out, kv(t, k, style.Sanitize(p.Notes[k])))
			}
		}
		out = append(out, kv(t, "settings", fmt.Sprintf("BetterBench %s, %d passes, %d warmup, %s", p.Version, p.RunsPerCategory, p.Warmup, strings.Join(p.Phases, " + "))))
		if p.Class == betterbench.ClassQuick {
			out = append(out, kv(t, "class", "quick run")+t.R(t.Muted, "   verified only against a reproduction, on medians"))
		}
		if s.rep == nil {
			out = append(out, "", t.R(t.Title, "Published figures"))
			for _, m := range p.Headline {
				out = append(out, kv(t, "", "")+style.PadRight(m.Label, 24)+m.Value+" "+m.Unit)
			}
		}
	}
	if s.rep != nil && s.rep.Scan != nil {
		verdict := t.R(t.OK, "your re-scan matches the published report")
		if !s.rep.WithinTolerance {
			verdict = t.R(t.Warn, "your re-scan differs from the published report")
		}
		out = append(out, "", t.R(t.Title, "Your re-scan")+"  "+verdict)
		for _, l := range app.FormatScan(s.rep.Scan) {
			out = append(out, "  "+t.R(t.Text, style.Sanitize(l)))
		}
	} else if s.rep != nil {
		verdict := t.R(t.OK, "your run matches the published figures")
		if !s.rep.WithinTolerance {
			verdict = t.R(t.Warn, "some figures differ by more than the tolerance")
		}
		out = append(out, "", t.R(t.Title, "Your reproduction")+"  "+verdict)
		for i, l := range app.FormatDeltas(s.rep.Metrics) {
			st := t.Text
			if i == 0 {
				st = t.Muted
			} else if !s.rep.Metrics[i-1].Within {
				st = t.Warn
			}
			out = append(out, "  "+t.R(st, l))
		}
	}
	out = append(out, "", role)
	if s.job != nil {
		out = append(out, "", jobHeader(c, s.job, w))
		out = append(out, s.job.log.View(t, w, max(3, h-len(out)))...)
	}
	pk := "reproduce"
	if s.scan != nil {
		pk = "re-scan"
	}
	keys := []widgets.Binding{{Keys: "p", Desc: pk}, {Keys: "a", Desc: "verify"}, {Keys: "x", Desc: "reject"}, {Keys: "esc", Desc: "back"}}
	if v.Mine {
		keys = []widgets.Binding{{Keys: "esc", Desc: "back"}}
	}
	return fitW(out, w), keys
}

// scanLevel is "Standard " for a Standard scan, "" when unknown.
func scanLevel(sp *app.ScanPlan) string {
	if sp == nil || sp.Level == "" {
		return ""
	}
	return strings.ToUpper(sp.Level[:1]) + sp.Level[1:] + " "
}

func fitW(lines []string, w int) []string {
	for i := range lines {
		lines[i] = style.Truncate(lines[i], w, "…")
	}
	return lines
}
