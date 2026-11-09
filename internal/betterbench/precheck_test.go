package betterbench

import (
	"os"
	"strings"
	"testing"
)

func statusOf(checks []Check) map[string]string {
	m := map[string]string{}
	for _, c := range checks {
		m[c.Name] = c.Status
	}
	return m
}

func TestPrechecksOnFixtures(t *testing.T) {
	old, _ := loadFixture(t)
	s := statusOf(Prechecks(old))
	// 0.4.0: too old, and it cannot show a cold cache.
	if s["version"] != "fail" || s["cold_cache"] != "warn" || s["run_class"] != "pass" || s["phases"] != "pass" {
		t.Errorf("0.4.0 fixture: %v", s)
	}
	data, _ := os.ReadFile("testdata/results_schema2_v061.json")
	r, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	checks := Prechecks(r)
	s = statusOf(checks)
	if s["version"] != "pass" || s["cold_cache"] != "fail" || !Blocking(checks) {
		t.Errorf("0.6.1 fixture with a cache hit: %v", s)
	}
	// Clear the hit and add hardware: everything that blocks now passes.
	for i := range r.Prefill {
		for j := range r.Prefill[i].CachedTokens {
			r.Prefill[i].CachedTokens[j] = 0
		}
	}
	r.Env.Notes["hardware"] = "1x RTX 4090"
	checks = Prechecks(r)
	if Blocking(checks) {
		t.Errorf("should be verifiable: %+v", checks)
	}
	// A quick run is its own verifiable class; a partial run is refused, with the fix named.
	r.Config.RunsPerCategory = 5
	if Blocking(Prechecks(r)) {
		t.Errorf("a 5-pass quick run should be verifiable: %+v", Prechecks(r))
	}
	r.Config.RunsPerCategory = 4
	r.Prefill = nil
	checks = Prechecks(r)
	s = statusOf(checks)
	if s["run_class"] != "fail" || s["phases"] != "fail" {
		t.Errorf("quick partial: %v", s)
	}
	for _, c := range checks {
		if c.Name == "phases" && !strings.Contains(c.Detail, "prefill") {
			t.Errorf("detail should name the missing phase: %s", c.Detail)
		}
	}
	delete(r.Env.Notes, "engine")
	if statusOf(Prechecks(r))["notes"] != "fail" {
		t.Error("engine note is required")
	}
}

func TestRunClass(t *testing.T) {
	for _, c := range []struct {
		passes, warmup int
		want           string
	}{
		{4, 1, ClassSmoke}, {5, 0, ClassSmoke}, {5, 1, ClassQuick}, {10, 2, ClassQuick},
		{19, 1, ClassQuick}, {20, 1, ClassFull}, {20, 0, ClassSmoke},
	} {
		if got := RunClass(Config{RunsPerCategory: c.passes, Warmup: c.warmup}); got != c.want {
			t.Errorf("%d passes, %d warmup: %s, want %s", c.passes, c.warmup, got, c.want)
		}
	}
}
