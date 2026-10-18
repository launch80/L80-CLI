package cli

import (
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/output"
	"github.com/launch80/L80-CLI/internal/pytool"
	"github.com/launch80/L80-CLI/internal/vast"
)

// runVast fronts bbvastl80: rent a Vast.ai GPU, serve a model on it,
// benchmark it with BetterBench from this machine, publish, tear down.
//
// Every verb is passed through. L80 adds the environment that makes
// bbvastl80 call back into this L80 and its pinned BetterBench, keeps runs
// in one place so they can be found from any directory, and after `run`
// names any instance that may still be billing. It never answers a
// confirmation prompt for the user.
func runVast(e env, args []string) int {
	r, apiErr := resolveTool("vast")
	if apiErr != nil {
		return fail(e, apiErr)
	}
	runsDir := vast.RunsDir()

	if wantsHelp(args) {
		toolHeader(e, r,
			"L80 vast — rent a Vast.ai GPU, serve a model, benchmark it, publish, tear down (bbvastl80)",
			"",
			"  L80 vast <bbvastl80 command> [args...]   every bbvastl80 command, passed through",
			"  L80 vast run --spec run.yaml [--max-hours N] [--json]",
			"  L80 vast status --json                    runs L80 knows about, from their state files",
			"",
			"  Renting bills your Vast.ai account until the instance is destroyed. A run tears its",
			"  box down after publishing or failing; if L80 is killed, run `L80 vast reap`.",
			"  Ctrl-C once lets the run clean up; twice forces it.",
			"  Vast key: VAST_API_KEY, or ~/.config/bbvastl80/credentials. Runs are kept in "+runsDir+".",
			"  Flags after -- go to bbvastl80 untouched. --max-hours N destroys the run's box after N hours.")
		res, err := runChild(e, r, []string{"--help"}, nil, nil)
		return exitFromChild(e, "vast", res, err)
	}

	verb := args[0]
	owned := ownedFlags{}
	switch verb {
	case "run":
		owned = ownedFlags{"json": false, "runs-dir": true, "max-hours": true}
	case "status":
		owned = ownedFlags{"json": false, "runs-dir": true}
	}
	mine, rest, err := splitOwned(args[1:], owned)
	if err != nil {
		return fail(e, api.Newf("E_USAGE", "Run `L80 vast --help`.", "%v", err))
	}
	if d := mine["runs-dir"]; d != "" {
		runsDir = d
	}

	if verb == "status" && isTrue(mine["json"]) && len(rest) == 0 {
		return vastStatusJSON(e, runsDir)
	}

	childEnv, envErr := vastEnv(runsDir)
	if envErr != nil {
		return fail(e, api.Newf("E_INTERNAL", "Check that "+stateDir()+" is writable.", "%v", envErr))
	}
	if verb != "run" {
		res, err := runChild(e, r, append([]string{verb}, rest...), childEnv, nil)
		return exitFromChild(e, "vast", res, err)
	}
	return vastRun(e, r, rest, childEnv, runsDir, mine)
}

// vastEnv is what bbvastl80 needs to call back into this L80: the binary
// itself for publishing, a shim that runs the pinned BetterBench, and an
// absolute runs directory.
func vastEnv(runsDir string) ([]string, error) {
	if err := os.MkdirAll(runsDir, 0o700); err != nil {
		return nil, err
	}
	shim, err := toolShim("betterbench")
	if err != nil {
		return nil, err
	}
	return []string{
		"L80_BIN=" + selfPath(),
		"L80_BETTERBENCH=" + shim,
		"BBVASTL80_RUNS_DIR=" + runsDir,
		"BETTERBENCH_NO_UPDATE_CHECK=1",
	}, nil
}

func vastRun(e env, r pytool.Runner, rest []string, childEnv []string, runsDir string, mine map[string]string) int {
	asJSON := isTrue(mine["json"])
	jsonOut := e.stdout
	e = synced(e)
	out := e
	if asJSON {
		// Keep stdout for the one JSON object.
		out = env{stdout: e.stderr, stderr: e.stderr}
	}
	var stop chan struct{}
	var limit time.Duration
	if v := mine["max-hours"]; v != "" {
		h, err := strconv.ParseFloat(v, 64)
		if err != nil || h <= 0 {
			return fail(e, api.Newf("E_USAGE", "Pass hours as a number, e.g. --max-hours 2.5.", "--max-hours %q is not a positive number", v))
		}
		limit = time.Duration(h * float64(time.Hour))
		stop = make(chan struct{})
	}

	start := time.Now()
	output.Detailf(e.stderr, "runs are kept in %s", runsDir)
	if limit > 0 {
		output.Detailf(e.stderr, "watchdog: the run's instance will be destroyed after %s", limit)
	}
	var fired atomic.Bool
	if stop != nil {
		t := time.AfterFunc(limit, func() {
			fired.Store(true)
			fmt.Fprintf(e.stderr, "\n  L80: --max-hours %s reached; stopping the run and destroying its instance\n", mine["max-hours"])
			close(stop)
		})
		defer t.Stop()
	}
	res, err := runChild(out, r, append([]string{"run"}, rest...), childEnv, stop)

	expired := fired.Load()
	runs, _ := vast.List(runsDir)
	mineRuns := vast.Since(runs, start)
	if expired {
		for _, run := range vast.Billing(mineRuns) {
			output.Detailf(e.stderr, "destroying instance %d (run %s)", *run.InstanceID, run.RunID)
			// The user asked for this by setting --max-hours, so the prompt is answered.
			dres, derr := runChild(out, r, []string{"destroy", "--run", run.RunID, "-y"}, childEnv, nil)
			if derr != nil || dres.code != 0 {
				output.Detailf(e.stderr, "warning: destroy failed; run `L80 vast destroy --run %s` now", run.RunID)
			}
		}
		runs, _ = vast.List(runsDir)
		mineRuns = vast.Since(runs, start)
	}
	billing := vast.Billing(mineRuns)
	for _, run := range billing {
		fmt.Fprintln(e.stderr, "  warning: "+run.Reminder(time.Now()))
	}
	if len(billing) > 0 && (res.interrupted || res.code != 0) {
		output.Detailf(e.stderr, "`L80 vast reap` destroys every instance bbvastl80 labelled, after asking")
	}

	code := exitFromChild(e, "vast", res, err)
	if asJSON {
		emitJSON(env{stdout: jsonOut, stderr: e.stderr}, map[string]any{
			"exit_status": code, "interrupted": res.interrupted, "watchdog_fired": expired,
			"runs_dir": runsDir, "runs": jsonRuns(mineRuns), "may_be_billing": jsonRuns(billing),
		})
	}
	return code
}

func vastStatusJSON(e env, runsDir string) int {
	runs, err := vast.List(runsDir)
	if err != nil {
		return fail(e, api.Newf("E_INPUT_INVALID", "Check the directory's permissions.", "could not read %s: %v", runsDir, err))
	}
	return emitJSON(e, map[string]any{"runs_dir": runsDir, "runs": jsonRuns(runs), "may_be_billing": jsonRuns(vast.Billing(runs))})
}

func jsonRuns(runs []vast.RunState) []map[string]any {
	out := make([]map[string]any, 0, len(runs))
	now := time.Now()
	for _, r := range runs {
		row := map[string]any{
			"id": r.RunID, "status": r.Status, "spec": r.SpecName, "created_at": r.Created().UTC().Format(time.RFC3339),
			"instance": r.InstanceID, "dph": r.DPH, "publish_url": r.PublishURL, "results": r.ResultsPath,
			"may_be_billing": r.MayBeBilling(), "dir": r.Dir,
		}
		if r.StartedBillingAt != nil {
			row["billing_minutes"] = int(r.BillingFor(now).Minutes())
		}
		out = append(out, row)
	}
	return out
}
