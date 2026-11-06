package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/pytool"
	"github.com/launch80/L80-CLI/internal/qualitybench"
)

// QualityResult is a finished QualityBench run.
type QualityResult struct {
	Path   string // results file as written
	Report string // the HTML report beside it
	Data   []byte
	Runner qualitybench.Runner
	// Partial lists what failed when the tool exited with a failed task; the
	// results are still usable and the page shows them as partial.
	Partial []string
}

// qualityUnavailable explains a missing QualityBench. While the tool is
// unpinned there is no bootstrap, so the remedy names the override.
func qualityUnavailable(err error) *api.Error {
	remedy := "Set L80_QUALITYBENCH to a qualitybench command (e.g. the console script of a local checkout), " +
		"or install QualityBench so `qualitybench` is on PATH."
	var nr *pytool.ErrNoRunner
	if errors.As(err, &nr) && qualitybench.Tool().Pinned() {
		remedy = nr.Remedy()
	}
	return api.Newf("E_TOOL_UNAVAILABLE", remedy, "%v", err)
}

// RunQuality resolves QualityBench, runs it with o, and reads back the results
// file. o.Out must already be an absolute file path.
func RunQuality(ctx context.Context, o qualitybench.RunOptions, s Sink) (QualityResult, *api.Error) {
	runner, err := qualitybench.ResolveRunner()
	if err != nil {
		return QualityResult{}, qualityUnavailable(err)
	}
	s.Note(Step, "running QualityBench via %s", runner.Source)
	for _, n := range runner.Notes {
		s.Note(Warn, "%s", n)
	}
	s.Note(Step, "%s", pytool.FormatArgv(append(append([]string{}, runner.Argv...), o.Args()...)))

	if o.Reference != "" {
		if err := qualitybench.CheckSidecar(o.Reference); err != nil {
			return QualityResult{}, api.Newf("E_INPUT_INVALID",
				"Pass the results file QualityBench wrote, with its .qb folder kept beside it.", "--reference: %v", err)
		}
	}
	data, err := qualitybench.Execute(ctx, runner, o, s.Output(), s.Output())
	if errors.Is(err, qualitybench.ErrPartial) {
		p := qualitybench.Partials(data)
		if len(p) == 0 {
			p = []string{"a task failed (see the log above)"}
		}
		for _, w := range p {
			s.Note(Warn, "partial: %s", w)
		}
		return QualityResult{Path: o.Out, Report: qualitybench.ReportPath(o.Out), Data: data, Runner: runner, Partial: p}, nil
	}
	if err != nil {
		return QualityResult{}, api.Newf("E_INPUT_INVALID",
			fmt.Sprintf("Fix what QualityBench reported above and rerun the same command: finished tasks are kept and skipped. Partial results, if any, are at %s.", o.Out),
			"quality run did not complete: %v", err)
	}
	return QualityResult{Path: o.Out, Report: qualitybench.ReportPath(o.Out), Data: data, Runner: runner}, nil
}

// CompareQuality runs `qualitybench compare` and returns its exit status.
func CompareQuality(ctx context.Context, ref, test, thresholds, out string, s Sink) (int, *api.Error) {
	runner, err := qualitybench.ResolveRunner()
	if err != nil {
		return 0, qualityUnavailable(err)
	}
	for _, f := range []string{ref, test} {
		if err := qualitybench.CheckSidecar(f); err != nil {
			return 0, api.Newf("E_INPUT_INVALID", "Pass results files QualityBench wrote, each with its .qb folder beside it.", "%v", err)
		}
	}
	code, err := qualitybench.Compare(ctx, runner, ref, test, thresholds, out, s.Output(), s.Output())
	if err != nil {
		return 0, api.Newf("E_TOOL_UNAVAILABLE", "Check `L80 tools check qualitybench`.", "could not run qualitybench: %v", err)
	}
	return code, nil
}

// SetupQuality runs `qualitybench setup` and returns its exit status.
func SetupQuality(ctx context.Context, tasks []string, sandbox string, s Sink) (int, *api.Error) {
	runner, err := qualitybench.ResolveRunner()
	if err != nil {
		return 0, qualityUnavailable(err)
	}
	s.Note(Step, "setting up QualityBench via %s: harness environments, the LiveCodeBench image and datasets (minutes, once)", runner.Source)
	code, err := qualitybench.Setup(ctx, runner, tasks, sandbox, s.Output())
	if err != nil {
		return 0, api.Newf("E_TOOL_UNAVAILABLE", "Check `L80 tools check qualitybench`.", "could not run qualitybench: %v", err)
	}
	return code, nil
}
