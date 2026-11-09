package tui

import (
	"fmt"

	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

// Runs started this session, for Home. The list lives in memory only: a
// finished QualityBench run is found again on disk by History.

// newestFirst is c.jobs, newest first.
func newestFirst(c *Console) []*job {
	out := make([]*job, 0, len(c.jobs))
	for i := len(c.jobs) - 1; i >= 0; i-- {
		out = append(out, c.jobs[i])
	}
	return out
}

// jobState is the short status: running, ok, exit N, or failed.
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
	return mark + " " + style.Sanitize(j.label()) + t.R(t.Muted, "  "+elapsed(j))
}
