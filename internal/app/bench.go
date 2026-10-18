package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/betterbench"
	"github.com/launch80/L80-CLI/internal/pytool"
)

// BenchResult is a finished BetterBench run.
type BenchResult struct {
	Path    string // results.json as written
	Data    []byte
	Runner  betterbench.Runner
	Version string // betterbench_version recorded in the file
}

// RunBenchmark resolves BetterBench, runs it with o, and reads back the
// results file. o.Out must already be an absolute file path.
func RunBenchmark(ctx context.Context, o betterbench.RunOptions, s Sink) (BenchResult, *api.Error) {
	runner, err := betterbench.ResolveRunner()
	if err != nil {
		var nr *pytool.ErrNoRunner
		remedy := "Install uv (https://docs.astral.sh/uv/) so L80 can bootstrap BetterBench, or install BetterBench itself " +
			"so `betterbench` is on PATH. L80_BETTERBENCH=/path/to/betterbench also works."
		if errors.As(err, &nr) {
			remedy = nr.Remedy()
		}
		return BenchResult{}, api.Newf("E_TOOL_UNAVAILABLE", remedy, "%v", err)
	}
	s.Note(Step, "running BetterBench via %s", runner.Source)
	for _, n := range runner.Notes {
		s.Note(Warn, "%s", n)
	}
	s.Note(Step, "%s", pytool.FormatArgv(append(append([]string{}, runner.Argv...), o.Args()...)))

	data, err := betterbench.Execute(ctx, runner, o, s.Output(), s.Output())
	if err != nil {
		return BenchResult{}, api.Newf("E_INPUT_INVALID",
			fmt.Sprintf("Fix what BetterBench reported above and retry. A partial results file, if any, is at %s.", o.Out),
			"benchmark did not complete: %v", err)
	}
	res := BenchResult{Path: o.Out, Data: data, Runner: runner}
	if r, perr := betterbench.Parse(data); perr == nil {
		res.Version = r.BetterBenchVersion
		for _, w := range ResultWarnings(r) {
			s.Note(Warn, "%s", w)
		}
	}
	return res, nil
}

// ResultWarnings are the caveats L80 adds about a results file before it is
// published: version drift from the pinned BetterBench, and prefix-cache
// hits on a run that claims a cold cache.
func ResultWarnings(r *betterbench.Results) []string {
	var out []string
	pinned := betterbench.Tool().MinVersion
	switch v := r.BetterBenchVersion; {
	case v == "":
	case pytool.Compare(v, "0.5.0") < 0:
		out = append(out, fmt.Sprintf("this file was written by BetterBench %s; launch80 renders it, but %s is current and fixes measurement bugs in older versions", v, pinned))
	case pytool.Compare(v, pinned) > 0:
		out = append(out, fmt.Sprintf("this file was written by BetterBench %s, newer than the %s this L80 release was validated against; run `L80 update`", v, pinned))
	}
	if hits := r.CacheHits(); hits > 0 && r.Config.UniqueNonce {
		out = append(out, fmt.Sprintf("%d request(s) were served from the server's prefix cache although the run asked for a cold cache; those timings are cache lookups", hits))
	}
	return out
}
