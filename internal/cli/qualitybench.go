package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/discover"
	"github.com/launch80/L80-CLI/internal/output"
	"github.com/launch80/L80-CLI/internal/qualitybench"
)

// defaultQualityPath mirrors defaultResultsPath for QualityBench runs.
func defaultQualityPath(model string, now time.Time) string {
	return "qualitybench-" + strings.TrimPrefix(defaultResultsPath(model, now), "betterbench-")
}

func runQualitybench(e env, args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "endpoints":
			return qualityEndpoints(e, args[1:])
		case "tasks":
			return qualityTasks(e, args[1:])
		case "compare":
			return qualityCompare(e, args[1:])
		case "setup":
			return qualitySetup(e, args[1:])
		}
	}

	fs := flag.NewFlagSet("qualitybench", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	fs.Usage = func() { qualitybenchUsage(e) }
	apiBase, asJSON := addCommonFlags(fs)

	endpoint := fs.String("endpoint", "", "OpenAI-compatible base URL; omit it to use the local server that serves --model")
	model := fs.String("model", "", "model id as the server lists it")
	quick := fs.Bool("quick", false, "about a tenth of each task's items: a smoke run, and the page says so")
	tasks := fs.String("tasks", "", "comma-separated task ids to run instead of the whole suite (see `L80 qualitybench tasks`)")
	thinking := fs.String("thinking", "", "on|off: force reasoning on or off (default: the server's)")
	reference := fs.String("reference", "", "a results file of the same suite to compare against (paired deltas, KLD, thresholds)")
	thresholds := fs.String("thresholds", "", "pre-registered thresholds file, hashed into the results")
	deadline := fs.String("deadline", "", "stop starting work at this time (RFC 3339) or after this long (e.g. 6h); cut tasks are marked partial")
	var notes multiFlag
	fs.Var(&notes, "note", "KEY=VALUE recorded in the run; repeatable")
	tokenizer := fs.String("tokenizer", "", "Hugging Face repo (or local dir) for logprobs and needle when the served id is not one")
	sandbox := fs.String("sandbox", "", "docker (default) | native: native runs model-written code on this machine; only for a disposable box")
	apiKey := fs.String("api-key", "", "bearer key for the endpoint (or L80_QUALITYBENCH_API_KEY); passed by environment, never recorded")
	out := fs.String("out", "", "results file (default ./qualitybench-<model>-<time>.json); the HTML report goes beside it")
	var scan multiFlag
	fs.Var(&scan, "scan", "with no --endpoint, also probe this host:port or URL; repeatable")

	results := fs.String("results", "", "publish this existing results file instead of running")
	dryRun := fs.Bool("dry-run", false, "run (or read) and prepare the payload, but do not publish")
	noPublish := fs.Bool("no-publish", false, "run and keep the results and HTML report locally; publish later with --results")
	payloadOut := fs.String("payload-out", "", "also write the exact payload that would be published to this path")

	var extra []string
	if i := indexOf(args, "--"); i >= 0 {
		extra, args = args[i+1:], args[:i]
	}
	positional, parseErr := parseArgs(fs, args)
	if parseErr != nil {
		return api.ExitUsage
	}
	if len(positional) != 0 {
		return fail(e, api.Newf("E_USAGE", "Run `L80 qualitybench --help`.", "unexpected argument %q", positional[0]))
	}
	if *sandbox != "" && *sandbox != "docker" && *sandbox != "native" {
		return fail(e, api.Newf("E_USAGE", "Pass --sandbox docker or --sandbox native.", "--sandbox %q", *sandbox))
	}
	if *thinking != "" && *thinking != "on" && *thinking != "off" {
		return fail(e, api.Newf("E_USAGE", "Pass --thinking on or --thinking off.", "--thinking %q", *thinking))
	}
	for _, n := range notes {
		if !strings.Contains(n, "=") {
			return fail(e, api.Newf("E_USAGE", "Write each note as --note KEY=VALUE.", "--note %q has no '='", n))
		}
	}

	var data []byte
	partial := false
	resultsPath := *results
	switch {
	case *results != "" && (*endpoint != "" || *model != ""):
		return fail(e, api.Newf("E_USAGE", "Use --results to publish a file, or --model (and optionally --endpoint) to run, not both.",
			"--results was given with --endpoint/--model"))
	case *results != "":
		var err error
		data, err = os.ReadFile(*results)
		if err != nil {
			return fail(e, api.Newf("E_INPUT_INVALID", "Check the path. It should be the results file `qualitybench run` wrote.",
				"could not read %s: %v", *results, err))
		}
	case *model == "":
		return fail(e, api.Newf("E_USAGE",
			"Run with `L80 qualitybench --model <id>` (L80 finds the local server; `L80 qualitybench endpoints` lists them), or publish with `--results <file>`.",
			"--model is required to run"))
	default:
		scope, err := qualitybench.ResolveScope(*quick, *tasks)
		if err != nil {
			return fail(e, api.Newf("E_USAGE", "Pick tasks from `L80 qualitybench tasks`.", "--tasks: %v", err))
		}
		for _, a := range extra {
			if name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "="); strings.HasPrefix(a, "-") && strings.HasPrefix(name, "api-key") {
				return fail(e, api.Newf("E_USAGE",
					"Pass the key as `L80 qualitybench --api-key <key>` (before --) or set L80_QUALITYBENCH_API_KEY.",
					"%s after -- would put the key on the command line", a))
			}
		}
		ep := *endpoint
		if ep == "" {
			found, apiErr := endpointFor(*model, scan)
			if apiErr != nil {
				return fail(e, apiErr)
			}
			ep = found.URL
			output.Detailf(e.stderr, "using %s (%s) for %s", found.URL, engineLabel(found), *model)
			if *tokenizer == "" {
				for _, m := range found.Models {
					if m.ID == *model && m.Tokenizer() != "" && m.Tokenizer() != *model {
						*tokenizer = m.Tokenizer()
						output.Detailf(e.stderr, "tokenizer %s (the server's root for %s)", *tokenizer, *model)
					}
				}
			}
			if !servesLoaded(found, *model) {
				output.Detailf(e.stderr, "warning: %s is not loaded yet; the first request pays the load time (QualityBench's warmup request is not scored)", *model)
			}
		}
		key := *apiKey
		if key == "" {
			key = os.Getenv("L80_QUALITYBENCH_API_KEY")
		}
		resultsPath = *out
		if resultsPath == "" {
			resultsPath = defaultQualityPath(*model, time.Now())
		}
		if abs, err := filepath.Abs(resultsPath); err == nil {
			resultsPath = abs
		}
		if info, err := os.Stat(resultsPath); err == nil && info.IsDir() {
			return fail(e, api.Newf("E_USAGE", "Pass --out with a file path such as ./results.json.", "--out %s is a directory", resultsPath))
		}
		mins, unmeasured := qualitybench.EstimateMinutes(scope.Tasks, scope.Tier == qualitybench.TierQuick)
		output.Detailf(e.stderr, "%s run: %s", scope.Tier, strings.Join(scope.Tasks, ", "))
		est := fmt.Sprintf("roughly %d min with thinking off on the reference run (%s)", mins, qualitybench.RefRun)
		if unmeasured > 0 {
			est += fmt.Sprintf(", plus %d task(s) not timed", unmeasured)
		}
		output.Detailf(e.stderr, "%s", est)

		opts := qualitybench.RunOptions{
			Endpoint: ep, Model: *model, Out: resultsPath, Scope: scope, Thinking: *thinking,
			Reference: *reference, Thresholds: *thresholds, Deadline: *deadline, Notes: notes, Extra: extra, APIKey: key,
			Tokenizer: *tokenizer, Sandbox: *sandbox,
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		res, apiErr := app.RunQuality(ctx, opts, app.WriterSink{W: e.stderr})
		stop()
		if apiErr != nil {
			return fail(e, apiErr)
		}
		data = res.Data
		partial = len(res.Partial) > 0
		output.Successf(e.stderr, "results written to %s", resultsPath)
		if _, err := os.Stat(res.Report); err == nil {
			output.Successf(e.stderr, "report written to %s", res.Report)
		}
		for _, l := range qualitybench.Headline(data) {
			output.Detailf(e.stderr, "%s", l)
		}
		if partial {
			output.Detailf(e.stderr, "warning: partial run (%s); the page lists what is missing", strings.Join(res.Partial, "; "))
		}
		if *noPublish {
			output.Detailf(e.stderr, "not published; publish later with `L80 qualitybench --results %s`", resultsPath)
			return partialCode(partial)
		}
	}

	body, err := qualitybench.Prepare(data)
	if err != nil {
		return failQualityPrepare(e, resultsPath, err)
	}
	if !qualitybench.StripsIdentity(body) {
		return fail(e, api.Newf("E_INTERNAL", "Report this: the payload still names the endpoint or host.", "identity fields survived preparation"))
	}
	limit := api.MaxPayloadBytesFor(qualitybench.TemplateID)
	if len(body) > limit {
		return fail(e, api.Newf("E_PAYLOAD_TOO_LARGE",
			"The results file carries more series than the page takes; rerun with a current QualityBench, which writes the per-item data to a sidecar.",
			"payload is %d bytes; the limit for %s is %d", len(body), qualitybench.TemplateID, limit))
	}
	if *payloadOut != "" {
		if err := os.WriteFile(*payloadOut, append(append([]byte{}, body...), '\n'), 0o644); err != nil {
			return fail(e, api.Newf("E_INPUT_INVALID", "Pass --payload-out with a writable path.", "could not write %s: %v", *payloadOut, err))
		}
		output.Detailf(e.stderr, "payload written to %s", *payloadOut)
	}
	if *dryRun {
		if *asJSON {
			return emitJSON(e, map[string]any{"dry_run": true, "template": qualitybench.TemplateID, "byte_size": len(body), "results": resultsPath})
		}
		output.Successf(e.stdout, "dry run: would publish %s as %s (%d bytes)", resultsPath, qualitybench.TemplateID, len(body))
		return api.ExitOK
	}
	if code := publishBody(e, body, qualitybench.TemplateID, resultsPath, *apiBase, *asJSON); code != api.ExitOK {
		return code
	}
	return partialCode(partial)
}

// partialCode passes the tool's "a task failed" status through once the
// results are kept (and published), so scripts can tell a partial run.
func partialCode(partial bool) int {
	if partial {
		return qualitybench.ExitTaskFailed
	}
	return api.ExitOK
}

func qualitySetup(e env, args []string) int {
	fs := flag.NewFlagSet("qualitybench setup", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	tasks := fs.String("tasks", "", "only what these tasks need")
	sandbox := fs.String("sandbox", "", "docker (default) | native")
	if _, err := parseArgs(fs, args); err != nil {
		return api.ExitUsage
	}
	var ts []string
	if *tasks != "" {
		scope, err := qualitybench.ResolveScope(false, *tasks)
		if err != nil {
			return fail(e, api.Newf("E_USAGE", "Pick tasks from `L80 qualitybench tasks`.", "--tasks: %v", err))
		}
		ts = scope.Tasks
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	code, apiErr := app.SetupQuality(ctx, ts, *sandbox, app.WriterSink{W: e.stderr})
	if apiErr != nil {
		return fail(e, apiErr)
	}
	return code
}

// endpointFor scans this machine for the server that serves model. One
// server that has it loaded wins; otherwise the choice is the user's.
func endpointFor(model string, extra []string) (discover.Endpoint, *api.Error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	eps := discover.Scan(ctx, discover.Options{Extra: extra})
	hits := discover.FindModel(eps, model)
	switch {
	case len(hits) == 0 && len(eps) == 0:
		return discover.Endpoint{}, api.Newf("E_USAGE",
			"Start the server first, or pass --endpoint <url> (or --scan host:port for a non-default port).",
			"no OpenAI-compatible server found on ports %s", portList())
	case len(hits) == 0:
		return discover.Endpoint{}, api.Newf("E_USAGE",
			"Run `L80 qualitybench endpoints` to see what each server lists, then pass the exact id, or --endpoint.",
			"no local server lists model %q", model)
	case len(hits) > 1 && (servesLoaded(hits[1], model) || !servesLoaded(hits[0], model)):
		var urls []string
		for _, h := range hits {
			urls = append(urls, h.URL)
		}
		return discover.Endpoint{}, api.Newf("E_USAGE", "Pass --endpoint with the one to use.",
			"%q is served by more than one server: %s", model, strings.Join(urls, ", "))
	}
	return hits[0], nil
}

func servesLoaded(ep discover.Endpoint, model string) bool {
	for _, m := range ep.Models {
		if m.ID == model {
			return m.Loaded
		}
	}
	return false
}

func portList() string {
	var s []string
	for _, p := range discover.DefaultPorts {
		s = append(s, fmt.Sprint(p))
	}
	return strings.Join(s, ", ")
}

func engineLabel(ep discover.Endpoint) string {
	if ep.Version != "" {
		return ep.Engine + " " + ep.Version
	}
	return ep.Engine
}

func qualityEndpoints(e env, args []string) int {
	fs := flag.NewFlagSet("qualitybench endpoints", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	var scan multiFlag
	fs.Var(&scan, "scan", "also probe this host:port or URL; repeatable")
	if _, err := parseArgs(fs, args); err != nil {
		return api.ExitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	eps := discover.Scan(ctx, discover.Options{Extra: scan})
	if *asJSON {
		return emitJSON(e, map[string]any{"endpoints": eps})
	}
	if len(eps) == 0 {
		output.Successf(e.stdout, "no OpenAI-compatible server found on ports %s", portList())
		output.Detailf(e.stdout, "start one, or pass --scan host:port")
		return api.ExitOK
	}
	for _, ep := range eps {
		output.Successf(e.stdout, "%s  %s", ep.URL, engineLabel(ep))
		for _, m := range ep.Models {
			line := m.ID
			var tags []string
			if m.Backend != "" {
				tags = append(tags, m.Backend)
			}
			if m.GPU != "" {
				tags = append(tags, m.GPU)
			}
			if !m.Loaded {
				tags = append(tags, "not loaded: the first request pays the load time")
			}
			if len(tags) > 0 {
				line += "  (" + strings.Join(tags, ", ") + ")"
			}
			output.Detailf(e.stdout, "%s", line)
		}
	}
	return api.ExitOK
}

func qualityTasks(e env, args []string) int {
	fs := flag.NewFlagSet("qualitybench tasks", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	if _, err := parseArgs(fs, args); err != nil {
		return api.ExitUsage
	}
	if *asJSON {
		type row struct {
			ID         string `json:"id"`
			Label      string `json:"label"`
			Items      int    `json:"items"`
			Measures   string `json:"measures"`
			RefMinutes int    `json:"ref_minutes,omitempty"`
			Default    bool   `json:"default"`
		}
		rows := make([]row, 0, len(qualitybench.Suite))
		for _, t := range qualitybench.Suite {
			rows = append(rows, row{t.ID, t.Label, t.Items, t.Measures, t.RefMinutes, t.Default})
		}
		return emitJSON(e, map[string]any{"tasks": rows, "ref_run": qualitybench.RefRun})
	}
	for _, t := range qualitybench.Suite {
		mins := "  —"
		if t.RefMinutes > 0 {
			mins = fmt.Sprintf("%3dm", t.RefMinutes)
		}
		opt := ""
		if !t.Default {
			opt = "  (optional: runs only when named)"
		}
		output.Successf(e.stdout, "%-12s %-17s %5d items %s  %s%s", t.ID, t.Label, t.Items, mins, t.Measures, opt)
	}
	output.Detailf(e.stdout, "minutes: the full tier, measured on %s", qualitybench.RefRun)
	return api.ExitOK
}

func qualityCompare(e env, args []string) int {
	fs := flag.NewFlagSet("qualitybench compare", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	thresholds := fs.String("thresholds", "", "pre-registered thresholds file")
	out := fs.String("out", "", "where to write the compare results (default: the tool's)")
	positional, err := parseArgs(fs, args)
	if err != nil {
		return api.ExitUsage
	}
	if len(positional) != 2 {
		return fail(e, api.Newf("E_USAGE", "Run `L80 qualitybench compare <reference.json> <test.json>`.", "compare takes two results files"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	code, apiErr := app.CompareQuality(ctx, positional[0], positional[1], *thresholds, *out, app.WriterSink{W: e.stderr})
	if apiErr != nil {
		return fail(e, apiErr)
	}
	return code
}

func failQualityPrepare(e env, path string, err error) int {
	var le *qualitybench.LeakError
	switch {
	case errors.Is(err, qualitybench.ErrAlreadyPayload):
		return fail(e, api.Newf("E_INPUT_INVALID", fmt.Sprintf("This is a payload for another template. Publish it with `L80 publish %s`.", path), "%s is %v", path, err))
	case errors.Is(err, qualitybench.ErrSyntax):
		return fail(e, api.Newf("E_JSON_INVALID", "Fix the JSON syntax and retry.", "%s: %v", path, err))
	case errors.As(err, &le):
		return fail(e, api.Newf("E_INPUT_INVALID",
			"Nothing was sent. QualityBench should never write this; rerun with a current version, or remove that value by hand.",
			"%s: %v", path, err))
	default:
		return fail(e, api.Newf("E_INPUT_INVALID", "Pass the results file `qualitybench run` wrote.", "%s: %v", path, err))
	}
}

func qualitybenchUsage(e env) {
	fmt.Fprint(e.stderr, `Usage:
  L80 qualitybench --model <id> [--endpoint <url>] [scope] [run flags] [-- <qualitybench run flags>]
  L80 qualitybench --results <results.json> [--dry-run]
  L80 qualitybench endpoints [--json] [--scan host:port]
  L80 qualitybench tasks [--json]
  L80 qualitybench compare <reference.json> <test.json> [--thresholds T.md]
  L80 qualitybench setup [--tasks a,b] [--sandbox docker|native]

Runs QualityBench (https://github.com/launch80/QualityBench) against an
OpenAI-compatible endpoint, writes results.json and an HTML report, and publishes
the results to launch80 (minus env.endpoint and env.host). With no --endpoint, L80
finds the local server that lists --model (see `+"`endpoints`"+`).

Scope (default: the full suite):
  --quick                 about a tenth of each task; the page says so
  --tasks a,b,c           only these tasks, at full size (combine with --quick for a quick subset)

Run flags:
  --thinking on|off       force reasoning on or off
  --reference ref.json    also compare against a reference run: paired deltas, KLD, thresholds
  --thresholds T.md       pre-registered thresholds, hashed into the results
  --deadline T            RFC 3339 time or a duration (6h); tasks cut by it are marked partial
  --note KEY=VALUE        recorded in the run (repeatable)
  --tokenizer org/repo    tokenizer for logprobs and needle, if the served id is not a HF repo
  --sandbox docker|native native runs model code on this machine: only on a disposable box
  --out <path>            results file (default ./qualitybench-<model>-<time>.json)
  --api-key <key>         endpoint key (or L80_QUALITYBENCH_API_KEY); sent by environment
  --no-publish            keep the results local
  Rerunning the same command resumes: finished tasks and shards are skipped.
  If a task fails, the rest are kept (and published); the page lists what is
  missing, and the exit status is 1.

Publish flags:
  --dry-run, --payload-out <path>, --json, --api-base   as for L80 publish
`)
}
