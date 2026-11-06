package qualitybench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/launch80/L80-CLI/internal/pytool"
)

// Estimate is how long a run should take, as `qualitybench estimate --json`
// prints it (QualityBench 0.2.0+) or, for an older tool, as L80 works it out
// from the reference table.
type Estimate struct {
	// Source is "history" (earlier runs of this model), "probe" (this server,
	// measured just now) or "reference" (the RefMinutes table).
	Source   string         `json:"source"`
	TotalMin float64        `json:"total_min"`
	RangeMin [2]float64     `json:"range_min"`
	Tasks    []TaskEstimate `json:"tasks"`
	Probe    *Probe         `json:"probe"`
	// SetupMin is the one-off setup (harness environments, the LCB image,
	// datasets) the first run pays; 0 once set up.
	SetupMin float64 `json:"setup_min"`
	Basis    string  `json:"basis"`
	Warning  string  `json:"warning,omitempty"`
	// FromTool is true when the tool itself produced the estimate, which also
	// means it understands --yes. It is not part of the tool's JSON.
	FromTool bool `json:"-"`
}

// TaskEstimate is one task's share of an Estimate.
type TaskEstimate struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	Min    float64 `json:"min"`
	Items  int     `json:"items"`
	Source string  `json:"source"`
}

// Probe is the server speed the estimate was scaled by.
type Probe struct {
	DecodeTPS  float64 `json:"decode_tps"`
	PrefillTPS float64 `json:"prefill_tps"`
	// Speed is this server against the reference run: 2 means twice as fast.
	Speed float64 `json:"speed"`
}

// EstimateOptions name the run being estimated.
type EstimateOptions struct {
	Endpoint string
	Model    string
	Scope    Scope
	Thinking string
	// NoProbe skips measuring the server (the reference table, or history).
	NoProbe bool
	APIKey  string
}

// Args is the argv for `qualitybench estimate --json`.
func (o EstimateOptions) Args() []string {
	args := []string{"estimate", "--json", "--endpoint", o.Endpoint, "--model", o.Model}
	if o.Scope.Tier == TierQuick {
		args = append(args, "--quick")
	}
	if len(o.Scope.Tasks) > 0 && !IsDefaultSet(o.Scope.Tasks) {
		args = append(args, "--tasks", strings.Join(o.Scope.Tasks, ","))
	}
	if o.Thinking != "" {
		args = append(args, "--thinking", o.Thinking)
	}
	if o.NoProbe {
		args = append(args, "--no-probe")
	}
	return args
}

// ThinkingFactor is how much longer thinking on takes than off in the
// reference table's absence of a measurement: GSM8K ran about 4x.
const ThinkingFactor = 4.0

// QuickItems is the item count of the quick tier: every tenth item, from the first.
func QuickItems(n int) int { return (n + 9) / 10 }

// StaticEstimate is the reference-table estimate L80 shows when the tool
// cannot estimate (QualityBench before 0.2.0, or no tool yet). Untimed
// tasks count as one minute so they still show.
func StaticEstimate(sc Scope, thinking string) Estimate {
	quick := sc.Tier == TierQuick
	e := Estimate{Source: "reference", Basis: "on the reference run (" + RefRun + ")"}
	f := 1.0
	if thinking == "on" {
		f = ThinkingFactor
		e.Basis += ", ×4 for thinking on"
	}
	for _, id := range sc.Tasks {
		t, _ := Lookup(id)
		m := float64(t.RefMinutes)
		if m == 0 {
			m = 1
		}
		items := t.Items
		if quick {
			m /= 10
			items = QuickItems(items)
		}
		m *= f
		e.Tasks = append(e.Tasks, TaskEstimate{ID: id, Label: t.Label, Min: m, Items: items, Source: "reference"})
		e.TotalMin += m
	}
	// The reference is a slow card; most servers are faster, a thinking
	// model can be slower still.
	e.RangeMin = [2]float64{e.TotalMin * 0.3, e.TotalMin * 1.5}
	return e
}

// ErrNoEstimate means the tool has no estimate command (before 0.2.0).
var ErrNoEstimate = fmt.Errorf("this QualityBench has no estimate command (0.2.0 adds it)")

// EstimateTimeout bounds `qualitybench estimate`: a probe takes under a
// minute once the model is loaded, and the first request may pay the load.
var EstimateTimeout = 90 * time.Second

// RunEstimate asks the tool for an estimate. When the tool cannot (an older
// version, a crash, a timeout) it returns the reference-table estimate with
// Warning set and a nil error: an estimate is always shown.
func RunEstimate(ctx context.Context, r Runner, o EstimateOptions) Estimate {
	e, err := toolEstimate(ctx, r, o)
	if err == nil {
		return e
	}
	s := StaticEstimate(o.Scope, o.Thinking)
	s.Warning = err.Error()
	return s
}

func toolEstimate(ctx context.Context, r Runner, o EstimateOptions) (Estimate, error) {
	if len(r.Argv) == 0 {
		return Estimate{}, fmt.Errorf("QualityBench is not available")
	}
	ctx, cancel := context.WithTimeout(ctx, EstimateTimeout)
	defer cancel()
	var out, errb bytes.Buffer
	env := []string{"QUALITYBENCH_NO_UPDATE_CHECK=1"}
	if o.APIKey != "" {
		env = append(env, "QUALITYBENCH_API_KEY="+o.APIKey)
	}
	code, err := pytool.Exec(ctx, r, o.Args(), pytool.ExecOpts{Stdout: &out, Stderr: &errb, ExtraEnv: env})
	if ctx.Err() == context.DeadlineExceeded {
		return Estimate{}, fmt.Errorf("the estimate took over %s", EstimateTimeout)
	}
	if err != nil {
		return Estimate{}, err
	}
	if code == 2 && strings.Contains(errb.String(), "invalid choice") {
		return Estimate{}, ErrNoEstimate
	}
	if code != 0 {
		return Estimate{}, fmt.Errorf("qualitybench estimate exited %d: %s", code, lastLine(errb.String()))
	}
	e, err := ParseEstimate(out.Bytes())
	if err != nil {
		return Estimate{}, err
	}
	return e, nil
}

// ParseEstimate reads the tool's estimate JSON (the last JSON object in out).
func ParseEstimate(out []byte) (Estimate, error) {
	s := strings.TrimSpace(string(out))
	if i := strings.LastIndex(s, "\n{"); i >= 0 {
		s = s[i+1:]
	}
	var e Estimate
	if err := json.Unmarshal([]byte(s), &e); err != nil {
		return Estimate{}, fmt.Errorf("qualitybench estimate printed no estimate: %v", err)
	}
	if e.Source == "" || len(e.Tasks) == 0 {
		return Estimate{}, fmt.Errorf("qualitybench estimate printed an empty estimate")
	}
	if e.RangeMin == [2]float64{} {
		e.RangeMin = [2]float64{e.TotalMin, e.TotalMin}
	}
	for i := range e.Tasks {
		if e.Tasks[i].Label == "" {
			if t, ok := Lookup(e.Tasks[i].ID); ok {
				e.Tasks[i].Label = t.Label
			}
		}
	}
	e.FromTool = true
	return e, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// EstimatePrefix starts the line `qualitybench run` prints once before the first task.
const EstimatePrefix = "qb:estimate "

// ParseEstimateLine reads a `qb:estimate {...}` line.
func ParseEstimateLine(line string) (Estimate, bool) {
	rest, ok := strings.CutPrefix(line, EstimatePrefix)
	if !ok {
		return Estimate{}, false
	}
	e, err := ParseEstimate([]byte(rest))
	return e, err == nil
}

// Total is the run's minutes including first-run setup.
func (e Estimate) Total() float64 { return e.TotalMin + e.SetupMin }

// Upper is the high end of the range, plus setup: what a limit compares against.
func (e Estimate) Upper() float64 {
	hi := e.RangeMin[1]
	if hi < e.TotalMin {
		hi = e.TotalMin
	}
	return hi + e.SetupMin
}

// TaskMin is one task's estimated minutes, or 0.
func (e Estimate) TaskMin(id string) float64 {
	for _, t := range e.Tasks {
		if t.ID == id {
			return t.Min
		}
	}
	return 0
}

// Headline is "about 38 min (27–57), measured on this server just now".
func (e Estimate) Headline() string {
	s := "about " + Minutes(e.TotalMin)
	lo, hi := e.RangeMin[0], e.RangeMin[1]
	if hi > lo && Minutes(lo) != Minutes(hi) {
		s += " (" + strings.TrimSuffix(Minutes(lo), " min") + "–" + Minutes(hi) + ")"
	}
	if e.Basis != "" {
		s += ", " + e.Basis
	}
	return s
}

// TaskLine is "GSM8K 6 · IFEval 3 · …" in minutes.
func (e Estimate) TaskLine() string {
	var parts []string
	for _, t := range e.Tasks {
		parts = append(parts, fmt.Sprintf("%s %s", t.Label, strings.TrimSuffix(Minutes(t.Min), " min")))
	}
	return strings.Join(parts, " · ")
}

// Minutes formats a duration in minutes: "<1 min", "12 min", "2h41".
func Minutes(m float64) string {
	switch {
	case m < 1:
		return "<1 min"
	case m < 60:
		return fmt.Sprintf("%d min", int(math.Round(m)))
	}
	r := int(math.Round(m))
	return fmt.Sprintf("%dh%02d", r/60, r%60)
}
