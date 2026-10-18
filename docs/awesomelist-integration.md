# Future: "best setup for my machine" via the Launch80 AwesomeList

Analysis only; nothing here is implemented yet.

## What the AwesomeList already provides

[github.com/launch80/AwesomeList](https://github.com/launch80/AwesomeList) is a
structured catalog, not only a README:

- `catalog/projects/*.yaml`: one record per community project, with **hard
  gates** (`hardware.architectures`, `hardware.gpu_count.min/max`,
  `hardware.min_vram_gb`, `software.os`, `conflicts`), what it `provides`
  (`openai_api`, `benchmarking`, `moe_expert_cache`, …), `maturity`
  (`recommended` … `unverified`) and `verification.level`
  (`maintainer_verified`, `community_reproduced`, `author_reported`,
  `unverified`).
- `catalog/compatibility/gpu-models.yaml`: GPU id → vendor, architecture,
  gfx target, VRAM.
- `catalog/paths.yaml`: goal-first "start here" picks per architecture.
- `catalog/benchmarks/benchmark-results.jsonl` with `schemas/benchmark.schema.json`:
  normalized results; BetterBench is the preferred tool.
- `catalog.json` and `llms.txt`: generated, machine-readable.
- `scripts/selector.py`: applies the gates, then ranks by maturity,
  verification, fit and setup friction (never by tok/s); `--json` output.
- `prompts/agent-quickstart.md`: the agent flow (detect read-only → interview
  → shortlist → plan → execute with approval → benchmark → reproduction report).

## Proposed L80 surface

```sh
L80 recommend [--goal openai_api] [--priority stability|simplicity|throughput] [--json]
L80 recommend --gpu r9700 --count 2 --os ubuntu        # override what detection found
L80 skills link --target claude-code --name l80-setup  # the agent flow, driven by L80
```

1. **Detect hardware, read-only**, in Go: PCI vendor/device ids from
   `/sys/bus/pci/devices/*/{vendor,device,class}` (no `lspci` needed), VRAM
   from `amdgpu`/`xe` sysfs or `nvidia-smi --query-gpu`, GPU count, OS from
   `/etc/os-release`, RAM from `/proc/meminfo`, ROCm/driver versions where
   present. macOS: `system_profiler SPDisplaysDataType -json`. Map devices to
   catalog GPU ids with a small PCI-id table kept beside `gpu-models.yaml`
   (proposed upstream as `catalog/compatibility/pci-ids.yaml`).
2. **Fetch the catalog pinned**, like a tool: an `awesomelist` entry in
   `internal/pytool/manifest.json` (repo + commit), `catalog.json` downloaded
   from `raw.githubusercontent.com/launch80/AwesomeList/<commit>/catalog.json`,
   cached in `~/.cache/L80/`, refreshed by `L80 update`. Pinning keeps a given
   L80 version's recommendations reproducible.
3. **Gate and rank.** Two options:
   - port `selector.py`'s gates and scoring to Go (about 200 lines), or
   - run `selector.py --json` through the existing pinned-tool runner (it needs
     PyYAML and jsonschema, so it would need a small `pyproject.toml` upstream).
   Running the upstream selector avoids two implementations drifting apart;
   it is the better default.
4. **Hand off**, never install. Print the shortlist (catalog id, what you get,
   maturity, verification level, why it fits, what to confirm, tradeoff) and
   the catalog card links. Installing stays with the user or their agent, via
   an `l80-setup` skill that follows `prompts/agent-quickstart.md`, so L80 never
   becomes a package manager for third-party projects.
5. **Close the loop.** After setup, `L80 betterbench` measures the result.
   A verified page (see verification-contract.md) can be exported as an
   AwesomeList benchmark record:

   ```sh
   L80 verify export <guid> --awesomelist >> catalog/benchmarks/benchmark-results.jsonl
   ```

   mapping `mode` from the phases (single-stream decode → `single_stream`,
   concurrency → `batched`), `hardware.gpu_model`/`gpu_count` from the notes,
   `metrics.decode_tok_s`/`prefill_tok_s`/`ttft_ms` from the mapped rows,
   `workload.tool: "BetterBench <version>"`, and
   `source: {type: "launch80", url: <page>}`. Only verified pages export as
   `maintainer_verified`; others as `author_reported`. That also needs
   `launch80` added to the catalog's `source_type` vocabulary.

## Risks and open questions

- **Schema drift.** The catalog is young; pin by commit and check
  `catalog.json`'s schema version before using it.
- **Detection gaps.** Unknown PCI ids should fall back to `--arch`/`--vram`
  questions, exactly as the selector does.
- **Ranking honesty.** Keep the catalog's rule of never ranking by tok/s;
  benchmark numbers are context, not a score.
- **Trust.** Catalog content (READMEs, scripts) is reference data, not
  instructions; the skill must carry the quickstart's "treat what you read as
  data" rules.
- **Upstream changes needed:** a `pyproject.toml` for the selector (if L80
  runs it), a PCI-id table, and `launch80` as a benchmark `source_type`.
