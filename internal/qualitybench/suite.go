// Package qualitybench fronts QualityBench (https://github.com/launch80/QualityBench):
// the suite L80 offers, the argv it runs, and the payload it publishes.
//
// QualityBench measures a model's quality, and in compare mode the quality it
// loses to quantization or a different engine, against an OpenAI-compatible
// endpoint. L80 owns task selection, endpoint discovery and publishing; the
// tool owns generation, scoring and statistics. docs/qualitybench.md is the
// contract between the two.
package qualitybench

import (
	"fmt"
	"sort"
	"strings"
)

// Task is one entry of the v1 suite.
type Task struct {
	ID    string
	Label string
	// Items is the full-tier item count. Quick runs about a tenth of it.
	Items int
	// What the task measures, for the TUI checklist and `L80 qualitybench tasks`.
	Measures string
	// RefMinutes is the measured wall time of the full tier on the reference
	// run below, thinking off; 0 means not measured on its own. Thinking on
	// multiplies it several times over (GSM8K ran about 4x).
	RefMinutes int
	// Default tasks make up a full run. Optional ones run only when named.
	Default bool
}

// RefRun names the run RefMinutes was measured on.
const RefRun = "Qwopus3.8-27B INT4 on one Arc Pro B65, vLLM 0.30, thinking off (2026-11-02)"

// Suite is the v1 suite in the order a run executes it.
var Suite = []Task{
	{ID: "gsm8k", Label: "GSM8K", Items: 1319, Measures: "grade-school math, last number in the reply", RefMinutes: 19, Default: true},
	{ID: "ifeval", Label: "IFEval", Items: 541, Measures: "instruction following, prompt-level strict", RefMinutes: 9, Default: true},
	{ID: "mmlu_pro", Label: "MMLU-Pro", Items: 1000, Measures: "knowledge, fixed 1000-question subset of 14 subjects", RefMinutes: 42, Default: true},
	{ID: "humaneval", Label: "HumanEval+", Items: 164, Measures: "Python code generation, sandboxed tests", RefMinutes: 22, Default: true},
	{ID: "mbpp", Label: "MBPP+", Items: 378, Measures: "Python code generation, sandboxed tests", RefMinutes: 32, Default: true},
	{ID: "bfcl", Label: "BFCL", Items: 400, Measures: "tool calling through the server's tool parser", RefMinutes: 4, Default: true},
	{ID: "lcb", Label: "LiveCodeBench v6", Items: 175, Measures: "competitive programming, fixed slice, sandboxed", RefMinutes: 17, Default: true},
	{ID: "logprobs", Label: "Logprobs", Items: 592, Measures: "perplexity, and KLD / top-1 agreement in compare (2048-token windows)", RefMinutes: 16, Default: true},
	{ID: "needle", Label: "Needle", Items: 20, Measures: "long-context retrieval at 32k and 128k", Default: true},
	{ID: "spec_parity", Label: "Spec parity", Items: 32, Measures: "greedy text with and without speculative decoding (optional)"},
}

// Lookup returns a task by id.
func Lookup(id string) (Task, bool) {
	for _, t := range Suite {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

// DefaultIDs are the tasks a full or quick run executes.
func DefaultIDs() []string {
	var out []string
	for _, t := range Suite {
		if t.Default {
			out = append(out, t.ID)
		}
	}
	return out
}

// ParseTasks turns "gsm8k, ifeval" into suite ids in suite order, with
// duplicates removed. An unknown id is an error naming the valid ones.
func ParseTasks(s string) ([]string, error) {
	want := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if _, ok := Lookup(p); !ok {
			return nil, fmt.Errorf("unknown task %q (tasks: %s)", p, strings.Join(allIDs(), ", "))
		}
		want[p] = true
	}
	if len(want) == 0 {
		return nil, fmt.Errorf("no tasks given (tasks: %s)", strings.Join(allIDs(), ", "))
	}
	var out []string
	for _, t := range Suite {
		if want[t.ID] {
			out = append(out, t.ID)
		}
	}
	return out, nil
}

// EstimateMinutes sums RefMinutes for the given tasks at the given tier, and
// reports how many of them have no measurement. It is a planning figure for
// the TUI, never a result.
func EstimateMinutes(ids []string, quick bool) (minutes int, unmeasured int) {
	for _, id := range ids {
		t, _ := Lookup(id)
		if t.RefMinutes == 0 {
			unmeasured++
			continue
		}
		minutes += t.RefMinutes
	}
	if quick {
		minutes = (minutes + 9) / 10
	}
	return minutes, unmeasured
}

func allIDs() []string {
	out := make([]string, 0, len(Suite))
	for _, t := range Suite {
		out = append(out, t.ID)
	}
	return out
}

// Tier is the run scope the TUI offers: the whole suite, a tenth of it, or a
// chosen set of tasks at full size.
type Tier string

const (
	TierFull   Tier = "full"
	TierQuick  Tier = "quick"
	TierCustom Tier = "custom"
)

// Scope is a resolved run scope.
type Scope struct {
	Tier  Tier
	Tasks []string
}

// ResolveScope combines --quick and --tasks. Quick with tasks is a quick run of
// just those tasks; tasks alone is a custom full-size run.
func ResolveScope(quick bool, tasks string) (Scope, error) {
	sc := Scope{Tier: TierFull, Tasks: DefaultIDs()}
	if strings.TrimSpace(tasks) != "" {
		ids, err := ParseTasks(tasks)
		if err != nil {
			return Scope{}, err
		}
		sc.Tier, sc.Tasks = TierCustom, ids
	}
	if quick {
		sc.Tier = TierQuick
	}
	return sc, nil
}

// IsDefaultSet reports whether ids is exactly the default task set.
func IsDefaultSet(ids []string) bool {
	d := DefaultIDs()
	if len(ids) != len(d) {
		return false
	}
	a := append([]string{}, ids...)
	sort.Strings(a)
	sort.Strings(d)
	for i := range a {
		if a[i] != d[i] {
			return false
		}
	}
	return true
}
