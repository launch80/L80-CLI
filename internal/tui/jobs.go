package tui

import (
	"fmt"

	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

// Jobs: every run started from any screen, running or finished, newest
// first. A job keeps its log here after its screen has moved on.

const scrJobs screenID = "jobs"

func init() {
	register(screenDef{id: scrJobs, label: "Jobs", side: true, blurb: "Every run from every screen, with its log. Finished runs keep theirs.",
		get: func(c *Console) screenModel { return jobsOf(c) }})
}

type jobsScreen struct {
	cursor int
	detail bool
}

func jobsOf(c *Console) *jobsScreen { return stateOf[jobsScreen](c, scrJobs) }

// newestFirst is c.jobs in the order the screen lists them.
func newestFirst(c *Console) []*job {
	out := make([]*job, 0, len(c.jobs))
	for i := len(c.jobs) - 1; i >= 0; i-- {
		out = append(out, c.jobs[i])
	}
	return out
}

func (s *jobsScreen) selected(c *Console) (*job, bool) {
	js := newestFirst(c)
	s.cursor = max(0, min(s.cursor, len(js)-1))
	if len(js) == 0 {
		return nil, false
	}
	return js[s.cursor], true
}

// jobState is the short status column: running, ok, exit N, or failed.
func jobState(j *job) string {
	switch {
	case j.running:
		return "running"
	case j.err != nil:
		return "failed"
	case j.code != 0:
		return fmt.Sprintf("exit %d", j.code)
	}
	return "ok"
}

// jobLine is one job in brief, for Home.
func jobLine(c *Console, j *job) string {
	t := c.th
	mark := t.R(t.OK, "✓")
	switch st := jobState(j); st {
	case "running":
		mark = t.R(t.Accent, widgets.Spin(c.tick))
	case "ok":
	default:
		mark = t.R(t.Bad, "✗") + t.R(t.Muted, " "+st)
	}
	return mark + " " + style.Sanitize(j.title) + t.R(t.Muted, "  "+elapsed(j))
}

func (s *jobsScreen) stop(c *Console, j *job) {
	if !j.running || j.cancel == nil {
		return
	}
	c.confirm(&widgets.Dialog{Title: "Stop job", Body: "Stop " + j.title + "?\n\nA rental run tears its instance down when stopped.",
		Buttons: []string{"Stop", "Keep running"}, Choice: 1, Danger: true}, func(ch int) Cmd {
		if ch == 0 && j.running {
			j.cancel()
		}
		return nil
	})
}

func (s *jobsScreen) update(c *Console, m Msg) Cmd {
	switch v := m.(type) {
	case MouseMsg:
		if j, ok := s.selected(c); ok && s.detail {
			j.log.HandleMouse(v)
		}
	case KeyMsg:
		j, ok := s.selected(c)
		if ok && ((v.Ctrl && v.Rune == 'x') || (v.Code == input.Rune && v.Rune == 'x' && !v.Ctrl)) {
			s.stop(c, j)
			return nil
		}
		if s.detail {
			switch {
			case isBack(v):
				s.detail = false
			case ok:
				j.log.HandleKey(v)
			}
			return nil
		}
		n := len(c.jobs)
		switch {
		case isBack(v):
			c.scr = scrHome
		case n == 0:
		case v.Code == input.Up || (v.Code == input.Rune && v.Rune == 'k'):
			s.cursor = max(0, s.cursor-1)
		case v.Code == input.Down || (v.Code == input.Rune && v.Rune == 'j'):
			s.cursor = min(n-1, s.cursor+1)
		case v.Code == input.Home:
			s.cursor = 0
		case v.Code == input.End:
			s.cursor = n - 1
		case v.Code == input.Enter:
			s.detail = true
		}
	}
	return nil
}

func (s *jobsScreen) view(c *Console, w, h int) ([]string, []widgets.Binding) {
	t := c.th
	j, ok := s.selected(c)
	if !ok {
		return []string{t.R(t.Muted, "Nothing has run yet. Runs started from any screen show up here, with their logs.")},
			[]widgets.Binding{{Keys: "esc", Desc: "back"}}
	}
	stop := widgets.Binding{Keys: "x", Desc: "stop"}
	if s.detail {
		out := []string{jobHeader(c, j, w), ""}
		out = append(out, j.log.View(t, w, max(3, h-len(out)))...)
		keys := []widgets.Binding{{Keys: "pgup pgdn", Desc: "scroll"}, {Keys: "end", Desc: "follow"}}
		if j.running {
			keys = append(keys, stop)
		}
		return out, append(keys, widgets.Binding{Keys: "esc", Desc: "jobs"})
	}
	tb := widgets.Table{Columns: []widgets.Column{{Title: "State", Width: 9}, {Title: "Started", Width: 8}, {Title: "Elapsed", Width: 8, Right: true}, {Title: "Job"}}, Cursor: s.cursor}
	for _, j := range newestFirst(c) {
		tb.Rows = append(tb.Rows, []string{jobState(j), j.started.Format("15:04:05"), elapsed(j), style.Sanitize(j.title)})
	}
	running := 0
	for _, j := range c.jobs {
		if j.running {
			running++
		}
	}
	head := t.R(t.Muted, fmt.Sprintf("%d job(s), %d running", len(c.jobs), running))
	tableH := min(len(tb.Rows)+2, max(4, h/2))
	out := append([]string{head, ""}, tb.View(t, w, tableH, true)...)
	// A preview of the selected job's last lines.
	if rest := h - len(out) - 2; rest > 2 {
		out = append(out, "", jobHeader(c, j, w))
		out = append(out, j.log.View(t, w, rest-1)...)
	}
	keys := []widgets.Binding{{Keys: "↑↓", Desc: "move"}, {Keys: "enter", Desc: "log"}}
	if j.running {
		keys = append(keys, stop)
	}
	return out, append(keys, widgets.Binding{Keys: "esc", Desc: "back"})
}
