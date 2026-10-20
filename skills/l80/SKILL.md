---
name: l80
description: Drive the launch80 `L80` CLI for the user — benchmark an LLM endpoint with BetterBench, rent a Vast.ai GPU to benchmark on, scan model weights for tampering, check GPU and inference telemetry, review or verify others' benchmark runs and weight scans, and publish results as shareable launch80 pages. Use whenever the user mentions L80, launch80, BetterBench, benchmarking a model or inference server, renting a GPU to test on, or vetting a model download. The detailed skills l80-test-report, l80-vast and l80-pwdet cover publishing, renting and scanning step by step.
---

# Use L80 on the user's behalf

`L80` is a single binary on the user's PATH. Everything the interactive console
(`L80 tui`) does is also a plain command, so you never need the console: run
the commands, read their output, and report back. The user should not have to
drive L80 themselves unless they want to.

## Ground rules

- **Tools are free; publishing needs a token.** Benchmarking, scanning and
  telemetry work with no account. Publishing a page needs a launch80 token the
  user sets up (`L80_TOKEN` or `~/.config/L80/credentials.json`). Never ask the
  user to paste a token, a Vast.ai key or an endpoint key into the chat. If one
  is missing, tell them which variable or file to set and wait.
- **Published pages are public.** Confirm with the user before any command that
  publishes (`publish`, `betterbench` without `--dry-run`, `pwdet --publish`,
  `vast run` with a publish step).
- **Renting costs money.** `L80 vast rent|serve|run` bills the user's Vast.ai
  account until the instance is destroyed. Get explicit consent with the price,
  prefer `--max-hours`, never pass `-y` to `destroy` or `reap`, and make sure
  every instance is torn down. Follow the `l80-vast` skill.
- **Prefer `--json`** where a command takes it, and parse that instead of the
  human output.
- **Check before you guess.** `L80 doctor` explains connectivity, token and tool
  problems; `L80 tools check` explains how each tool resolves.

## Commands

| Goal | Command |
|---|---|
| Is everything set up? | `L80 doctor`, `L80 auth status`, `L80 tools check --json` |
| Benchmark an endpoint and publish | `L80 betterbench --endpoint <url> --model <m> [--quick] [--note k=v]` (quick: 5 passes, verifiable only with a reproduction) |
| Publish an existing BetterBench file | `L80 betterbench --results results.json` |
| Publish any other report | `L80 publish report.json --template <id> [--dry-run] [--json]` (see `l80-test-report`) |
| List templates | `L80 templates list --json` |
| Write a rental spec (model, GPUs, price ceiling) | `L80 vast spec --model <hf repo> --max-dph N` |
| Rent a GPU, serve, bench, publish, tear down | `L80 vast run --spec run.yaml --max-hours 2` (see `l80-vast`) |
| Scan weights for tampering | `L80 pwdet scan <dir\|org/name\|url#model> --profile basic\|standard\|thorough\|deep --json [--publish]` (see `l80-pwdet`) |
| Publish an earlier scan | `L80 pwdet publish <report.json\|dir> [--dry-run] --json` |
| GPU / inference telemetry | `L80 dashboard serve` (web page on :8787), `L80 dashboard tui` |
| What has this key published | `L80 history --json [--local]` |
| Verification | `L80 verify check <results.json\|report.json\|url>`, `L80 verify request <url>`, `L80 verify status <url>` |
| Review others' runs | `L80 verify queue`, `L80 verify reproduce <id> --endpoint <url>` (a pwdet scan needs no endpoint), `L80 verify approve\|reject <id>` (verifier keys) |
| Keep L80 current | `L80 update --check --json`, then `L80 update` |
| Full command list | `L80 help` |

`L80 betterbench` keeps `results.json` in the working directory and strips the
endpoint host before publishing. The endpoint's own key goes in
`L80_BETTERBENCH_API_KEY`, which is never recorded.

## Errors and exit codes

Failures print `error: E_CODE`, a message, and a `remedy:` line. Carry out the
remedy if it is safe, or relay it to the user.

| Exit | Meaning |
|---|---|
| 0 | ok |
| 2 | usage (`E_USAGE`): fix the command line |
| 3 / 4 | input invalid / server rejected the payload (`E_SCHEMA_INVALID`) |
| 5 | token missing or rejected: the user must configure one |
| 6 / 7 / 8 | rate limited / network / server: retry later |
| 10 | unknown template: run `L80 templates list` |
| 11 | a pinned tool is unavailable (`E_TOOL_UNAVAILABLE`): usually install `uv` |
| 12 | `L80 update` left an action: the report names the one step left |

A wrapped tool's own non-zero exit status (BetterBench, bbvastl80, pwdet, the
dashboard) is passed through unchanged; read its output above the status.

## Coming soon

Recommendations (what hardware, engine and quantization to run a model on) and
training, tuning and quantizing models are on the way. Until then, say so rather
than improvising a command that does not exist.

Community and help: https://discord.gg/launch80
