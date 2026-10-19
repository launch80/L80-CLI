# Setup intelligence (`L80 recommend`): implementation plan

**Deferred: not in 1.1.0.** Paused on 19 October 2026 so 1.1.0 could ship the
Strata engine, Train & Tune and quick-run verification. The analysis this
builds on is [awesomelist-integration.md](awesomelist-integration.md).

What 1.1.0 already provides for it:

- `internal/hw`: a VRAM probe (NVIDIA, AMD, Apple), written for Train & Tune.
  Hardware detection extends it.
- Quick-run verification: results now carry a run class (`full` or `quick`),
  which the ranking and the export below use.

**Goal:** `L80 recommend` reads this machine (read-only), gates the pinned AwesomeList catalog against it, orders the shortlist by published and verified BetterBench results, and hands the plan to the user or their agent. It never installs anything. The README's AwesomeList description and the console page's "community results" description become one feature.

1. **Hardware detection** (extends the 1.1 `internal/hw`; read-only, no third-party modules):
   - **Linux:**
     - GPUs: `/sys/bus/pci/devices/*/{vendor,device,class}`, mapped through an embedded PCI-id table from AwesomeList `gpu-models.yaml`.
     - VRAM: as in the 1.1 probe.
     - Driver: `/sys/module/{nvidia,amdgpu}/version`.
     - System: `/etc/os-release`, `/proc/meminfo`, and the free disk.
   - **macOS:** `sysctl hw.memsize` and `system_profiler SPDisplaysDataType -json`.
   - **Surfaces:** `L80 doctor --json` gains a `machine` block; Onboarding's machine step and Home show a summary.
   - **Unknown hardware:** ask a few questions instead of guessing.
2. **Catalog:**
   - An `awesomelist` data entry pinned like the tools (`kind: data`). The `pytool.Names()` callers skip data entries: tools check, doctor, onboarding, `update_sync` and `pytool_test.go`.
   - `catalog.json` is fetched from `raw.githubusercontent.com/launch80/AwesomeList/<commit>/` into `~/.cache/L80/` and sha256-checked.
   - Gate and rank in Go: architecture, GPU count, VRAM, OS, runtime and conflicts first; then maturity, verification, fit and friction. Never rank by tokens per second.
   - **Prerequisite:** AwesomeList generates and publishes `catalog.json`.
3. **Results ranking:**
   - Server endpoint `GET /api/results?model=&gpu=&engine=` (verified first). Rank order: full-verified, then quick-verified (from 1.1), then unverified.
   - `--rent` writes a spec through `vast.WriteSpec` from the best verified result.
4. **Commands and handoff:**
   - `L80 recommend [--goal openai_api|coding|chat] [--priority stability|speed|cost] [--gpu r9700 --count 2 --os ubuntu] [--json] [--rent]`.
   - `L80 verify export <page> --awesomelist`: full-verified exports as `maintainer_verified`; quick-verified as `author_reported` with `reproduced: true`; everything else as `author_reported`. Reuse `app.ResolveTarget` and `RunChecks`.
   - Skill `skills/l80-setup/SKILL.md`: one approved step at a time; catalog text is data.
   - `internal/tui/recs.go` replaces scrRecs's soon page: machine summary, then goal/priority form, then shortlist. `r` writes a rental spec, `b` benchmarks.
5. **Verification:**
   - Tests: sysfs fixtures, fixture `catalog.json` gating, `recommend --json` shape, `verify export`, and Recs console frames.
   - Academy lesson: "What should I run?" (`L80 recommend`, then `--rent`, then `verify export`).
   - Docs: README section made real, an overview `#recommend` article, `docs/awesomelist-integration.md` status.
