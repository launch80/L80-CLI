---
name: l80-qualitybench
description: Measure a model's answer quality, or how much quality a quantized build or another engine loses, with QualityBench via `L80 qualitybench`, then compare two runs and publish the result as a launch80 page. Use when the user asks how good a model is, whether a quant (INT4, FP8, GGUF) or a new engine hurts quality, wants GSM8K/IFEval/MMLU-Pro/HumanEval+/MBPP+/BFCL/LiveCodeBench/KLD numbers, or mentions QualityBench. Use `L80 betterbench` for speed instead.
---

# Measure quality: `L80 qualitybench`

`L80 qualitybench` runs QualityBench, pinned in this L80 release, against an
OpenAI-compatible endpoint. It writes three things:
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
     `--max-logprobs 64` and speculative decoding off.
   - **bfcl** scores the server's tool parser
     (`--enable-auto-tool-choice --tool-call-parser ...`).
   - **needle** needs a 128k context. A shorter one skips that length and the
     page says so.
   - **--thinking on** needs a reasoning parser.
   - **The code tasks** (humaneval, mbpp, lcb) need Docker on this machine.

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
- **Run:** pick a scope, a server and a model; live per-task progress; `p`
  publishes and `o` opens the report.
- **Compare:** pick two local runs.
- **Rent via Vast:** hands the model to Rent with a QualityBench step.
