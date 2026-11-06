# Changelog

## 1.3.3 (2026-11-06)

### QualityBench results land in one folder

- `L80 qualitybench` and the console's Bench Quality screen now write the
  results file, the HTML report and the `.qb/` folder to
  `~/.local/state/L80/qualitybench/` (`$XDG_STATE_HOME/L80/qualitybench`),
  whichever directory you run from. `L80_QUALITYBENCH_DIR` changes the folder;
  `--out` still sets one run's path. Before, they went to the current
  directory, so the console only found a run when started where it ran.

### Bench - History: Published and Unpublished tabs

- Bench - History has two tabs, switched with `tab` or `←→`, each with its
  count. **Published** is the pages this key published (`v` requests
  verification). **Unpublished** is the QualityBench reports not published yet,
  from the results folder, rental runs and the current directory (for runs
  before 1.3.3): `p` publishes one, `o` opens its report, enter shows its
  scores. A report moves to Published once it is.

## 1.3.2 (2026-11-06)

### Console: screens renamed

- Benchmark is now **Bench Speed**, QualityBench **Bench Quality**, Poison
  Detector **Bench Risk**, History **Bench - History**, Verify **Bench
  Verify**, and Rent (vast) **Rent GPUs**. The sidebar is still Home then A to
  Z, so the Bench screens sit together and the `1`-`9` jumps move.
- The CLI commands are unchanged.

### Bench - History: publish QualityBench reports; the Jobs screen is gone

- **Bench - History** now lists the QualityBench reports on this machine that are not
  published yet (the current directory and rental step folders, each with its
  `.qb` folder), above the pages you published. They are read from disk, so a
  finished run is still there after the console restarts. `p` publishes one
  (it asks first), `o` opens its HTML report, enter shows its scores.
- The **Jobs** screen is removed: its list lived only in memory and was empty
  after a restart. Home still shows this session's runs, and each run's own
  screen keeps its log and its stop key.
- A task-only run (BFCL only, say) now publishes: launch80 accepts empty
  `tasks` or `logprobs`, and the page leaves out the empty sections.
  QualityBench 0.2.0 still ends such a run with "launch80 will not accept
  this file yet"; ignore it. The note goes with the next QualityBench pin.

## 1.3.1 (2026-11-06)

- Pins QualityBench 0.2.0 (`329daf2`) and bbvastl80 0.2.2 (`ec58eab`).

### Rentals: no more false "may still be billing"

- The Rent screen, `L80 vast status --json` and the end of `L80 vast run`
  check runs whose state file still says "rented" against Vast's own instance
  list (`bbvastl80 list --json`, bbvastl80 0.2.2). A run whose box is no
  longer on the account shows as **gone** and is not counted as billing. Before,
  boxes destroyed on the Vast website, or by a crash before teardown, were
  reported as billing forever. If Vast can't be asked (no key, no network, an
  older bbvastl80), the warning stays and says why it is unsure.

### QualityBench: how long, then a yes

- **The estimate comes first.** `L80 qualitybench` asks QualityBench 0.2.0
  (`qualitybench estimate`) how long the run will take, measured on the server
  you point it at, and prints the total, a range and each task's minutes. With
  an older QualityBench it shows the reference-run table, labelled as such.
- **Then it asks** on a terminal (`Start? [y/N]`), per the new
  `qualitybench.confirm` setting: `always` (default), `over:<duration>` to ask
  only for longer runs, or `never`. `--yes` skips it once;
  `L80_QUALITYBENCH_CONFIRM` overrides; without a terminal L80 prints the
  estimate and starts, as before. `--estimate-only [--json]` prints the
  estimate and runs nothing, for agents to quote before they run.
- **`L80 settings list|get|set|unset`** reads and writes
  `~/.config/L80/settings.json`.
- **Progress with time left.** On a terminal the CLI keeps one status line
  (`48% · 23 min left · IFEval 31/55`). The console adds an overall bar
  weighted by each task's estimated minutes, the time left (QualityBench's own
  figure from 0.2.0, else worked out from the run so far), per-task time left,
  and a warning naming the tasks a deadline will cut.
- **Console:** Run shows "Checking how long this will take…", then Start /
  Start, don't ask again / Cancel. Settings cycles the question. The Scope row
  shows the tool's estimate for the picked server without sending it requests.
  Job titles on Home and Jobs carry the live percentage and time left.
- **Rentals:** the consent dialog lists each step's time; the run view shows the
  QualityBench step's overall bar and time left against the watchdog's
  deadline. A rental never asks a second time once it is billing.

## 1.3.0

### QualityBench

- **`L80 qualitybench`** runs [QualityBench](https://github.com/launch80/QualityBench)
  against an OpenAI-compatible endpoint, writes results and an HTML report, and
  publishes to `bench.qualitybench.v1`. `--quick` and `--tasks a,b` choose the
  scope; `--thinking on|off`, `--reference` and `--thresholds` set up a
  quality-loss comparison; `--deadline` caps the time. With no `--endpoint`, L80
  finds the local server serving `--model`. Rerunning the same command resumes.
- QualityBench is released and pinned: v0.1.1 (`e1664cf`). It is no longer
  listed as unreleased; `L80_QUALITYBENCH` still runs a local build.
- `--tokenizer org/repo` names the tokenizer for logprobs and needle. It
  defaults to the weights the local vLLM server reports (`root`).
- `--sandbox docker|native` chooses where the code tasks run. `native` runs
  model code on this machine: only use it on a disposable box.
- `L80 qualitybench setup [--tasks a,b] [--sandbox …]` installs the harness
  environments, the LiveCodeBench image and the datasets ahead of a run.
- If a task fails, the rest are kept and published (unless `--no-publish`),
  the failed task is listed under `partials`, and L80 exits 1.
- `--reference` and `compare` need each results file's `.qb/` folder beside it,
  and say so when it is missing.
- `L80 qualitybench endpoints` lists the inference servers on this machine
  (vLLM, llama.cpp, llama-swap, SGLang, Ollama, LM Studio), their versions and
  models, and whether each llama-swap model is loaded. `tasks` lists the
  suite with item counts and measured run times. `compare` runs locally.
- `L80 publish --template bench.qualitybench.v1` strips `env.endpoint` and
  `env.host`, as for BetterBench files, and refuses home-directory paths and
  LAN URLs anywhere in the file.

### Rentals: steps

- bbvastl80 is pinned at v0.2.1 (`ad155d7`). A box that never starts is
  destroyed and the next offer is tried, and specs can set `gpu.min_inet_down`. A spec's `steps:` lists operations
  to run in order on one rental: `betterbench` and `qualitybench`. They run on
  this machine through the tunnel, each into `runs/<id>/<NN>-<label>/`. A
  failed step does not stop the next one; the run exits 1 if any failed.
  Progress comes as `bbvastl80:step` lines.
- vLLM gets `--max-logprobs 64` when a step scores logprobs. New spec fields
  `tool_call_parser` and `reasoning_parser`.
- `L80 vast spec --steps betterbench,qualitybench,qualitybench-thinking` writes
  such a spec, with `--qb-tasks`, `--reference`, `--thresholds`,
  `--qb-deadline`, `--tool-parser`, `--reasoning-parser` and `--no-publish`.
  QualityBench steps run with thinking off unless they are
  `qualitybench-thinking`.
- The `--max-hours` watchdog passes `BBVASTL80_DEADLINE`, so QualityBench stops
  starting work 15 minutes before the cap and its results are written.

### Console

- **QualityBench screen.** Run or Compare (two local results). Scope is Full,
  Quick or a Custom checklist with a live run-time estimate. Pick a server
  found on this machine and one of its models, a "Compare to" reference from
  local results, and a tokenizer. Thinking defaults to off. Setup runs before
  each run. A run shows per-task progress, then the scores, publish (`p`) and
  open report (`o`). "Rent via Vast" hands the model and a QualityBench step
  to Rent.
- **Rent (vast)** has three tabs: Runs, Spec file and New rental. New rental is
  a wizard: Model, Hardware, Operations, Review. Presets are Speed + quality,
  Quality baseline, Speed only, Compare to a run and Custom. Review shows a
  time plan per step and the cost cap (max hours × the $/hr ceiling); the
  budget defaults to the cap. One consent dialog, then a live step list with
  per-task QualityBench progress; `o` opens a step's report.
- **Home is a launcher.** "What do you want to do?" opens benchmark speed,
  measure quality, compare runs, rent a GPU, scan weights, publish, verify or
  settings. Status and the latest jobs sit below it.
- New screens: **Settings** (log in with a masked input, log out, check for an
  update with its release notes and run it), **Skills** (install or link per
  agent), **Templates**, **Doctor** and **Jobs** (every run, its log, and stop).
- Screens come from a registry. The sidebar scrolls when it is taller than the
  pane, and `1`-`9` jump to the first nine entries.
- `L80 update --check --json` carries the release notes (`notes`).

### Skills

- New skill `l80-qualitybench`: run, compare and publish QualityBench.
- `l80` covers QualityBench; `l80-vast` covers steps and the New rental wizard.

## 1.2.2

- The console hands the terminal to the dashboard and `vast ssh` again. On
  builds with recent Go the terminal was opened in a mode that cannot pause
  its input, so choosing the terminal dashboard (or an ssh session) said
  "This terminal cannot be handed to another program from the console". The
  console now opens the terminal read-only, which restores the handoff: the
  child runs full-screen, and `q` in the dashboard comes back to the console.

## 1.2.1

- The console's About story is reworded.

## 1.2.0

- **pwdet scans can be verified.** When the launch80 server lists
  `scan.pwdet.v1`, `L80 pwdet scan --publish` and `L80 pwdet publish` send the
  page with the scrubbed pwdet report (no host, paths, endpoints or evidence
  data) so it can be checked and re-run; otherwise they send
  `generic.report.v1` as before and say why that page is not verifiable.
- `L80 verify check|request` accept a pwdet report.json or a `scan.pwdet.v1`
  page. A scan qualifies at Standard or above, of a Hugging Face repo pinned
  to a commit, from pwdet 0.2 or later (which records its inputs and a digest).
- `L80 verify show|reproduce|approve` handle scans: `reproduce` needs no
  endpoint; it runs `pwdet reproduce` on the published report (`--model` and
  `--base` point at local copies). A scan is stamped only against a re-run
  within tolerance: same file hashes, deterministic findings matching the
  digest, the same significant probe findings. `--accept-differences` does not
  apply. The console's Verify screen does the same with `p` (re-scan), and
  its lists gain a Type column (betterbench or pwdet).
- **Assurance levels.** pwdet 0.2 reports say what a scan could see: Basic,
  Standard, Thorough or Deep, with each threat tested or not, the gaps and
  how to close them, and the measured accuracy for that level. Pages show
  the verdict with its level ("clean · standard") and gain Threat coverage,
  Measured accuracy and Not tested sections; the Method text describes the
  level that ran. Reports from pwdet 0.1 (schema 1) still parse.
- **Console Poison Detector:** the form offers the levels (Standard first),
  Base and Endpoint; the result shows the level badge, a one-line threat
  matrix and the accuracy; `m` opens a Method tab (how pwdet works, what this
  scan tested, gaps, accuracy, strengths and limits, the probe power line). A
  multi-line evidence summary no longer breaks the frame.

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
- **`L80 pwdet publish <report.json|folder>`** publishes a report from an
  earlier scan as the same generic.report.v1 page `scan --publish` makes,
  without running pwdet again. A reports folder publishes its newest scan.
  Takes `--dry-run`, `--payload-out`, `--title`, `--summary` and `--json`.
- **The console's Poison Detector shows the whole report.** After a scan:
  the verdict with the model revision and pwdet version, a coverage table
  (run, pass, warn, fail, skipped per layer), every check with the ones to
  review first, and the selected check's full observation, evidence and
  remediation. `a` switches to only the checks to review. Before, it listed
  only warnings and failures and pointed at the HTML report.
- The console sidebar no longer lists the deferred Recommendations and
  Train & Tune pages.
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
