---
name: l80-qualitybench
description: Measure a model's answer quality, or how much quality a quantized build or another engine loses, with QualityBench via `L80 qualitybench`, then compare two runs and publish the result as a launch80 page. Use when the user asks how good a model is, whether a quant (INT4, FP8, GGUF) or a new engine hurts quality, wants GSM8K/IFEval/MMLU-Pro/HumanEval+/MBPP+/BFCL/LiveCodeBench/KLD numbers, or mentions QualityBench. Use `L80 betterbench` for speed instead.
---

# Measure quality: `L80 qualitybench`

`L80 qualitybench` runs QualityBench, pinned in this L80 release, against an
OpenAI-compatible endpoint. It writes three things, by default in
`~/.local/state/L80/qualitybench/` (`$L80_QUALITYBENCH_DIR`, or `--out` for one run):
- a results file;
- an HTML report beside it;
- a `<name>.qb/` folder of per-item records, which compare needs.

It can then publish the results as a launch80 page. Running is free; only publishing needs a token.

## Before a run

1. `L80 qualitybench endpoints` lists the servers on this machine and what
   each serves. With no `--endpoint`, L80 picks the server that lists `--model`.
2. `L80 qualitybench tasks` lists the suite, with minutes measured on the
   reference run.
3. `L80 qualitybench setup` installs the harness environments, the
   LiveCodeBench image and the datasets in advance. This takes minutes once.
   A run also does it on its own.
4. Server requirements. Tell the user before starting if one is missing:
   - **logprobs** needs vLLM's `prompt_logprobs`. Start vLLM with
     `--max-logprobs 64` and speculative decoding off. On llama.cpp, radiance or
     another server without it, leave logprobs out of `--tasks`. The task scores
     still publish, and the page shows perplexity and KLD as n/a.
   - **bfcl** scores the server's tool parser
     (`--enable-auto-tool-choice --tool-call-parser ...`).
   - **needle** needs a 128k context. A shorter one skips that length and the
     page says so.
   - **--thinking on** needs a reasoning parser.
   - **A model that thinks by default** needs `--thinking off` (the baseline)
     or `--thinking on`. Without one, the harnesses' short token limits cut its
     replies off mid-thought. For a large thinking model on code, also pass
     `--thinking-max-tokens 32768` or more (32768 is the default from
     QualityBench 0.3.0). If the results carry a "stopped at the token cap"
     caveat, that task's score understates the model.
   - **The code tasks** (humaneval, mbpp, lcb) need Docker or podman on this
     machine. For rootless podman (Fedora), set `QUALITYBENCH_CONTAINER=podman`.

## Run

```sh
L80 qualitybench --model <id> --quick --no-publish          # a smoke run, about a tenth of each task
L80 qualitybench --model <id> --no-publish --note precision=BF16 --note engine="vLLM 0.30"
```

- `--tasks gsm8k,ifeval` runs a subset. `--thinking on|off` forces reasoning.
- `--deadline 6h` stops starting work after that. Cut tasks are listed as partial.
- `--tokenizer org/repo` sets the tokenizer when the served id is an alias
  (llama-swap). L80 defaults it to the server's `root`.
- The endpoint key goes in `L80_QUALITYBENCH_API_KEY`. Never put it on the
  command line, and never ask the user to paste it into the chat.
- To resume an interrupted or partial run, run the same command again.
  Finished work is kept.
- Exit 1 means a task failed but the results were still written, and
  published unless `--no-publish`. Read the `partial:` lines and relay them.

A full run takes hours: about 2h40 with thinking off on the reference run.
Suggest `--quick` first.

## How long it takes: ask before running

Runs are long, so **quote the estimate to the user and get a yes before you
start one**:

```sh
L80 qualitybench --model <id> --quick --estimate-only --json   # measures the server, runs nothing
```

- Read `estimate.total_min`, `estimate.range_min` and `estimate.basis` and tell
  the user, e.g. "about 38 min (27–57), measured on this server just now".
  `estimate.setup_min` > 0 is a one-off first-run setup. A `warning` (an older
  QualityBench, or the server not reachable) means the figure is only the
  reference table: say so.
- Once they agree, run with `--yes`. Without a terminal L80 prints the estimate
  and starts anyway, so the yes is yours to get.
- People who do not want the question set
  `L80 settings set qualitybench.confirm never` (or `over:30m` to be asked only
  for longer runs). Only change it when the user asks you to.

## Compare

```sh
L80 qualitybench compare bf16.json int4.json [--thresholds T.md]
```

- Both files need their `.qb/` folders beside them, the same tier (both full
  or both quick) and the same tokenizer.
- Compare prints KLD, top-1 agreement, the perplexity change and the paired
  per-task deltas (McNemar p and a bootstrap CI), then a verdict per threshold.
- It writes `int4-vs-bf16.json` and `.html`.
- `--reference ref.json` on a run does the run and the compare in one go.

## Publish

**Confirm with the user first: pages are public.**

```sh
L80 qualitybench --results int4-vs-bf16.json --dry-run
L80 qualitybench --results int4-vs-bf16.json
```

L80 removes `env.endpoint` and `env.host` before sending. Give the user the
URL it prints.

## On a rented GPU

To measure a model the user cannot serve locally, use `l80-vast` with a
`qualitybench` step:

```sh
L80 vast spec --model <repo> --steps qualitybench --tool-parser hermes --context 32768
```

QualityBench runs on this machine against the rental, through the tunnel.
Each step's results land in the run's folder.

## In the console

`L80 tui` → QualityBench:
- **Run:** pick a scope, a server and a model. Run measures the server and
  shows how long the run will take, then asks (Start / Start, don't ask again /
  Cancel; Settings changes it). While it runs: an overall bar with the time
  left, per-task bars, and the job title shows `48% · 23 min left`. `p`
  publishes and `o` opens the report.
- **Compare:** pick two local runs.
- **Rent via Vast:** hands the model to Rent with a QualityBench step.
