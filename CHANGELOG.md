# Changelog

## 1.0.0

First stable release. It ships everything listed under 0.5.0 to 0.8.0 below;
those were development milestones and were never released on their own.

- `L80 vast`, `L80 pwdet` and `L80 dashboard` work out of the box: bbvastl80,
  pwdet and the AI command dashboard are public under github.com/launch80 and
  pinned at v0.1.0 (`L80 tools list`).
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
