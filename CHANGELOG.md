# Changelog

## 1.1.0

- **Quick runs can be verified.** A BetterBench run with 5 to 19 passes and
  at least one warmup (`--quick` is 5 after 1) is now its own run class,
  derived from the page's stored config. A quick run can reach verified, but
  only against a reproduction within wider tolerances (decode 15%, ITL 25%,
  TTFT 40%, peak aggregate 25%, prefill 30%), compared on medians; p99 and
  1% low figures are skipped. `--accept-differences` does not apply. The badge
  reads "Verified by Launch80 · quick run (N passes)". Under 5 passes, or with
  no warmup, stays a smoke run that cannot be verified.
- `L80 verify approve` refuses a quick run that has no reproduction
  (`E_REPRODUCTION_REQUIRED`). `check`, `request` and `show` explain what a
  quick stamp means; `queue`, `mine`, `show` and History gain a CLASS column,
  and JSON outputs gain `run_class`. On the console's Verify screen, `a` on an
  unreproduced quick run opens the reproduce form.
- Pages made from quick runs say "verifiable as a quick run" instead of "not a
  publishable measurement".
- The precheck formerly called `full_run` is now `run_class`.
- Server side: the verification service needs the 1.1 changes listed in
  `docs/verification-contract.md` before it stamps quick runs; until then it
  keeps refusing them and L80 reports its decision.

## 1.0.1

- The console's pwdet screen is now labelled **Poison Detector** in the
  sidebar. The command is still `L80 pwdet`.
- **L80 builds the rental spec for you.** `L80 vast spec --model <hf repo>`
  writes a run spec from a few flags (engine, GPU types, count, memory, price
  ceiling, budget, context, disk) to `~/.local/state/L80/vast/specs`, checked
  before it is written. On the console's Rent screen, `b` (or ctrl+b on New
  run) opens the same thing as a form, and starting a run with no spec opens
  it instead of failing. The newest spec is filled in for you. GPU names are
  written the way Vast matches them (`RTX_4090` becomes `RTX 4090`). The old
  hint pointed at `L80 vast init`, which only writes a `.env` for the Vast key.

## 1.0.0

First stable release. It ships everything listed under 0.5.0 to 0.8.0 below;
those were development milestones and were never released on their own.

- **`L80 update` updates everything**, not just the binary: after replacing
  itself it hands over to the new binary, which builds the pinned tools now,
  upgrades a uv-installed tool on PATH that is older than its pin, and
  refreshes the installed L80 skills. What it cannot change (an override such
  as `L80_BETTERBENCH` pointing at an old copy) comes back as an action that
  names the profile line setting it. `--json` gives the whole report;
  `--check` reports without changing anything; `--no-prefetch` skips the
  downloads. New exit code 12: finished, with actions left for a person.
  From 0.4.0, run `L80 update` twice: the first run installs 1.0.0, the
  second syncs.

- **Your AI agents can drive L80.** A new umbrella `l80` skill teaches an agent
  the whole command surface, and `L80 skills link` installs skills for any
  harness: `--target claude-code | agents (Codex, Cursor) | gemini | opencode |
  detected`, or `--dir PATH`; `--all` installs every skill. `L80 skills targets`
  shows what is found and installed; `L80 skills agents-md` prints an AGENTS.md
  snippet for agents without skills. `L80 update` refreshes copies in every
  harness, and its JSON skill rows gain `target`.
- **Onboarding.** The first `L80 tui` opens a short guided setup: check the
  tools, save a publish token, pick where to start, install skills for the
  agents found. Skipping is remembered too (`~/.config/L80/state.json`).
  `L80 onboard` or the sidebar runs it again.
- The default server is **`https://www.launch80.com`**. The apex redirects
  every request to www, and Go drops the token on that cross-host redirect, so
  an `L80_API_BASE` of `https://launch80.com` is sent to www directly.
- Verification shows **`requester_label` and `reviewer_label`**: who asked and
  who reviewed are named by key label in `verify queue | show | status | mine`
  and the console, falling back to key ids only when no label is sent.
- `L80 vast`, `L80 pwdet` and `L80 dashboard` work out of the box: bbvastl80,
  pwdet and the AI command dashboard are public under github.com/launch80 and
  pinned at v0.1.0 (`L80 tools list`).
- **`L80 tui` gets a sidebar.** Every screen is listed on the left; moving
  through the list previews each one live in the pane, enter opens it, esc
  goes back. `1`-`9` jump, `?` opens help. Amber accent throughout. Home
  sits on top and the rest is A to Z; Vast is now **Rent (vast)**. New screens:
  **About** (our story, the Discord, the Supporter page and the full command
  directory), **Onboarding**, and two previews of what is coming,
  **Recommendations** and **Train & Tune** (train, tune, quantize).
- **Verification is a community workflow.** Any key sees other keys' requests
  (`L80 verify queue`, the console's new Verify screen) and can reproduce them:
  `L80 verify reproduce <id> --endpoint <url>` reruns BetterBench with the
  published settings on your hardware and compares the headline figures.
  Verifier keys (`publisher_keys.is_verifier`) stamp with `L80 verify approve`
  or reject with `L80 verify reject --reason`, never on their own requests;
  reproducing first is optional, and a reproduction goes with the stamp.
  `L80 review` remains as an alias. Tool output now streams live into the
  console's log (Python tools run unbuffered).
- The repository is renamed **launch80/L80-CLI** (was L80-Skills); the Go module
  path follows. `L80 update` now fetches releases from L80-CLI. Older binaries
  keep updating through GitHub's redirect from the old name.

## 0.8.0

- **"Verified by Launch80"**, client side. `L80 verify check <results.json | url>`
  runs the checks a review applies (full run, all phases, cold cache with no
  prefix-cache hits, request health, BetterBench 0.6.1+, engine and hardware
  notes) plus a provenance check that the page came, unchanged, from a file this
  machine published. `L80 verify request <url> --note …` asks for review;
  `L80 verify status` follows it. Reviewer keys get `L80 review list | approve |
  reject`. The console's history screen gains `v` to request verification.
- `L80 history` shows each page's trust tier and takes `--tier`.
- The server contract (data model, endpoints, checks, rendering) is in
  docs/verification-contract.md. Until the server implements it, requests report
  that it is not supported (exit 8) and checks still run locally.
- docs/awesomelist-integration.md analyses a future `L80 recommend` built on the
  Launch80 AwesomeList catalog.

## 0.7.0

- **`L80 tui`**: a full-screen console for everything L80 does. Screens for
  benchmarking an endpoint (form, streamed run, summary with caveats, publish),
  renting a GPU (runs table with what may still be billing, new-run form, a
  confirmation that quotes the spec's price ceiling and budget, destroy, reap,
  ssh, a watchdog), scanning weights (form, verdict, findings, publish), the
  dashboard, published pages and tools. Destructive and public actions always
  ask first, with the safe choice focused.
- Built on the standard library only: raw mode through termios ioctls, a
  line-diff renderer, an input decoder with xterm modifiers, SGR mouse and
  bracketed paste, and a widget set (list with filter, table, text input,
  form, log pane, dialog). The design follows Bubble Tea, Bubbles and Lip Gloss;
  see THIRD_PARTY_NOTICES.md.
- Tool output shown in the console is sanitised: a subprocess cannot move the
  cursor or retitle the window.
- `L80` with no arguments on a terminal mentions `L80 tui`.

## 0.6.0

### New: the Launch80 tools behind L80, free to use

- **`L80 vast`** runs bbvastl80: rent a Vast.ai GPU, serve a model, benchmark it
  with BetterBench from this machine, publish, tear down. Every bbvastl80 command
  passes through. L80 points bbvastl80 at this L80 and at the pinned BetterBench
  (through a small shim, so your own `L80_BETTERBENCH` is honoured), keeps runs in
  `~/.local/state/L80/vast/runs`, and after `run` names any instance that may still be
  billing. `--max-hours N` is a watchdog that stops the run and destroys its
  instance. `status --json` reads the run state files. L80 never answers a
  confirmation prompt.
- **`L80 pwdet`** runs the Poisoned Weights Detector. `scan … --json` reports the
  verdict and report path; `--publish` puts the report on launch80 as a
  `generic.report.v1` page (token-gated like every publish) with the hostname,
  local paths and private endpoints removed, and refuses if any would remain.
  pwdet's verdict exit status passes through.
- **`L80 dashboard`** opens the AI command dashboard terminal UI; `serve` runs its
  sampler and web page, and refuses a second sampler on the same port.
- **`L80 tools run <name> [args]`** runs any pinned tool with its arguments untouched.
- New skills: `l80-vast`, `l80-pwdet` (`L80 skills link --target claude-code --name …`).
- A wrapped tool's own exit status is returned unchanged. The first Ctrl-C goes to
  the tool so it can clean up; a second one forces it to stop.

The three tools are not pinned in this release until their repositories are public
(see docs/sibling-repos.md); until then they run from `L80_VAST`, `L80_PWDET`,
`L80_DASHBOARD` or PATH.

## 0.5.0

### Pinned tools

- **Every Python tool L80 runs is now pinned to a commit** in an embedded manifest
  (`internal/pytool/manifest.json`). BetterBench is pinned to v0.6.1. Previously
  `uvx` bootstrapped whatever BetterBench's default branch held that day.
- A `betterbench` on PATH is used only if it is at least the pinned version; an older
  copy is skipped in favour of the pinned bootstrap, with a warning.
  `L80_BETTERBENCH` still overrides, unchecked.
- New `L80 tools list | check | prefetch | path`. `L80 doctor` lists the tools too.
- New exit code 11, `E_TOOL_UNAVAILABLE`, when a tool cannot be run here.
- `make bump-tools` and `make check-manifest` (see docs/tools.md). Releases now run the
  tests and validate every pin before building.

### BetterBench 0.6.1 alignment

- `--api-key` (or `L80_BETTERBENCH_API_KEY`) for endpoints behind a gateway. The key
  reaches BetterBench by environment, never on a command line; a key after `--` is
  refused. The echoed command is redacted.
- BetterBench's own update check is switched off for runs L80 starts.
- `cached_tokens` is read from 0.6.1 files. A run that asked for a cold cache but was
  partly served from the server's prefix cache gets a warning before publishing and a
  caveat in the `bench.report.v1` mapping.
- A warning when a results file comes from a BetterBench older than 0.5.0 or newer than
  the pinned version.
- `--out` naming an existing directory is refused instead of failing after the run.

### Other

- `E_SERVER` now exits 8 (server error), as documented, instead of 1.
- The local ledger records, per publish, the sha256 of the source file and of the
  bytes sent, the server's content hash and the trust tier. This is the provenance a
  future verification request will use.
