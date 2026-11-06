package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/qualitybench"
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/vast"
)

var probeEst = qualitybench.Estimate{Source: "probe", TotalMin: 38, RangeMin: [2]float64{27, 57}, FromTool: true,
	Basis: "measured on this server just now",
	Tasks: []qualitybench.TaskEstimate{{ID: "gsm8k", Label: "GSM8K", Min: 6}, {ID: "ifeval", Label: "IFEval", Min: 3}}}

// estimatingQuality is the QualityBench screen with a tool that estimates
// 38 min and a run function that never starts a real job.
func estimatingQuality(t *testing.T) (*Console, *int) {
	t.Helper()
	c := openQuality(t, swap)
	calls := 0
	c.qb.estimateFn = func(_ context.Context, o qualitybench.RunOptions, noProbe bool) qualitybench.Estimate {
		if !noProbe {
			calls++
		}
		return probeEst
	}
	t.Setenv("L80_QUALITYBENCH", "/nonexistent/qualitybench")
	return c, &calls
}

func TestApprovalDialogShowsTheEstimate(t *testing.T) {
	c, calls := estimatingQuality(t)
	c.qb.focus = qsSubmit
	feed(c, code(input.Enter))
	if *calls != 1 || c.dialog == nil || c.dialog.Title != "Start QualityBench?" {
		t.Fatalf("calls %d dialog %+v", *calls, c.dialog)
	}
	body := c.dialog.Body
	for _, want := range []string{"Launch80/Qwopus-Int4-baked on http://127.0.0.1:8000/v1 · full · thinking off",
		"About 38 min (27–57 min), measured on this server just now", "GSM8K 6 · IFEval 3"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	if got := strings.Join(c.dialog.Buttons, "|"); got != "Start|Start, don't ask again|Cancel" {
		t.Errorf("buttons %s", got)
	}
	feed(c, code(input.Esc)) // Cancel
	if c.qb.job != nil || c.dialog != nil {
		t.Error("cancel started a run")
	}
}

func TestDontAskAgainSavesNeverAndStarts(t *testing.T) {
	c, _ := estimatingQuality(t)
	c.qb.focus = qsSubmit
	feed(c, code(input.Enter))
	feed(c, code(input.Right))
	feed(c, code(input.Enter))
	if v, src := config.Get(config.KeyQualityConfirm); v != "never" || src != "file" {
		t.Errorf("setting %s (%s)", v, src)
	}
	if c.qb.job == nil || c.qb.job.status == nil {
		t.Fatal("no run started")
	}
	waitJob(t, c.qb.job)
	// Next time: no dialog.
	c.qb.phase = 0
	c.qb.focus = qsSubmit
	c.qb.job = nil
	feed(c, code(input.Enter))
	if c.dialog != nil || c.qb.job == nil {
		t.Errorf("asked again: %+v", c.dialog)
	}
	waitJob(t, c.qb.job)
}

func TestOverMinutesSkipsShortRuns(t *testing.T) {
	c, _ := estimatingQuality(t)
	config.Set(config.KeyQualityConfirm, "over:1h") // upper bound 57 min
	c.qb.focus = qsSubmit
	feed(c, code(input.Enter))
	if c.dialog != nil || c.qb.job == nil {
		t.Fatalf("dialog %+v", c.dialog)
	}
	waitJob(t, c.qb.job)
}

func TestScopeRowUsesTheToolsEstimate(t *testing.T) {
	c, calls := estimatingQuality(t)
	c.qb.scopeKey = ""        // forget the estimate made while the screen opened
	feed(c, code(input.Down)) // any key refreshes the quiet estimate
	if v := screen(t, c); !strings.Contains(v, "9 tasks · about 38 min (27–57 min), measured on") {
		t.Errorf("scope row:\n%s", v)
	}
	if *calls != 0 {
		t.Error("the quiet estimate probed the server")
	}
}

func TestOverallBarAndJobTitle(t *testing.T) {
	p := newQBProgress([]string{"gsm8k", "ifeval"})
	p.tr.SetEstimate(probeEst)
	p.parse(`qb:progress {"task":"gsm8k","state":"scored","correct":5,"total":10}`)
	p.parse(`qb:progress {"task":"ifeval","done":3,"total":10}`)
	c := newTestConsole(t)
	lines := strings.Join(p.view(c.th, 110), "\n")
	for _, want := range []string{"Overall", "left", "GSM8K", "scored 50.0%", "IFEval", "3/10", "running"} {
		if !strings.Contains(lines, want) {
			t.Errorf("missing %q:\n%s", want, lines)
		}
	}
	j := newJob("QualityBench M")
	j.running, j.status = true, p.status
	if l := j.label(); !strings.HasPrefix(l, "QualityBench M · 7") || !strings.Contains(l, "left") {
		t.Errorf("label %q", l)
	}
	p.tr.SetDeadline(time.Now())
	if lines := strings.Join(p.view(c.th, 110), "\n"); !strings.Contains(lines, "the deadline will cut IFEval") {
		t.Errorf("no deadline warning:\n%s", lines)
	}
}

func TestRentStatusNamesTheQualityStep(t *testing.T) {
	p := newRentProgress([]vast.Step{{Type: vast.StepBetterBench}, {Type: vast.StepQualityBench, Quick: true}})
	p.parse(`bbvastl80:step {"index": 2, "type": "qualitybench", "state": "running", "label": "02-quality"}`)
	p.parse(`qb:estimate {"source":"probe","total_min":10,"tasks":[{"id":"gsm8k","min":10}]}`)
	p.parse(`qb:progress {"task":"gsm8k","done":5,"total":132}`)
	if s := p.status(); !strings.HasPrefix(s, "step 2/2 · QualityBench ") {
		t.Errorf("status %q", s)
	}
	if e, ok := p.qb.tr.Estimate(); !ok || e.TotalMin != 10 {
		t.Error("the rental view missed the qb:estimate line")
	}
}

func TestSettingsCyclesConfirm(t *testing.T) {
	c := newTestConsole(t)
	feed(c, SizeMsg{110, 40})
	jumpTo(t, c, scrSettings)
	for range 4 {
		feed(c, code(input.Down))
	}
	want := []string{"over:30m", "never", "always"}
	for _, w := range want {
		feed(c, code(input.Enter))
		if v, _ := config.Get(config.KeyQualityConfirm); v != w {
			t.Fatalf("after enter: %s, want %s", v, w)
		}
	}
	if v := view(t, c); !strings.Contains(v, "Now always: shows the estimate and asks") {
		t.Errorf("view:\n%s", v)
	}
}

func waitJob(t *testing.T, j *job) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for j != nil && j.running && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
}
