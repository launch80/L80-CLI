---
name: l80-vast
description: Rent a cloud GPU on Vast.ai, serve a model on it, benchmark it with BetterBench and publish the result, then tear the box down, using `L80 vast`. Use when the user wants to benchmark a model on hardware they do not have, compare GPUs they would rent, or run a BetterBench result "on an H100/H200/4090" in the cloud. Do not use for benchmarking an endpoint the user already runs (use `L80 betterbench`).
---

# Rent a GPU and benchmark a model: `L80 vast`

`L80 vast` runs bbvastl80, pinned in this L80 release. Every bbvastl80 command
works through it (`L80 vast <command> [args]`). L80 adds the wiring: bbvastl80
calls back into this L80 to publish and into its pinned BetterBench to measure,
runs are kept in one place, and after a run L80 names any instance that may
still be billing.

**Renting costs the user money until the instance is destroyed.** Treat every
command that rents (`rent`, `serve`, `run`) as a spending decision the user
makes, not one you make.

## Steps

1. Check setup: `L80 vast doctor`. It needs a Vast.ai key (`VAST_API_KEY`, or
   `~/.config/bbvastl80/credentials`), an SSH key, and an L80 token if the run
   publishes. Ask the user to fix anything it flags. Never ask them to paste a key
   into the chat.
2. Get a spec. A YAML file names the GPU filter and price ceiling (`gpu.max_dph`),
   the model, the serving engine (vLLM or llama.cpp), the benchmark and the publish
   step. Write one with flags rather than by hand:

   ```sh
   L80 vast spec --model Qwen/Qwen3-8B --gpu "RTX 4090,RTX 3090" --max-dph 0.6 --budget 3
   ```

   It prints the path (under `~/.local/state/L80/vast/specs`; `-o` writes elsewhere,
   `--print` only prints). Other flags: `--engine vllm|llamacpp`, `--gpus N`,
   `--min-vram GB`, `--context N`, `--disk GB`, `--quick`, `--name`, `--force`.
   GPU names are Vast's own (`RTX 4090`, `H100 SXM`); `L80 vast search` shows them.
   The repository's `run.example.yaml` documents every other field. Show the user
   the `gpu`, `budget` and `model` sections before going on.
3. Look before renting: `L80 vast search --spec run.yaml` lists matching offers and
   prices. It does not bill.
4. Dry run: `L80 vast run --spec run.yaml --dry-run`.
5. **Ask the user to confirm** the GPU, the hourly price and a time limit. Then:

   ```sh
   L80 vast run --spec run.yaml --max-hours 2
   ```

   `--max-hours` is an L80 watchdog: when it fires, L80 stops the run and destroys
   its instance. Always pass one unless the user declines. A full run commonly takes
   20–60 minutes.
6. Give the user the published URL from the output, and the run id.
7. If L80 prints `warning: instance … may still be billing`, tell the user at once
   and offer `L80 vast destroy --run <id>`.

## Rules

- Never pass `-y` to `destroy` or `reap` yourself. Those commands ask before they
  destroy; let the user answer.
- `L80 vast status --json` lists every run L80 knows about and which may be billing.
- `L80 vast reap` destroys every instance bbvastl80 labelled. Suggest it when the
  user is unsure what is still running.
- Ctrl-C once lets a run tear its box down; twice forces it to stop, and then the box
  may survive. Follow it with `status --json`.
- Flags after `--` go to bbvastl80 untouched.
- Exit status is bbvastl80's own (0 success, 1 failure); 11 means L80 could not start
  it (see `L80 tools check vast`).

## Keeping L80 current

`L80 update --json` brings everything up to date in one command: the L80
binary, the pinned tools (BetterBench, bbvastl80, pwdet, the dashboard) and
the installed L80 skills. Exit 0 with `"up_to_date": true` means done. Exit 12
means it did what it could: each `actions[]` entry is one exact instruction it
could not carry out itself, most often an old override variable in a shell
profile. Show those to the user and ask before editing their profile. If the
output has no `actions` key, the old L80 just replaced itself: run
`L80 update --json` once more. `L80 update --check --json` reports the same
thing without changing anything.
