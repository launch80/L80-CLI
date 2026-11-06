package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	l80skills "github.com/launch80/L80-CLI"
	"github.com/launch80/L80-CLI/internal/skillinstall"
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

// Skills: what `L80 skills targets` lists, and `L80 skills link` for each
// agent harness (an installed copy, or a --dev symlink to a checkout).

const scrSkills screenID = "skills"

func init() {
	register(screenDef{id: scrSkills, label: "Skills", side: true, blurb: "Teach your AI agents to drive L80: install the skills per harness.",
		get: func(c *Console) screenModel { return skillsOf(c) }, open: func(c *Console) Cmd { skillsOf(c).load(); return nil }})
}

type skillTarget struct {
	t        skillinstall.Target
	dir      string
	detected bool
	states   map[string]skillinstall.State
}

type skillsScreen struct {
	table   widgets.Table
	targets []skillTarget
	msg     string
	bad     bool
}

func skillsOf(c *Console) *skillsScreen { return stateOf[skillsScreen](c, scrSkills) }

// load reads every harness's skills directory, as `L80 skills targets` does.
func (s *skillsScreen) load() {
	s.targets = nil
	for _, t := range skillinstall.Targets {
		d, err := t.Dir()
		if err != nil {
			continue
		}
		st := skillTarget{t: t, dir: d, detected: t.Detected(), states: map[string]skillinstall.State{}}
		for _, n := range l80skills.SkillNames {
			st.states[n] = skillinstall.Status(n, filepath.Join(d, n))
		}
		s.targets = append(s.targets, st)
	}
	cur := s.table.Cursor
	s.table = widgets.Table{Columns: []widgets.Column{{Title: "Target", Width: 12}, {Title: "Found", Width: 9}, {Title: "Skills", Width: 26}, {Title: "Directory"}}, Cursor: cur}
	for _, st := range s.targets {
		found := "no"
		if st.detected {
			found = "detected"
		}
		s.table.Rows = append(s.table.Rows, []string{st.t.Name, found, skillSummary(st.states), st.dir})
	}
	s.table.Cursor = min(cur, max(0, len(s.table.Rows)-1))
}

// skillSummary counts states in a fixed order: "3 current, 1 outdated".
func skillSummary(states map[string]skillinstall.State) string {
	counts := map[skillinstall.State]int{}
	for _, st := range states {
		counts[st]++
	}
	var parts []string
	for _, st := range []skillinstall.State{skillinstall.Current, skillinstall.Outdated, skillinstall.Linked, skillinstall.NotInstalled} {
		if n := counts[st]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, strings.ReplaceAll(string(st), "_", " ")))
		}
	}
	return strings.Join(parts, ", ")
}

// installSkills copies every bundled skill into dir, refreshing what is
// there and leaving a --dev symlink alone. It returns what happened.
func installSkills(dir string) (string, bool) {
	var failed []string
	for _, n := range l80skills.SkillNames {
		dest := filepath.Join(dir, n)
		if skillinstall.Status(n, dest) == skillinstall.Linked {
			continue
		}
		if err := skillinstall.Install(n, dest, skillinstall.Opts{Force: true}); err != nil && !errors.Is(err, skillinstall.ErrExists) {
			failed = append(failed, n+": "+err.Error())
		}
	}
	if len(failed) > 0 {
		return strings.Join(failed, "; "), true
	}
	return fmt.Sprintf("installed %d skills", len(l80skills.SkillNames)), false
}

// linkSkills symlinks every skill in src (a checkout's skills/ directory)
// into dir, replacing installed copies, as `L80 skills link --dev` does.
func linkSkills(dir, src string) (string, bool) {
	var failed []string
	for _, n := range l80skills.SkillNames {
		if err := skillinstall.Install(n, filepath.Join(dir, n), skillinstall.Opts{Force: true, DevSource: filepath.Join(src, n)}); err != nil {
			failed = append(failed, n+": "+err.Error())
		}
	}
	if len(failed) > 0 {
		return strings.Join(failed, "; "), true
	}
	return fmt.Sprintf("linked %d skills to %s", len(l80skills.SkillNames), src), false
}

// devSkills finds a skills/ checkout under the working directory, the
// default source of `L80 skills link --dev`.
func devSkills() (string, bool) {
	wd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	src := filepath.Join(wd, "skills")
	if _, err := os.Stat(filepath.Join(src, "l80", "SKILL.md")); err != nil {
		return "", false
	}
	return src, true
}

func (s *skillsScreen) selected() (*skillTarget, bool) {
	if s.table.Cursor < 0 || s.table.Cursor >= len(s.targets) {
		return nil, false
	}
	return &s.targets[s.table.Cursor], true
}

func (s *skillsScreen) update(c *Console, m Msg) Cmd {
	k, ok := m.(KeyMsg)
	if !ok {
		return nil
	}
	if s.table.HandleKey(k) {
		return nil
	}
	switch {
	case isBack(k):
		c.scr = scrHome
	case k.Code == input.Rune && k.Rune == 'r':
		s.load()
	case k.Code == input.Enter || (k.Code == input.Rune && k.Rune == 'i'):
		if st, ok := s.selected(); ok {
			s.msg, s.bad = installSkills(st.dir)
			s.msg = st.t.Name + ": " + s.msg
			s.load()
		}
	case k.Code == input.Rune && k.Rune == 'l':
		st, ok := s.selected()
		if !ok {
			return nil
		}
		src, ok := devSkills()
		if !ok {
			c.setFlash("linking needs an L80-CLI checkout: start the console from it, or run `L80 skills link --from <dir>`", true)
			return nil
		}
		c.confirm(&widgets.Dialog{Title: "Link skills", Body: "Replace the skills in " + st.dir + " with symlinks to " + src + "?\n\nEdits in the checkout are then live; `L80 update` leaves links alone.",
			Buttons: []string{"Link", "Cancel"}, Choice: 1}, func(ch int) Cmd {
			if ch == 0 {
				s.msg, s.bad = linkSkills(st.dir, src)
				s.msg = st.t.Name + ": " + s.msg
				s.load()
			}
			return nil
		})
	}
	return nil
}

func (s *skillsScreen) view(c *Console, w, h int) ([]string, []widgets.Binding) {
	t := c.th
	if s.targets == nil {
		s.load()
	}
	out := s.table.View(t, w, min(len(s.table.Rows)+2, h/2), true)
	if st, ok := s.selected(); ok {
		out = append(out, "", t.R(t.Title, st.t.Harness))
		nameW := 0
		for _, n := range l80skills.SkillNames {
			nameW = max(nameW, len(n)+2)
		}
		for _, n := range l80skills.SkillNames {
			state := st.states[n]
			v := t.R(t.Muted, strings.ReplaceAll(string(state), "_", " "))
			switch state {
			case skillinstall.Current, skillinstall.Linked:
				v = t.R(t.OK, string(state))
			case skillinstall.Outdated:
				v = t.R(t.Warn, string(state)) + t.R(t.Muted, " (i refreshes it)")
			}
			out = append(out, "  "+t.R(t.Muted, style.PadRight(n, nameW))+v)
		}
	}
	if s.msg != "" {
		st := t.OK
		if s.bad {
			st = t.Bad
		}
		out = append(out, "", t.R(st, style.Sanitize(s.msg)))
	}
	out = append(out, "", t.R(t.Muted, "Agents pick up new skills on their next session start."),
		t.R(t.Muted, "No skills support? Paste `L80 skills agents-md` into AGENTS.md or CLAUDE.md."))
	for i := range out {
		out[i] = style.Truncate(out[i], w, "…")
	}
	return style.Fit(out, w, h), []widgets.Binding{{Keys: "↑↓", Desc: "move"}, {Keys: "enter/i", Desc: "install"}, {Keys: "l", Desc: "link a checkout"}, {Keys: "r", Desc: "refresh"}, {Keys: "esc", Desc: "back"}}
}
