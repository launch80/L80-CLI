---
name: l80-pwdet
description: Check an LLM checkpoint or a served model for tampered or poisoned weights with the Poisoned Weights Detector, via `L80 pwdet`, and optionally publish the report to launch80. Use when the user asks whether a model download is safe, tampered, backdoored or poisoned, wants to vet a Hugging Face repo before deploying it, or asks for a provenance or integrity check of weights.
---

# Scan a model for tampering: `L80 pwdet`

`L80 pwdet` runs the Poisoned Weights Detector pinned in this L80 release. Every
pwdet command works through it. Scanning is free and needs no token; only
`--publish` does.

## Steps

1. Identify the model:
   - a local directory with safetensors or GGUF files,
   - a Hugging Face repo id, `org/name` or `org/name@revision`,
   - a served copy, `http://host:port/v1#model-name` (for behavioural probes).
2. Pick a level with `--profile`. Say what each one can see:
   - `basic`: seconds. Files, headers, configs, tokenizer, chat template, Hub
     provenance. It cannot see weight implants or trained-in behaviour.
   - `standard`: minutes. Also compares every tensor with the base model
     (`--base org/name`, or the one the model card names), fetched from the Hub.
     The usual choice.
   - `thorough`: also probes behaviour against the base. pwdet serves both
     models on this machine's GPU (Intel Arc or NVIDIA, Docker) one at a time,
     minutes per model; or pass `--endpoint http://host:port/v1#model`.
   - `deep`: thorough plus white-box activations (Docker, Intel Arc or NVIDIA).
3. Run it:

   ```sh
   L80 pwdet scan Qwen/Qwen3-0.6B --profile basic --json
   L80 pwdet scan org/derived --base org/original --profile standard --json
   L80 pwdet scan org/derived --base org/original --profile thorough --json
   ```

4. Read the verdict from the JSON, and open `report.html` next to `report.json` for
   the detail. Always give the level with the verdict ("clean at Standard") and
   name the threats the report lists as not tested (`assurance.threats` and
   `assurance.gaps` in report.json). Explain the warn and fail findings.
5. Only if the user wants a shareable page:
   `L80 pwdet scan <model> --publish [--title …] [--summary …]`. Add `--dry-run
   --payload-out p.json` to show them the exact page content first. For a scan
   that already ran, `L80 pwdet publish <report.json|its folder|reports/>` builds
   the same page without scanning again (same flags; a reports folder means its
   newest scan).
6. To have Launch80 verify a published scan: it must be Standard or above, of a
   Hub repo id (not a local folder). `L80 verify check <report.json>` shows
   whether it qualifies; `L80 verify request <page url>` asks. Another key
   re-runs it with `L80 verify reproduce <id>`.

## Verdicts and exit status

| Verdict | Exit | Meaning |
|---|---|---|
| `clean` | 0 | The checks that ran found nothing above low severity |
| `inconclusive` | 0 | Too few checks could run to say anything |
| `suspicious` | 1 | Something worth investigating |
| `likely_tampered`, `malicious` | 2 | Treat the model as unsafe |

Exit 1 also means pwdet itself failed, so read the JSON, not only the exit status.
Exit 11 means L80 could not start pwdet (`L80 tools check pwdet`).

## Rules

- Say plainly that a clean verdict means these checks found nothing, not that the
  model is proven safe. A clean Basic scan says nothing about weight implants or
  trained-in backdoors; suggest Standard or Thorough.
- A published page never carries the hostname, local paths or private endpoints.
  L80 removes them and refuses to publish if any remain.
- Findings the publisher disclosed are downgraded one severity step, never hidden.

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
