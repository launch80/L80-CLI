package cli

import (
	"context"
	"encoding/json"
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
	"github.com/launch80/L80-CLI/internal/betterbench"
	"github.com/launch80/L80-CLI/internal/output"
)

// multiFlag collects a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ", ") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// phaseFlags records which phase flags were given, in order.
type phaseFlags struct{ list []string }

func (p *phaseFlags) flag(fs *flag.FlagSet, name, help string) {
	fs.BoolFunc(name, help, func(string) error { p.list = append(p.list, name); return nil })
}

// defaultResultsPath is where a run's results.json goes when --out is not
// given: the working directory, named after the model and the minute, so a
// series of runs never overwrite each other and the file is easy to find.
func defaultResultsPath(model string, now time.Time) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
			return r
		default:
			return '_'
		}
	}, model)
	return fmt.Sprintf("betterbench-%s-%s.json", safe, now.Format("20060102-1504"))
}

func runBetterbench(e env, args []string) int {
	fs := flag.NewFlagSet("betterbench", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	fs.Usage = func() { betterbenchUsage(e) }
	apiBase, asJSON := addCommonFlags(fs)

	// Running the benchmark.
	endpoint := fs.String("endpoint", "", "OpenAI-compatible base URL to benchmark, e.g. http://host:8080/v1")
	model := fs.String("model", "", "model name to request")
	quick := fs.Bool("quick", false, "short smoke run (BetterBench --quick); the page will say so")
	passes := fs.Int("passes", 0, "measured passes per category (default: BetterBench's 20)")
	warmup := fs.Int("warmup", -1, "warmup passes per category (default: BetterBench's)")
	var notes multiFlag
	fs.Var(&notes, "note", "KEY=VALUE recorded in the run and shown as a chip; repeatable")
	var phases phaseFlags
	phases.flag(fs, "decode", "run the single-stream phase (combinable with --prefill/--concurrency)")
	phases.flag(fs, "prefill", "run the prefill sweep")
	phases.flag(fs, "concurrency", "run the concurrency sweep")
	noPrefill := fs.Bool("no-prefill", false, "skip the prefill sweep")
	noConcurrency := fs.Bool("no-concurrency", false, "skip the concurrency sweep")
	apiKey := fs.String("api-key", "", "bearer key for the endpoint (or L80_BETTERBENCH_API_KEY); passed to BetterBench by environment, never recorded")
	out := fs.String("out", "", "where betterbench writes results.json (default ./betterbench-<model>-<time>.json)")

	// Publishing an existing file instead.
	results := fs.String("results", "", "publish this existing results.json instead of running a benchmark")

	// What to publish.
	tmpl := fs.String("template", betterbench.RawTemplateID,
		"bench.betterbench.v1 publishes the results file as written; bench.report.v1 maps it to the summary template")
	title := fs.String("title", "", "page title (default: model, plus the engine note if any)")
	summary := fs.String("summary", "", "summary paragraph shown under the title")
	var sections multiFlag
	fs.Var(&sections, "section", "extra prose section as \"Heading=Body\"; repeatable")
	dryRun := fs.Bool("dry-run", false, "run (or read) and prepare the payload, but do not publish")
	payloadOut := fs.String("payload-out", "", "also write the exact payload that would be published to this path")

	// Mapping-only flags, honoured with --template bench.report.v1.
	engine := fs.String("engine", "", "serving stack label (bench.report.v1 only)")
	hardware := fs.String("hardware", "", "hardware label (bench.report.v1 only)")

	// Everything after "--" goes to `betterbench run` verbatim.
	var extra []string
	if i := indexOf(args, "--"); i >= 0 {
		extra, args = args[i+1:], args[:i]
	}
	positional, parseErr := parseArgs(fs, args)
	if parseErr != nil {
		return api.ExitUsage
	}
	// A bare path is the 0.2.x form `L80 betterbench results.json`; keep it working.
	if len(positional) == 1 && *results == "" && *endpoint == "" {
		*results = positional[0]
		positional = nil
	}
	if len(positional) != 0 {
		return fail(e, api.Newf("E_USAGE", "Run `L80 betterbench --help`.", "unexpected argument %q", positional[0]))
	}

	prose := betterbench.Prose{Title: *title, Summary: *summary}
	for _, s := range sections {
		heading, body, ok := strings.Cut(s, "=")
		if !ok {
			return fail(e, api.Newf("E_USAGE", "Write each section as --section \"Heading=Body text\".", "--section %q has no '='", s))
		}
		prose.Sections = append(prose.Sections, betterbench.Section{Heading: strings.TrimSpace(heading), Body: strings.TrimSpace(body)})
	}

	// --- obtain the results file ------------------------------------------
	var data []byte
	resultsPath := *results
	switch {
	case *results != "" && *endpoint != "":
		return fail(e, api.Newf("E_USAGE", "Use --results to publish a file, or --endpoint/--model to run a benchmark, not both.",
			"--results and --endpoint were both given"))
	case *results != "":
		var err error
		data, err = os.ReadFile(*results)
		if err != nil {
			return fail(e, api.Newf("E_INPUT_INVALID", "Check the path. It should be the results.json that `betterbench run --out` wrote.",
				"could not read %s: %v", *results, err))
		}
	case *endpoint == "" || *model == "":
		return fail(e, api.Newf("E_USAGE",
			"Run a benchmark with `L80 betterbench --endpoint http://host:8080/v1 --model <name>`, or publish a file with `L80 betterbench --results results.json`.",
			"--endpoint and --model are required to run a benchmark"))
	default:
		for _, a := range extra {
			if name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "="); strings.HasPrefix(a, "-") && strings.HasPrefix(name, "api-key") {
				return fail(e, api.Newf("E_USAGE",
					"Pass the key as `L80 betterbench --api-key <key>` (before --) or set L80_BETTERBENCH_API_KEY, so it reaches BetterBench by environment and never appears in a command line.",
					"%s after -- would put the key on the command line", a))
			}
		}
		key := *apiKey
		if key == "" {
			key = os.Getenv("L80_BETTERBENCH_API_KEY")
		}
		resultsPath = *out
		if resultsPath == "" {
			resultsPath = defaultResultsPath(*model, time.Now())
		}
		if abs, err := filepath.Abs(resultsPath); err == nil {
			resultsPath = abs
		}
		if info, err := os.Stat(resultsPath); err == nil && info.IsDir() {
			return fail(e, api.Newf("E_USAGE", "Pass --out with a file path such as ./results.json.",
				"--out %s is a directory", resultsPath))
		}
		opts := betterbench.RunOptions{
			Endpoint: *endpoint, Model: *model, Out: resultsPath, Quick: *quick,
			Passes: *passes, Warmup: *warmup, Notes: notes, Phases: phases.list,
			NoPrefill: *noPrefill, NoConcurrency: *noConcurrency, Extra: extra, APIKey: key,
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		res, apiErr := app.RunBenchmark(ctx, opts, app.WriterSink{W: e.stderr})
		stop()
		if apiErr != nil {
			return fail(e, apiErr)
		}
		data = res.Data
		output.Successf(e.stderr, "results written to %s", resultsPath)
	}
	if *results != "" {
		// An existing file gets the same pre-publish caveats as a fresh run.
		if r, perr := betterbench.Parse(data); perr == nil {
			for _, w := range app.ResultWarnings(r) {
				output.Detailf(e.stderr, "warning: %s", w)
			}
		}
	}

	// --- build the payload --------------------------------------------------
	var body []byte
	switch *tmpl {
	case betterbench.RawTemplateID:
		var err error
		body, err = betterbench.PrepareRaw(data, prose)
		if err != nil {
			return failPrepare(e, resultsPath, err)
		}
	case betterbench.TemplateID:
		parsed, err := betterbench.Parse(data)
		if err != nil {
			return failPrepare(e, resultsPath, err)
		}
		opt := betterbench.Options{Title: *title, Summary: *summary, Engine: *engine, Hardware: *hardware, Sections: prose.Sections}
		payload, err := betterbench.Build(parsed, opt)
		if err != nil {
			return failPrepare(e, resultsPath, err)
		}
		body, err = json.Marshal(payload)
		if err != nil {
			return fail(e, api.Newf("E_INTERNAL", "Retry once.", "could not encode payload: %v", err))
		}
	default:
		return fail(e, api.Newf("E_TEMPLATE_UNKNOWN",
			fmt.Sprintf("Use --template %s (the results file as written) or %s (the summary mapping).", betterbench.RawTemplateID, betterbench.TemplateID),
			"%q is not a template this command can produce", *tmpl))
	}
	limit := api.MaxPayloadBytesFor(*tmpl)
	if len(body) > limit {
		return fail(e, api.Newf("E_PAYLOAD_TOO_LARGE",
			"Re-run with fewer passes or categories, or publish with --template bench.report.v1 for a summary.",
			"payload is %d bytes; the limit for %s is %d", len(body), *tmpl, limit))
	}

	if *payloadOut != "" {
		if err := os.WriteFile(*payloadOut, append(append([]byte{}, body...), '\n'), 0o644); err != nil {
			return fail(e, api.Newf("E_INPUT_INVALID", "Pass --payload-out with a writable path.", "could not write %s: %v", *payloadOut, err))
		}
		output.Detailf(e.stderr, "payload written to %s", *payloadOut)
	}

	if *dryRun {
		if *asJSON {
			return emitJSON(e, map[string]any{
				"dry_run": true, "template": *tmpl, "byte_size": len(body), "results": resultsPath,
			})
		}
		output.Successf(e.stdout, "dry run: would publish %s as %s (%d bytes)", resultsPath, *tmpl, len(body))
		return api.ExitOK
	}

	return publishBody(e, body, *tmpl, resultsPath, *apiBase, *asJSON)
}

func failPrepare(e env, path string, err error) int {
	var ie *betterbench.InputError
	switch {
	case errors.Is(err, betterbench.ErrAlreadyPayload):
		return fail(e, api.Newf("E_INPUT_INVALID",
			fmt.Sprintf("This file is already a template payload. Publish it directly: `L80 publish %s`.", path), "%s is %v", path, err))
	case errors.Is(err, betterbench.ErrSyntax):
		return fail(e, api.Newf("E_JSON_INVALID", "Fix the JSON syntax and retry.", "%s: %v", path, err))
	case errors.Is(err, betterbench.ErrNotBetterBench):
		return fail(e, api.Newf("E_INPUT_INVALID", "Pass the results.json written by `betterbench run --out`.", "%s is %v", path, err))
	case errors.As(err, &ie):
		return fail(e, api.Newf("E_INPUT_INVALID", "Shorten that flag's text and retry.", "%s", ie.Msg))
	default:
		return fail(e, api.Newf("E_INPUT_INVALID",
			"Check that this is an unmodified results.json from a supported BetterBench version (0.2.3 or later).", "%s %v", path, err))
	}
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

func betterbenchUsage(e env) {
	fmt.Fprint(e.stderr, `Usage:
  L80 betterbench --endpoint <url> --model <name> [run flags] [-- <betterbench run flags>]
  L80 betterbench --results <results.json> [publish flags]

Runs BetterBench (https://github.com/GGZ14/BetterBench) against an OpenAI-compatible
endpoint and publishes the results.json it writes, as written, to launch80 — minus
env.endpoint and env.host, which never leave this machine. Or publishes a results.json
you already have.

Run flags:
  --quick                 short smoke run; the page will say so
  --passes N, --warmup N  passes per category
  --note KEY=VALUE        recorded in the run, shown as a chip (repeatable); engine=... names the stack
  --decode --prefill --concurrency, --no-prefill --no-concurrency   phase selection
  --out <path>            where results.json goes (default ./betterbench-<model>-<time>.json)
  --api-key <key>         endpoint key (or L80_BETTERBENCH_API_KEY); sent by environment, never recorded
  BetterBench runs at the version pinned in this L80 (see `+"`L80 tools list`"+`): a betterbench on PATH
  that is at least that version, else uvx bootstraps the pinned commit. L80_BETTERBENCH overrides.

Publish flags:
  --title, --summary, --section "Heading=Body"   prose shown on the page
  --template bench.betterbench.v1 (default) | bench.report.v1 (summary mapping; --engine/--hardware apply)
  --dry-run               prepare but do not publish;  --payload-out <path> saves the exact payload
  --json, --api-base      as for L80 publish
`)
}
