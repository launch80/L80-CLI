package betterbench

import (
	"fmt"
	"strconv"
	"strings"
)

// Check is one verification pre-check. A "fail" blocks a verification
// request; a "warn" is shown to the reviewer but does not block.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // pass | warn | fail
	Detail string `json:"detail"`
}

// MinVerifiableVersion is the oldest BetterBench whose results can be
// verified: 0.6.1 is the first that records prefix-cache hits, without which
// a cold-cache claim cannot be checked.
const MinVerifiableVersion = "0.6.1"

// Prechecks are the content checks a run must pass before launch80 is asked
// to verify it. The server recomputes the same checks from the stored page;
// these exist so the user learns the answer before asking.
func Prechecks(r *Results) []Check {
	var out []Check
	add := func(name, status, format string, a ...any) {
		out = append(out, Check{Name: name, Status: status, Detail: fmt.Sprintf(format, a...)})
	}

	// A full run, not --quick: BetterBench's default is 20 passes after 3 warmups.
	switch c := r.Config; {
	case c.RunsPerCategory >= 20 && c.Warmup >= 1:
		add("full_run", "pass", "%d passes per category after %d warmup", c.RunsPerCategory, c.Warmup)
	default:
		add("full_run", "fail", "%d passes after %d warmup; a verifiable run needs at least 20 passes and 1 warmup (no --quick)", c.RunsPerCategory, c.Warmup)
	}

	var missing []string
	if len(r.SingleStream) == 0 {
		missing = append(missing, "single-stream")
	}
	if len(r.Prefill) == 0 {
		missing = append(missing, "prefill")
	}
	if len(r.Concurrency) == 0 {
		missing = append(missing, "concurrency")
	}
	if len(missing) == 0 {
		add("phases", "pass", "single-stream, prefill and concurrency all measured")
	} else {
		add("phases", "fail", "missing: %s; run without --decode/--prefill/--concurrency/--no-*", strings.Join(missing, ", "))
	}

	switch {
	case !r.Config.UniqueNonce:
		add("cold_cache", "fail", "the run asked for a warm prefix cache (unique_nonce false)")
	case r.CacheHits() > 0:
		add("cold_cache", "fail", "%d request(s) were served from the server's prefix cache; the timings include cache lookups", r.CacheHits())
	case !r.CacheReported():
		add("cold_cache", "warn", "the server does not report cached_tokens, so a cold cache cannot be confirmed")
	default:
		add("cold_cache", "pass", "every request reported cached_tokens and none was served from cache")
	}

	var runs, failed, truncated int
	for _, cat := range r.SingleStream {
		for _, run := range cat {
			runs++
			if !run.OK {
				failed++
			} else if run.FinishReason != nil && *run.FinishReason == "length" {
				truncated++
			}
		}
	}
	concFail := 0
	for _, c := range r.Concurrency {
		concFail += c.Requests - c.OK
	}
	switch {
	case runs > 0 && float64(failed)/float64(runs) > 0.02:
		add("requests", "fail", "%d of %d single-stream requests failed (over 2%%)", failed, runs)
	case failed > 0 || concFail > 0:
		add("requests", "warn", "%d single-stream and %d concurrency request(s) failed and are excluded", failed, concFail)
	default:
		add("requests", "pass", "every request completed")
	}
	if runs > 0 && truncated > 0 {
		add("truncation", "warn", "%d of %d runs stopped at max_tokens", truncated, runs)
	}

	switch v := r.BetterBenchVersion; {
	case v == "":
		add("version", "fail", "the file does not say which BetterBench wrote it")
	case compareVersions(v, MinVerifiableVersion) < 0:
		add("version", "fail", "BetterBench %s; verification needs %s or later (it records prefix-cache hits)", v, MinVerifiableVersion)
	default:
		add("version", "pass", "BetterBench %s", v)
	}

	engine := strings.TrimSpace(r.Env.Notes["engine"])
	hardware := strings.TrimSpace(r.Env.Notes["hardware"])
	if hardware == "" && (len(r.Env.GPU.NvidiaSMI) > 0 || len(r.Env.GPU.ROCmSMIProductName) > 0) {
		hardware = "detected by BetterBench"
	}
	switch {
	case engine == "" && hardware == "":
		add("notes", "fail", "add --note engine=<stack and version> and --note hardware=<GPUs>; a reviewer cannot reproduce a run without them")
	case engine == "":
		add("notes", "fail", "add --note engine=<stack and version>, e.g. engine=vLLM 0.9.3")
	case hardware == "":
		add("notes", "fail", "add --note hardware=<GPUs>, e.g. hardware=2x Radeon AI PRO R9700 (BetterBench detected none)")
	default:
		add("notes", "pass", "engine %q, hardware %s", engine, hardware)
	}

	n := 0
	if g := r.RecordedGate; g != nil {
		n = len(g.UnderSampled)
	} else {
		n = len(r.SampleGate())
	}
	if n > 0 {
		add("sample_gate", "warn", "%d percentile(s) rest on fewer samples than the n·tail ≥ 5 rule; the page marks them", n)
	}
	return out
}

// Blocking reports whether any check failed.
func Blocking(checks []Check) bool {
	for _, c := range checks {
		if c.Status == "fail" {
			return true
		}
	}
	return false
}

func compareVersions(a, b string) int {
	pa, pb := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(strings.SplitN(pa[i], "-", 2)[0])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(strings.SplitN(pb[i], "-", 2)[0])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}
