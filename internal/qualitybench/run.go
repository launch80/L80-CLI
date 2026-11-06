package qualitybench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/launch80/L80-CLI/internal/pytool"
)

// Runner is a resolved way to invoke the qualitybench CLI.
type Runner = pytool.Runner

// ErrNoRunner means QualityBench cannot be run on this machine; errors.As it
// to a *pytool.ErrNoRunner for the remedy.
var ErrNoRunner = errors.New("qualitybench is not installed and no pinned build is available to bootstrap")

// Tool returns QualityBench's manifest entry.
func Tool() pytool.Tool {
	t, err := pytool.Lookup("qualitybench")
	if err != nil {
		// The manifest is embedded and tested; this cannot happen in a build.
		panic(err)
	}
	return t
}

// ResolveRunner finds how to run QualityBench: L80_QUALITYBENCH, then a
// qualitybench on PATH, then (once this build pins a commit) a uvx bootstrap.
func ResolveRunner() (Runner, error) {
	r, err := pytool.Resolve(Tool())
	if err != nil {
		return Runner{}, fmt.Errorf("%w: %v", ErrNoRunner, err)
	}
	return r, nil
}

// RunOptions are the parts of `qualitybench run` L80 exposes by name.
type RunOptions struct {
	Endpoint string
	Model    string
	// Out is the results file. The tool writes its HTML report beside it.
	Out   string
	Scope Scope
	// Thinking is "on", "off" or "" (the server's default). The tool's proxy
	// enforces it and preflights that reasoning is present or absent.
	Thinking string
	// Reference is a results file of the same suite; with it the tool also
	// runs compare and writes paired deltas, KLD and threshold verdicts.
	Reference  string
	Thresholds string
	// Deadline is an RFC 3339 time or a duration such as 6h. Tasks cut by it
	// are marked partial in the results, never dropped.
	Deadline string
	Notes    []string // KEY=VALUE
	// Tokenizer is the Hugging Face repo (or local dir) logprobs and needle
	// tokenize with, when the served id is not one (a llama-swap alias).
	Tokenizer string
	// Sandbox is "docker" (default) or "native"; native runs model-written
	// code directly and is only for disposable rented boxes.
	Sandbox string
	Extra   []string // passed through after the named flags
	// Yes passes --yes: L80 has already asked. Only QualityBench 0.2.0+
	// knows the flag, so set it only when the tool proved it (Estimate.FromTool).
	Yes bool
	// APIKey reaches the child as QUALITYBENCH_API_KEY, never on the command line.
	APIKey string
}

// Env is the environment the run adds for the child.
func (o RunOptions) Env() []string {
	env := []string{"QUALITYBENCH_NO_UPDATE_CHECK=1"}
	if o.APIKey != "" {
		env = append(env, "QUALITYBENCH_API_KEY="+o.APIKey)
	}
	return env
}

// Args builds the argv for `qualitybench run`. A full run of the default set
// sends no --tasks, so the tool's own suite definition stays the authority.
func (o RunOptions) Args() []string {
	args := []string{"run", "--endpoint", o.Endpoint, "--model", o.Model, "--out", o.Out}
	if o.Scope.Tier == TierQuick {
		args = append(args, "--quick")
	}
	if len(o.Scope.Tasks) > 0 && !IsDefaultSet(o.Scope.Tasks) {
		args = append(args, "--tasks", strings.Join(o.Scope.Tasks, ","))
	}
	if o.Thinking != "" {
		args = append(args, "--thinking", o.Thinking)
	}
	if o.Reference != "" {
		args = append(args, "--reference", o.Reference)
	}
	if o.Thresholds != "" {
		args = append(args, "--thresholds", o.Thresholds)
	}
	if o.Deadline != "" {
		args = append(args, "--deadline", o.Deadline)
	}
	if o.Tokenizer != "" {
		args = append(args, "--tokenizer", o.Tokenizer)
	}
	if o.Sandbox != "" {
		args = append(args, "--sandbox", o.Sandbox)
	}
	for _, n := range o.Notes {
		args = append(args, "--note", n)
	}
	if o.Yes {
		args = append(args, "--yes")
	}
	return append(args, o.Extra...)
}

// ReportPath is where the tool writes the HTML report for a results file.
func ReportPath(out string) string {
	if s, ok := strings.CutSuffix(out, ".json"); ok {
		return s + ".html"
	}
	return out + ".html"
}

// ExitTaskFailed is the tool's status when a task failed: the results file
// is still written, with that task listed in `partials`.
const ExitTaskFailed = 1

// ErrPartial is returned with the results when the tool exited
// ExitTaskFailed: the data is usable and lists what failed.
var ErrPartial = errors.New("a task failed; the results list it under partials")

// Execute runs `qualitybench run`, streaming its output, and returns the
// results file it wrote. Cancelling ctx interrupts the run; the tool keeps
// what it finished, so the same command resumes it. When a task failed the
// data comes back with ErrPartial.
func Execute(ctx context.Context, r Runner, o RunOptions, out, errOut io.Writer) ([]byte, error) {
	code, err := pytool.Exec(ctx, r, o.Args(), pytool.ExecOpts{Stdout: out, Stderr: errOut, ExtraEnv: o.Env()})
	if err != nil {
		return nil, err
	}
	if code != 0 && code != ExitTaskFailed {
		return nil, fmt.Errorf("qualitybench exited with status %d", code)
	}
	data, rerr := os.ReadFile(o.Out)
	if rerr != nil {
		if code != 0 {
			return nil, fmt.Errorf("qualitybench exited with status %d and wrote no %s", code, o.Out)
		}
		return nil, fmt.Errorf("qualitybench finished but %s was not written: %w", o.Out, rerr)
	}
	if code == ExitTaskFailed {
		return data, ErrPartial
	}
	return data, nil
}

// Partials lists the `partials` of a results file: tasks cut by the deadline,
// refused by the server, or failed.
func Partials(data []byte) []string {
	var d struct {
		Partials []any `json:"partials"`
	}
	if json.Unmarshal(data, &d) != nil {
		return nil
	}
	var out []string
	for _, p := range d.Partials {
		switch v := p.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			if t, ok := v["task"].(string); ok {
				out = append(out, t)
			}
		}
	}
	return out
}

// SidecarDir is the per-item folder the tool keeps beside a results file;
// compare and --reference need it.
func SidecarDir(results string) string {
	if s, ok := strings.CutSuffix(results, ".json"); ok {
		return s + ".qb"
	}
	return results + ".qb"
}

// CheckSidecar reports why results cannot be used as a reference, or nil.
func CheckSidecar(results string) error {
	if _, err := os.Stat(results); err != nil {
		return fmt.Errorf("%s: %w", results, err)
	}
	if info, err := os.Stat(SidecarDir(results)); err != nil || !info.IsDir() {
		return fmt.Errorf("%s has no %s folder beside it (compare needs the per-item records)", results, SidecarDir(results))
	}
	return nil
}

// Setup runs `qualitybench setup`: the harness environments, the LCB image
// and the datasets, so the first run does not pay for them.
func Setup(ctx context.Context, r Runner, tasks []string, sandbox string, out io.Writer) (int, error) {
	args := []string{"setup"}
	if len(tasks) > 0 {
		args = append(args, "--tasks", strings.Join(tasks, ","))
	}
	if sandbox != "" {
		args = append(args, "--sandbox", sandbox)
	}
	return pytool.Exec(ctx, r, args, pytool.ExecOpts{Stdout: out, Stderr: out, ExtraEnv: []string{"QUALITYBENCH_NO_UPDATE_CHECK=1"}})
}

// Compare runs `qualitybench compare ref test` locally; no token is involved.
func Compare(ctx context.Context, r Runner, ref, test, thresholds, out string, stdout, stderr io.Writer) (int, error) {
	args := []string{"compare", ref, test}
	if thresholds != "" {
		args = append(args, "--thresholds", thresholds)
	}
	if out != "" {
		args = append(args, "--out", out)
	}
	return pytool.Exec(ctx, r, args, pytool.ExecOpts{Stdout: stdout, Stderr: stderr, ExtraEnv: []string{"QUALITYBENCH_NO_UPDATE_CHECK=1"}})
}
