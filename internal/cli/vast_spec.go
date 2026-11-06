package cli

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/output"
	"github.com/launch80/L80-CLI/internal/qualitybench"
	"github.com/launch80/L80-CLI/internal/vast"
)

// runVastSpec writes a run spec from a few flags, so nobody has to start
// from a blank YAML file. It does not need bbvastl80 and rents nothing.
func runVastSpec(e env, args []string) int {
	d := vast.DefaultSpec()
	fs2 := flag.NewFlagSet("vast spec", flag.ContinueOnError)
	fs2.SetOutput(e.stderr)
	model := fs2.String("model", "", "Hugging Face repo with the weights, e.g. Qwen/Qwen3-8B (required)")
	id := fs2.String("id", "", "model id served at /v1 (default: the repo)")
	engine := fs2.String("engine", d.Engine, "serving engine: "+strings.Join(vast.Engines, " | "))
	gpu := fs2.String("gpu", "", "GPU names, comma-separated, e.g. \"RTX 4090,A100 SXM4\" (default: any)")
	gpus := fs2.Int("gpus", d.GPUCount, "number of GPUs")
	vram := fs2.Float64("min-vram", d.MinGPURAMGB, "minimum total GPU memory, GB")
	dph := fs2.Float64("max-dph", d.MaxDPH, "price ceiling, $/hr")
	budget := fs2.Float64("budget", d.BudgetUSD, "budget for the whole run, $")
	ctxLen := fs2.Int("context", d.MaxModelLen, "max context length (0: the engine's default)")
	disk := fs2.Int("disk", d.DiskGB, "instance disk, GB")
	quick := fs2.Bool("quick", false, "short runs: BetterBench quick and the QualityBench quick tier")
	steps := fs2.String("steps", "", "operations on the box, in order: betterbench, qualitybench, qualitybench-thinking (default: betterbench)")
	qbTasks := fs2.String("qb-tasks", "", "QualityBench task ids, comma-separated (default: the suite)")
	reference := fs2.String("reference", "", "a local QualityBench results file the qualitybench step compares against")
	thresholds := fs2.String("thresholds", "", "thresholds file for that compare")
	qbDeadline := fs2.String("qb-deadline", "", "time budget per QualityBench step, e.g. 3h")
	toolParser := fs2.String("tool-parser", "", "vLLM --tool-call-parser (QualityBench bfcl scores it), e.g. hermes")
	reasoning := fs2.String("reasoning-parser", "", "vLLM --reasoning-parser (needed for qualitybench-thinking), e.g. qwen3")
	noPublish := fs2.Bool("no-publish", false, "keep every step's results local")
	name := fs2.String("name", "", "spec name (default: from the model)")
	out := fs2.String("o", "", "write here instead of "+vast.SpecsDir())
	force := fs2.Bool("force", false, "overwrite an existing spec")
	printOnly := fs2.Bool("print", false, "print the YAML instead of writing it")
	asJSON := fs2.Bool("json", false, "emit machine-readable JSON")
	if err := fs2.Parse(args); err != nil {
		return api.ExitUsage
	}

	s := vast.Spec{Name: *name, ModelRepo: *model, ModelID: *id, Engine: *engine, GPUCount: *gpus,
		MinGPURAMGB: *vram, MaxDPH: *dph, BudgetUSD: *budget, MaxModelLen: *ctxLen, DiskGB: *disk, Quick: *quick}
	s.GPUNames = vast.ParseGPUNames(*gpu)
	s.ToolCallParser, s.ReasoningParser = *toolParser, *reasoning
	if *steps != "" {
		var tasks []string
		if *qbTasks != "" {
			ts, err := qualitybench.ParseTasks(*qbTasks)
			if err != nil {
				return fail(e, api.Newf("E_USAGE", "Pick tasks from `L80 qualitybench tasks`.", "--qb-tasks: %v", err))
			}
			tasks = ts
		}
		for _, name := range strings.Split(*steps, ",") {
			st := vast.Step{Quick: *quick, Publish: !*noPublish}
			switch strings.TrimSpace(name) {
			case "betterbench":
				st.Type = vast.StepBetterBench
			case "qualitybench", "qualitybench-thinking":
				st.Type, st.Tasks, st.Thresholds, st.Deadline = vast.StepQualityBench, tasks, *thresholds, *qbDeadline
				st.Reference = *reference
				// Off is the baseline: a chat-template switch, ignored by models without thinking.
				st.Thinking, st.Label = "off", "quality"
				if strings.HasSuffix(name, "-thinking") {
					st.Thinking, st.Label, st.Reference = "on", "quality-thinking", ""
				}
			default:
				return fail(e, api.Newf("E_USAGE", "Use betterbench, qualitybench or qualitybench-thinking.", "--steps: unknown step %q", name))
			}
			s.Steps = append(s.Steps, st)
		}
	}
	if *model == "" {
		return fail(e, api.Newf("E_USAGE",
			"Pass the weights' repo, e.g. `L80 vast spec --model Qwen/Qwen3-8B --gpu \"RTX 4090\" --max-dph 0.6`, or build one in `L80 tui` (Rent, New rental).",
			"--model is required"))
	}
	if err := s.Validate(); err != nil {
		return fail(e, api.Newf("E_INPUT_INVALID", "Fix the flag and run it again.", "%v", err))
	}
	if *printOnly {
		fmt.Fprint(e.stdout, s.YAML())
		return api.ExitOK
	}

	path, err := vast.WriteSpec(s, *out, *force)
	if errors.Is(err, vast.ErrSpecExists) {
		return fail(e, api.Newf("E_USAGE", "Pass --force to replace it, or --name to write a new one.", "%v", err))
	}
	if err != nil {
		return fail(e, api.Newf("E_INTERNAL", "Check that the directory is writable, or pass -o.", "%v", err))
	}
	if *asJSON {
		return emitJSON(e, map[string]any{"spec": path})
	}
	output.Successf(e.stdout, "wrote %s", path)
	output.Detailf(e.stdout, "Look at matching offers: L80 vast search --spec %s", path)
	output.Detailf(e.stdout, "Try it without renting: L80 vast run --spec %s --dry-run", path)
	output.Detailf(e.stdout, "Rent and run it:        L80 vast run --spec %s --max-hours 2", path)
	return api.ExitOK
}
