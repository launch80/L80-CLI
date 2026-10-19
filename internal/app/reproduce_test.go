package app

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/launch80/L80-CLI/internal/betterbench"
)

// withPasses returns the fixture run re-recorded as n passes after w warmup.
func withPasses(t *testing.T, n, w int) []byte {
	t.Helper()
	data, err := os.ReadFile("../betterbench/testdata/results_schema2_v061.json")
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]any
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	cfg := top["config"].(map[string]any)
	cfg["runs_per_category"], cfg["warmup"] = n, w
	out, _ := json.Marshal(top)
	return out
}

func TestCompareByRunClass(t *testing.T) {
	full := withPasses(t, 20, 3)
	ds, within, err := Compare(full, full)
	if err != nil || !within {
		t.Fatalf("a run compared with itself: %v %v", within, err)
	}
	tails := 0
	for _, d := range ds {
		if percentile(d.Label) {
			tails++
		}
		if d.Label == "Combined decode" && d.TolerancePct != 10 {
			t.Errorf("full decode tolerance %g", d.TolerancePct)
		}
	}
	if tails == 0 {
		t.Fatalf("the fixture should carry a tail figure to skip: %+v", ds)
	}

	quick := withPasses(t, 5, 1)
	if p, _ := PlanFrom(quick); p.Class != betterbench.ClassQuick {
		t.Errorf("class = %q", p.Class)
	}
	ds, within, err = Compare(quick, quick)
	if err != nil || !within {
		t.Fatalf("quick: %v %v", within, err)
	}
	for _, d := range ds {
		if percentile(d.Label) {
			t.Errorf("a quick comparison must skip %s", d.Label)
		}
		if d.Label == "Combined decode" && d.TolerancePct != 15 {
			t.Errorf("quick decode tolerance %g", d.TolerancePct)
		}
	}
}

func TestQuickTolerancesAreWider(t *testing.T) {
	for _, label := range []string{"Combined decode", "Combined ITL 1% low", "Combined TTFT p50", "Peak aggregate", "Prefill"} {
		if tolerance(betterbench.ClassQuick, label) <= tolerance(betterbench.ClassFull, label) {
			t.Errorf("%s: quick should allow more drift than full", label)
		}
	}
	if !percentile("Combined update p99") || !percentile("Combined ITL 1% low") || percentile("Combined TTFT p50") {
		t.Error("percentile labels")
	}
}
