package qualitybench

import (
	"encoding/json"
	"math"
	"strings"
	"sync"
	"time"
)

// ProgressPrefix starts the tool's machine-readable progress lines.
const ProgressPrefix = "qb:progress "

// Tracker follows a run from the tool's output: per-task progress, the run's
// overall fraction and the time left. QualityBench 0.2.0 reports its own
// run-level ETA; for older tools (and between its lines) the tracker works
// one out: each task weighs its estimated minutes, the running task's
// remaining items go at the observed rate (an EWMA), and tasks not started
// take their estimate scaled by how finished tasks compared with theirs.
//
// The job goroutine feeds it; the view reads it. It is safe for both.
type Tracker struct {
	mu       sync.Mutex
	order    []string
	rows     map[string]*TaskProgress
	est      Estimate
	hasEst   bool
	run      *RunProgress
	runAt    time.Time
	start    time.Time
	deadline time.Time
	// Now is the clock, swapped in tests.
	Now func() time.Time
}

// TaskProgress is one task's state.
type TaskProgress struct {
	ID                   string
	Done, Total, Correct int
	// State is queued, running, scoring, scored, partial or failed.
	State          string
	Started, Ended time.Time
	// rate is items per second (EWMA) while generating.
	rate     float64
	lastDone int
	lastAt   time.Time
}

// RunProgress is the tool's run-level line (QualityBench 0.2.0+).
type RunProgress struct {
	Task      string  `json:"task"`
	TaskIndex int     `json:"task_index"`
	Tasks     int     `json:"tasks"`
	ElapsedS  float64 `json:"elapsed_s"`
	ETAS      float64 `json:"eta_s"`
	EstTotalS float64 `json:"est_total_s"`
	Fraction  float64 `json:"fraction"`
}

// Overall is the run as a whole.
type Overall struct {
	Fraction float64
	Elapsed  time.Duration
	// Left is the time remaining; Known is false until there is a basis for it.
	Left  time.Duration
	Known bool
	// EstTotal is the estimate the run started with (0 if none).
	EstTotal time.Duration
	// Current is the running task, if any.
	Current *TaskProgress
	// Cut lists the tasks a deadline will stop before they finish.
	Cut []string
	// FromTool is true when Left came from the tool's own ETA.
	FromTool bool
}

// NewTracker follows a run of these tasks, in run order.
func NewTracker(tasks []string) *Tracker {
	t := &Tracker{rows: map[string]*TaskProgress{}, Now: time.Now}
	for _, id := range tasks {
		t.order = append(t.order, id)
		t.rows[id] = &TaskProgress{ID: id, State: "queued"}
	}
	return t
}

// SetEstimate gives the tracker the run's estimate, which weights the tasks.
func (t *Tracker) SetEstimate(e Estimate) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.est, t.hasEst = e, true
}

// Estimate returns the estimate the tracker weights by, if it has one.
func (t *Tracker) Estimate() (Estimate, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.est, t.hasEst
}

// SetDeadline sets the time the run stops starting work.
func (t *Tracker) SetDeadline(d time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.deadline = d
}

// Parse takes one line of the tool's output and reports whether it was a
// progress (or estimate) line, which a log should not show.
func (t *Tracker) Parse(line string) bool {
	line = strings.TrimRight(line, "\r\n")
	if e, ok := ParseEstimateLine(line); ok {
		t.mu.Lock()
		if !t.hasEst || !t.est.FromTool {
			t.est, t.hasEst = e, true
		}
		t.mu.Unlock()
		return true
	}
	if strings.HasPrefix(line, EstimatePrefix) {
		return true
	}
	rest, ok := strings.CutPrefix(line, ProgressPrefix)
	if !ok {
		return false
	}
	var u struct {
		Task     string       `json:"task"`
		Done     *int         `json:"done"`
		Total    *int         `json:"total"`
		Correct  *int         `json:"correct"`
		State    string       `json:"state"`
		ElapsedS *float64     `json:"elapsed_s"`
		Run      *RunProgress `json:"run"`
	}
	if json.Unmarshal([]byte(rest), &u) != nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.Now()
	if t.start.IsZero() {
		t.start = now
	}
	if u.Run != nil {
		t.run, t.runAt = u.Run, now
		return true
	}
	if u.Task == "" {
		return false
	}
	r, ok := t.rows[u.Task]
	if !ok {
		r = &TaskProgress{ID: u.Task, State: "queued"}
		t.rows[u.Task] = r
		t.order = append(t.order, u.Task)
	}
	if r.Started.IsZero() && u.State == "" {
		r.Started = now
		if u.ElapsedS != nil {
			r.Started = now.Add(-time.Duration(*u.ElapsedS * float64(time.Second)))
		}
		r.lastAt, r.lastDone = r.Started, 0
	}
	if u.Total != nil {
		r.Total = *u.Total
	}
	if u.Done != nil {
		if *u.Done > r.lastDone && !r.lastAt.IsZero() {
			if dt := now.Sub(r.lastAt).Seconds(); dt > 0 {
				inst := float64(*u.Done-r.lastDone) / dt
				if r.rate == 0 {
					r.rate = inst
				} else {
					r.rate = 0.3*inst + 0.7*r.rate
				}
			}
			r.lastDone, r.lastAt = *u.Done, now
		}
		r.Done = *u.Done
	}
	if u.Correct != nil {
		r.Correct = *u.Correct
	}
	switch {
	case u.State != "":
		r.State = u.State
	case r.State == "queued":
		r.State = "running"
	}
	switch u.State {
	case "scored", "partial", "failed":
		if u.State == "scored" && r.Total > 0 {
			r.Done = r.Total
		}
		r.Ended = now
		if r.Started.IsZero() {
			r.Started = now
		}
	}
	return true
}

// Rows returns a copy of every task's state, in run order.
func (t *Tracker) Rows() []TaskProgress {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]TaskProgress, 0, len(t.order))
	for _, id := range t.order {
		out = append(out, *t.rows[id])
	}
	return out
}

func finished(state string) bool {
	return state == "scored" || state == "partial" || state == "failed"
}

// weight is a task's estimated minutes: from the estimate, else the
// reference table, else one minute.
func (t *Tracker) weight(id string) float64 {
	if t.hasEst {
		if m := t.est.TaskMin(id); m > 0 {
			return m
		}
	}
	if tk, ok := Lookup(id); ok && tk.RefMinutes > 0 {
		return float64(tk.RefMinutes)
	}
	return 1
}

// Overall works out the run's fraction and time left now.
func (t *Tracker) Overall() Overall {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.Now()
	var o Overall
	if !t.start.IsZero() {
		o.Elapsed = now.Sub(t.start)
	}
	if t.hasEst {
		o.EstTotal = time.Duration(t.est.TotalMin * float64(time.Minute))
	}

	// Fraction: each task's share of the estimated minutes.
	var total, doneW float64
	// ratio: actual ÷ estimated for finished tasks, to correct the rest.
	var actual, estFinished float64
	for _, id := range t.order {
		r := t.rows[id]
		w := t.weight(id)
		total += w
		switch {
		case finished(r.State):
			doneW += w
			if r.State == "scored" && !r.Started.IsZero() && !r.Ended.IsZero() {
				actual += r.Ended.Sub(r.Started).Minutes()
				estFinished += w
			}
		case r.State == "running" || r.State == "scoring":
			doneW += w * taskFrac(r)
			if o.Current == nil {
				c := *r
				o.Current = &c
			}
		}
	}
	if total > 0 {
		o.Fraction = doneW / total
	}
	ratio := 1.0
	if estFinished > 0 && actual > 0 {
		ratio = actual / estFinished
	}

	// Time left: the running task at its observed rate, the rest at their
	// corrected estimates.
	var left float64 // minutes
	known := t.hasEst || estFinished > 0
	for _, id := range t.order {
		r := t.rows[id]
		w := t.weight(id)
		switch {
		case finished(r.State):
		case r.State == "running" || r.State == "scoring":
			byEst := math.Max(0, w*ratio-now.Sub(r.Started).Minutes())
			if r.rate > 0 && r.Total > r.Done && r.State == "running" {
				byRate := float64(r.Total-r.Done) / r.rate / 60
				left += byRate
				known = true
			} else {
				left += byEst
			}
		default:
			left += w * ratio
		}
	}
	o.Left = time.Duration(left * float64(time.Minute))
	o.Known = known

	// The tool's own ETA wins while it is fresh.
	if t.run != nil && now.Sub(t.runAt) < 2*time.Minute {
		since := now.Sub(t.runAt)
		o.Left = max(0, time.Duration(t.run.ETAS*float64(time.Second))-since)
		o.Known, o.FromTool = true, true
		if t.run.Fraction > 0 {
			o.Fraction = t.run.Fraction
		}
		if t.run.EstTotalS > 0 && o.EstTotal == 0 {
			o.EstTotal = time.Duration(t.run.EstTotalS * float64(time.Second))
		}
		if t.run.ElapsedS > 0 {
			o.Elapsed = time.Duration(t.run.ElapsedS*float64(time.Second)) + since
		}
	}
	if o.Fraction > 1 {
		o.Fraction = 1
	}

	// Which tasks a deadline cuts: walk the remaining tasks in order.
	if !t.deadline.IsZero() && o.Known {
		at := now
		for _, id := range t.order {
			r := t.rows[id]
			if finished(r.State) {
				continue
			}
			var m float64
			if r.State == "running" || r.State == "scoring" {
				if r.rate > 0 && r.Total > r.Done {
					m = float64(r.Total-r.Done) / r.rate / 60
				} else {
					m = math.Max(0, t.weight(id)*ratio-now.Sub(r.Started).Minutes())
				}
			} else {
				m = t.weight(id) * ratio
			}
			at = at.Add(time.Duration(m * float64(time.Minute)))
			if at.After(t.deadline) {
				o.Cut = append(o.Cut, id)
			}
		}
	}
	return o
}

func taskFrac(r *TaskProgress) float64 {
	if r.State == "scoring" {
		return 0.95
	}
	if r.Total <= 0 {
		return 0
	}
	f := float64(r.Done) / float64(r.Total)
	// Generation done is not the task done: scoring follows.
	return math.Min(f, 1) * 0.95
}

// Duration formats a time left: "23 min", "1h05", "<1 min".
func Duration(d time.Duration) string { return Minutes(d.Minutes()) }

// Status is the one-line summary the CLI rewrites on a terminal:
// "48% · 23 min left · IFEval 31/55".
func (o Overall) Status() string {
	parts := []string{strings.TrimSpace(percent(o.Fraction))}
	if o.Known {
		parts = append(parts, Duration(o.Left)+" left")
	}
	if c := o.Current; c != nil {
		label := c.ID
		if tk, ok := Lookup(c.ID); ok {
			label = tk.Label
		}
		if c.State == "scoring" {
			parts = append(parts, label+" scoring")
		} else if c.Total > 0 {
			parts = append(parts, label+" "+itoa(c.Done)+"/"+itoa(c.Total))
		} else {
			parts = append(parts, label)
		}
	}
	if len(o.Cut) > 0 {
		parts = append(parts, "deadline cuts "+strings.Join(labels(o.Cut), ", "))
	}
	return strings.Join(parts, " · ")
}

func percent(f float64) string { return itoa(int(math.Floor(f*100))) + "%" }

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func labels(ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id
		if tk, ok := Lookup(id); ok {
			out[i] = tk.Label
		}
	}
	return out
}

// Labels turns task ids into their display labels.
func Labels(ids []string) []string { return labels(ids) }

// TaskLeft is a running or queued task's time left (0 if unknown or done).
func (t *Tracker) TaskLeft(id string) (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r, ok := t.rows[id]
	if !ok || finished(r.State) {
		return 0, false
	}
	if r.State == "running" && r.rate > 0 && r.Total > r.Done {
		return time.Duration(float64(r.Total-r.Done) / r.rate * float64(time.Second)), true
	}
	if r.State == "queued" && t.hasEst {
		return time.Duration(t.weight(id) * float64(time.Minute)), true
	}
	return 0, false
}
