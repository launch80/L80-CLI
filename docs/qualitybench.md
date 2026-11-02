# QualityBench ↔ L80 contract

`L80 qualitybench` runs the `qualitybench` console script from
[launch80/QualityBench](https://github.com/launch80/QualityBench). L80 owns
task selection, endpoint discovery and publishing. The tool owns generation,
scoring, statistics and the HTML report. This file is what each side may rely on.
A change to it is a change to both.

Status: **draft**, written before QualityBench's first release. The manifest
lists the tool unpinned until then (`L80 tools check` shows `unreleased`).
`L80_QUALITYBENCH` runs a local build.

## Invocation

```
qualitybench --version                       prints a bare version, e.g. 0.1.0
qualitybench run --endpoint URL --model ID --out PATH
    [--quick] [--tasks a,b,c] [--thinking on|off]
    [--reference REF.json] [--thresholds T.md] [--deadline T]
    [--note KEY=VALUE]...  [extra flags passed through]
qualitybench compare REF.json TEST.json [--thresholds T.md] [--out PATH]
```

- `--endpoint` is a base URL including `/v1`.
- `--tasks` takes ids from the suite table below, in any order. L80 sends it
  only for a subset; with no `--tasks` the tool runs its own default set, which
  must match the table.
- `--quick` runs a fixed tenth of each task's items. The lists are fixed, not
  sampled, so quick runs compare with each other. The results say so.
- `--deadline` takes an RFC 3339 time or a Go-style duration (`6h`, `90m`).
  The tool orders tasks so cheap ones go first, starts no work after the
  deadline, and marks cut tasks in `partials` instead of dropping them.
- Environment: `QUALITYBENCH_API_KEY` is the endpoint's bearer key, which never
  arrives as a flag. `QUALITYBENCH_NO_UPDATE_CHECK=1` means L80 owns version alignment.

Exit status: 0 when the results file is complete or partial by deadline; 1 on
failure; 2 on usage. On any exit the tool keeps what it finished.

## Suite (v1)

| id | items | default |
|---|---|---|
| gsm8k | 1319 | yes |
| ifeval | 541 | yes |
| mmlu_pro | 1000 (fixed subset, 14 subjects) | yes |
| humaneval | 164 | yes |
| mbpp | 378 | yes |
| bfcl | 400 (4 categories) | yes |
| lcb | 175 (v6 slice) | yes |
| logprobs | 592 windows × 2048 | yes |
| needle | 20 (32k and 128k × 10 depths) | yes |
| spec_parity | 32 | no, only when named |

`internal/qualitybench/suite.go` holds the same table, with the measured
minutes the TUI uses for estimates. The two must not drift: a task added to
the tool is added here in the same release that pins it.

## Behaviour L80 relies on

These come from the 2026-11 Qwopus study, where each one failed at least once:
- **Resume:** rerunning the same command skips finished tasks and finished
  shards. L80 tells users to "rerun the same command".
- **Request lifecycle:**
  - Each request gets a long timeout (at least an hour with thinking on) and
    at most 3 attempts.
  - The tool **cancels** a generation it abandons, by closing the connection
    or sending an explicit abort.
  - It never resends a request that is still running server-side. evalplus
    0.3.1 kills a request after 100 s and retries it forever; behind a thinking
    proxy that filled the KV cache with orphaned generations and stalled a run
    for 3.5 h.
- **Warmup:** the first request after start is a warmup and is never scored.
  On llama-swap it pays the model load.
- **Concurrency:** every harness runs concurrently. Sequential harnesses such
  as evalplus codegen are sharded by item range.
- **Sandbox:** code is executed with no network.

## Outputs

- `--out PATH`: the results file, described below.
- `PATH` with `.json` replaced by `.html`: a self-contained HTML report that
  works offline.
- Per-item records and graded text go in a sidecar directory next to the
  results file. They are never part of the publish payload.

### Results file

The results file is a `bench.qualitybench.v1` document **as the server
validates it**, minus `$template`, which L80 adds. Requirements:
- It has `schema_version: 1` and `model`. L80 refuses other versions with a
  pointer to `L80 update`.
- It stays under **256 KB** (the server's limit), so summaries and per-window
  series only.
- It may carry `env.endpoint` and `env.host`. L80 strips both. It must not
  carry any other machine detail: L80 refuses to publish a file with a
  home-directory path (`/home/`, `/Users/`, `~/`, `C:\Users\`) or a URL to
  localhost, a private address or a `.local` host anywhere in it.
- `partials` lists every task cut short.

The schema itself lives with the server template
(`lib/artifacts/schemas/bench-qualitybench-v1.ts` in the launch80 repo).
`lib/skills/sample-qualitybench.json` there is a real example, and
`internal/qualitybench/testdata/results-bf16.json` here is the same file with
`env.endpoint` and `env.host` added back.

### Progress lines (for the console)

While running, the tool prints one line per update to stdout:

```
qb:progress {"task":"gsm8k","done":120,"total":1319}
qb:progress {"task":"gsm8k","state":"scored","correct":1147,"total":1319}
qb:progress {"task":"lcb","state":"partial","done":88,"total":175}
```

The console's QualityBench screen parses these for its per-task progress
view. Any other output is shown as log text.

## Not in this contract yet

- Running on rented GPUs. That comes through `L80 vast` with a
  `benchmark: qualitybench` spec block, and teardown must never wait on the
  user.
- Verifying quality runs (`L80 verify`). It needs a reproducibility rule:
  the same items, and a tolerance band taken from a reference-vs-reference
  noise floor.
