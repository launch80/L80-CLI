package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/betterbench"
	"github.com/launch80/L80-CLI/internal/history"
)

// A verifier stamps a page only after running the benchmark themselves:
// same model, same BetterBench settings, their own endpoint. This file turns
// a published results.json into that run and compares the two.

// Plan is how to reproduce a published run.
type Plan struct {
	Model           string
	Notes           map[string]string
	RunsPerCategory int
	Warmup          int
	Phases          []string // the phases the published run has data for
	Version         string   // BetterBench version it was measured with
	Class           string   // betterbench.RunClass of the published run
	Headline        []betterbench.Metric
}

// PlanFrom reads a published BetterBench payload.
func PlanFrom(payload []byte) (Plan, error) {
	r, err := betterbench.ParsePublished(payload)
	if err != nil {
		return Plan{}, fmt.Errorf("the published page is not a BetterBench run: %w", err)
	}
	p := Plan{Model: r.Env.Model, Notes: r.Env.Notes, RunsPerCategory: r.Config.RunsPerCategory, Warmup: r.Config.Warmup, Version: r.BetterBenchVersion,
		Class: betterbench.RunClass(r.Config)}
	if len(r.SingleStream) > 0 {
		p.Phases = append(p.Phases, "decode")
	}
	if len(r.Prefill) > 0 {
		p.Phases = append(p.Phases, "prefill")
	}
	if len(r.Concurrency) > 0 {
		p.Phases = append(p.Phases, "concurrency")
	}
	if built, err := betterbench.Build(r, betterbench.Options{}); err == nil {
		p.Headline = built.Headline
	}
	return p, nil
}

// Options turns the plan into a BetterBench run against the verifier's
// endpoint. model overrides the published model name when the verifier's
// server answers to a different one.
func (p Plan) Options(endpoint, model, apiKey, out string) betterbench.RunOptions {
	if model == "" {
		model = p.Model
	}
	o := betterbench.RunOptions{Endpoint: endpoint, Model: model, Out: out, Passes: p.RunsPerCategory, Warmup: p.Warmup, APIKey: apiKey,
		Notes: []string{"reproduces=" + p.Model}}
	if len(p.Phases) > 0 && len(p.Phases) < 3 {
		o.Phases = p.Phases
	}
	return o
}

type tol struct {
	prefix string
	pct    float64
}

// tolerances are how far a reproduced headline figure may drift from the
// published one and still count as the same result. Latency varies more
// than throughput between runs, so it gets more room; a quick run's few
// passes vary more again.
var tolerances = map[string][]tol{
	betterbench.ClassFull:  {{"Combined decode", 10}, {"Combined ITL", 15}, {"Combined TTFT", 25}, {"Peak aggregate", 15}, {"Prefill", 20}},
	betterbench.ClassQuick: {{"Combined decode", 15}, {"Combined ITL", 25}, {"Combined TTFT", 40}, {"Peak aggregate", 25}, {"Prefill", 30}},
}

func tolerance(class, label string) float64 {
	for _, t := range tolerances[class] {
		if strings.HasPrefix(label, t.prefix) {
			return t.pct
		}
	}
	if class == betterbench.ClassQuick {
		return 25
	}
	return 15
}

// percentile reports a tail figure (p95, p99, a 1% low). A quick run's few
// passes cannot hold one, so a quick reproduction compares medians only.
func percentile(label string) bool {
	return strings.Contains(label, " p9") || strings.Contains(label, "% low")
}

func number(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", ""), 64)
	return f, err == nil
}

// Compare lines up the headline figures both runs report.
func Compare(published, reproduced []byte) ([]api.MetricDelta, bool, error) {
	pp, err := PlanFrom(published)
	if err != nil {
		return nil, false, err
	}
	rp, err := PlanFrom(reproduced)
	if err != nil {
		return nil, false, fmt.Errorf("your run: %w", err)
	}
	mine := map[string]betterbench.Metric{}
	for _, m := range rp.Headline {
		mine[m.Label] = m
	}
	var out []api.MetricDelta
	all := true
	for _, m := range pp.Headline {
		r, ok := mine[m.Label]
		a, okA := number(m.Value)
		b, okB := number(r.Value)
		if !ok || !okA || !okB || a == 0 || (pp.Class != betterbench.ClassFull && percentile(m.Label)) {
			continue
		}
		d := api.MetricDelta{Label: m.Label, Unit: m.Unit, Published: a, Reproduced: b, TolerancePct: tolerance(pp.Class, m.Label)}
		d.DeltaPct = math.Round((b-a)/a*1000) / 10
		d.Within = math.Abs(d.DeltaPct) <= d.TolerancePct
		all = all && d.Within
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, false, errors.New("the two runs share no headline figures to compare; run the same phases")
	}
	return out, all, nil
}

// Reproduce runs the plan and compares the result with the published run.
func Reproduce(ctx context.Context, published []byte, o betterbench.RunOptions, version string, s Sink) (*api.Reproduction, *api.Error) {
	plan, err := PlanFrom(published)
	if err != nil {
		return nil, api.Newf("E_INPUT_INVALID", "Only BetterBench pages can be reproduced.", "%v", err)
	}
	res, apiErr := RunBenchmark(ctx, o, s)
	if apiErr != nil {
		return nil, apiErr
	}
	deltas, within, err := Compare(published, res.Data)
	if err != nil {
		return nil, api.Newf("E_INPUT_INVALID", "Reproduce with the same phases as the published run.", "%v", err)
	}
	sum := sha256.Sum256(res.Data)
	rr, _ := betterbench.Parse(res.Data)
	rep := &api.Reproduction{BetterBenchVersion: res.Version, Model: o.Model, RunsPerCategory: plan.RunsPerCategory, RunClass: plan.Class, Phases: plan.Phases,
		Metrics: deltas, WithinTolerance: within, ResultsSHA256: hex.EncodeToString(sum[:]), RanAt: time.Now().UTC().Format(time.RFC3339), L80Version: version}
	if rr != nil {
		rep.Notes = rr.Env.Notes
	}
	return rep, nil
}

// reproductionPath keeps reproductions beside the publish ledger, one per
// request, so `L80 verify approve` can send the evidence it was run with.
func reproductionPath(id string) string {
	safe := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == '.' {
			return '_'
		}
		return r
	}, id)
	return filepath.Join(filepath.Dir(history.Path()), "reproductions", safe+".json")
}

// SaveReproduction records a reproduction for a request id.
func SaveReproduction(id string, r *api.Reproduction) error {
	p := reproductionPath(id)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	return os.WriteFile(p, b, 0o600)
}

// LoadReproduction returns the reproduction saved for a request, if any.
func LoadReproduction(id string) (*api.Reproduction, error) {
	b, err := os.ReadFile(reproductionPath(id))
	if err != nil {
		return nil, err
	}
	var r api.Reproduction
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// FormatDeltas renders the comparison as aligned lines.
func FormatDeltas(ds []api.MetricDelta) []string {
	out := []string{fmt.Sprintf("%-22s %12s %12s %8s  %s", "figure", "published", "yours", "delta", "")}
	for _, d := range ds {
		mark := "ok"
		if !d.Within {
			mark = fmt.Sprintf("outside ±%g%%", d.TolerancePct)
		}
		out = append(out, fmt.Sprintf("%-22s %12s %12s %+7.1f%%  %s", d.Label, fmtNum(d.Published, d.Unit), fmtNum(d.Reproduced, d.Unit), d.DeltaPct, mark))
	}
	return out
}

func fmtNum(f float64, unit string) string {
	s := strconv.FormatFloat(f, 'f', 1, 64)
	if f >= 1000 {
		s = strconv.FormatFloat(f, 'f', 0, 64)
	}
	if unit != "" {
		s += " " + unit
	}
	return s
}
