package qualitybench

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func line(task string, f string, a ...any) string {
	return ProgressPrefix + fmt.Sprintf(`{"task":%q,`+f+`}`, append([]any{task}, a...)...)
}

// A synthetic run where every task goes twice as slowly as estimated: the
// time left must converge on the truth as tasks finish.
func TestOverallConvergesOnASlowerServer(t *testing.T) {
	c := &clock{time.Unix(1_000_000, 0)}
	tr := NewTracker([]string{"gsm8k", "ifeval", "mmlu_pro"})
	tr.Now = c.now
	tr.SetEstimate(Estimate{Source: "probe", TotalMin: 30, Tasks: []TaskEstimate{
		{ID: "gsm8k", Min: 10}, {ID: "ifeval", Min: 5}, {ID: "mmlu_pro", Min: 15}}})
	// Truth: 20, 10 and 30 minutes; 100 items each.
	truth := map[string]float64{"gsm8k": 20, "ifeval": 10, "mmlu_pro": 30}
	start := c.t
	var errs []float64
	for _, id := range []string{"gsm8k", "ifeval", "mmlu_pro"} {
		step := time.Duration(truth[id] / 10 * float64(time.Minute))
		tr.Parse(line(id, `"done":0,"total":100`))
		for d := 10; d <= 100; d += 10 {
			c.add(step)
			tr.Parse(line(id, `"done":%d,"total":100`, d))
			o := tr.Overall()
			trueLeft := 60 - c.t.Sub(start).Minutes()
			errs = append(errs, math.Abs(o.Left.Minutes()-trueLeft))
		}
		tr.Parse(line(id, `"state":"scored","correct":50,"total":100`))
	}
	// Early on the estimate is off by about the slowdown; by the last task
	// the error must be small.
	if last := errs[len(errs)-5]; last > 3 {
		t.Errorf("time left still off by %.1f min near the end (errors %v)", last, errs)
	}
	if errs[0] < errs[len(errs)-5] {
		t.Errorf("did not improve: %v", errs)
	}
	if o := tr.Overall(); o.Fraction != 1 || o.Left != 0 {
		t.Errorf("finished run: %+v", o)
	}
}

func TestOverallWeightsByEstimatedMinutes(t *testing.T) {
	c := &clock{time.Unix(0, 0)}
	tr := NewTracker([]string{"bfcl", "mmlu_pro"})
	tr.Now = c.now
	tr.SetEstimate(Estimate{TotalMin: 50, Tasks: []TaskEstimate{{ID: "bfcl", Min: 5}, {ID: "mmlu_pro", Min: 45}}})
	tr.Parse(line("bfcl", `"state":"scored","correct":1,"total":4`))
	if f := tr.Overall().Fraction; math.Abs(f-0.1) > 1e-9 {
		t.Errorf("fraction %v, want 0.1", f)
	}
}

func TestToolETAWinsWhileFresh(t *testing.T) {
	c := &clock{time.Unix(0, 0)}
	tr := NewTracker([]string{"gsm8k"})
	tr.Now = c.now
	tr.Parse(ProgressPrefix + `{"run":{"task":"gsm8k","task_index":1,"tasks":1,"elapsed_s":60,"eta_s":600,"est_total_s":660,"fraction":0.1}}`)
	c.add(30 * time.Second)
	o := tr.Overall()
	if !o.FromTool || o.Left != 570*time.Second || o.Fraction != 0.1 {
		t.Errorf("%+v", o)
	}
	c.add(5 * time.Minute) // stale: back to L80's own figure
	if tr.Overall().FromTool {
		t.Error("a stale tool ETA still wins")
	}
}

func TestDeadlineNamesTheTasksItCuts(t *testing.T) {
	c := &clock{time.Unix(0, 0)}
	tr := NewTracker([]string{"gsm8k", "ifeval", "mmlu_pro"})
	tr.Now = c.now
	tr.SetEstimate(Estimate{TotalMin: 60, Tasks: []TaskEstimate{{ID: "gsm8k", Min: 10}, {ID: "ifeval", Min: 10}, {ID: "mmlu_pro", Min: 40}}})
	tr.SetDeadline(c.t.Add(25 * time.Minute))
	o := tr.Overall()
	if strings.Join(o.Cut, ",") != "mmlu_pro" || !strings.Contains(o.Status(), "deadline cuts MMLU-Pro") {
		t.Errorf("cut %v, status %q", o.Cut, o.Status())
	}
}

func TestEstimateLineIsNotLogged(t *testing.T) {
	tr := NewTracker([]string{"gsm8k"})
	if !tr.Parse(EstimatePrefix + `{"source":"probe","total_min":5,"tasks":[{"id":"gsm8k","min":5}]}`) {
		t.Fatal("estimate line not taken")
	}
	if e, ok := tr.Estimate(); !ok || e.Source != "probe" || e.Tasks[0].Label != "GSM8K" {
		t.Errorf("%+v", e)
	}
	if tr.Parse("[2026] gsm8k: start") {
		t.Error("a log line was taken")
	}
}

func TestStaticEstimate(t *testing.T) {
	e := StaticEstimate(Scope{Tier: TierQuick, Tasks: DefaultIDs()}, "")
	if e.Source != "reference" || len(e.Tasks) != 9 || e.Tasks[0].Items != 132 || math.Abs(e.TaskMin("gsm8k")-1.9) > 1e-9 {
		t.Errorf("%+v", e)
	}
	on := StaticEstimate(Scope{Tier: TierFull, Tasks: []string{"gsm8k"}}, "on")
	if on.TotalMin != 76 || !strings.Contains(on.Basis, "thinking on") {
		t.Errorf("%+v", on)
	}
	if h := on.Headline(); !strings.HasPrefix(h, "about 1h16 (23–1h54)") {
		t.Errorf("headline %q", h)
	}
}

func TestParseEstimateSkipsLogLines(t *testing.T) {
	e, err := ParseEstimate([]byte("probing…\n" + `{"source":"history","total_min":12,"range_min":[10,14],"tasks":[{"id":"ifeval","min":12}]}`))
	if err != nil || !e.FromTool || e.Source != "history" || e.Upper() != 14 {
		t.Errorf("%+v %v", e, err)
	}
	if _, err := ParseEstimate([]byte("Traceback")); err == nil {
		t.Error("garbage parsed")
	}
}
