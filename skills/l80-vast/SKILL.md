---
name: l80-vast
description: Rent a cloud GPU on Vast.ai, serve a model on it, run operations on it in order (BetterBench speed, QualityBench quality or a quality compare), publish the results, then tear the box down, using `L80 vast`. Use when the user wants to benchmark or quality-test a model on hardware they do not have, compare GPUs they would rent, or run a BetterBench/QualityBench result "on an H100/H200/4090" in the cloud. Do not use for benchmarking an endpoint the user already runs (use `L80 betterbench`).
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

   **Operations.** `--steps` lists what runs on the box once it serves the model, in
   order. The default is one BetterBench step.
   - `betterbench`: speed.
   - `qualitybench`: quality with thinking off. Add `--reference results.json` to
     compare against an earlier run; its `.qb/` folder must sit beside it.
   - `qualitybench-thinking`: a second run with thinking on. It needs
     `--reasoning-parser` and a context of 32768 or more.

   Related flags:
   - `--qb-tasks gsm8k,ifeval`
   - `--qb-deadline 3h`
   - `--tool-parser hermes`: QualityBench's bfcl needs it.
   - `--no-publish`

   Context: QualityBench's needle needs `--context 131072` for its long half.
   Steps run on this machine through the tunnel, so the code tasks need Docker here.
   Each step writes to `<runs>/<run-id>/<NN>-<label>/`.
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
   its instance. A QualityBench step stops starting work 15 minutes before the cap,
   so its results file is written. Always pass a cap unless the user declines.
   - A BetterBench-only run commonly takes 20–60 minutes.
   - A full QualityBench step takes about 2h40 with thinking off on an RTX PRO 6000,
     plus about 20 minutes to provision.
   - Size `--max-hours` from `L80 qualitybench tasks`, and quote the user the cap
     times the `$/hr` ceiling.
6. The output ends with one line per step (`bbvastl80:step` lines while it runs):
   status, results path and page URL. Give the user the URLs and the run id. A
   failed step does not stop the next one, and the run exits 1 if any step failed.
7. If L80 prints `warning: instance … may still be billing`, tell the user at once
   and offer `L80 vast destroy --run <id>`.

## In the console

`L80 tui` → Rent GPUs → New rental is the same thing as a wizard:
- Model, Hardware (`s` searches offers), Operations (presets such as "Speed + quality"
  and "Compare to a run"), and Review, with per-step time estimates and the cost cap.
- One consent dialog, then a live step list.
- The spec it writes is kept as the record. `L80 vast run --spec` reruns it.

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
