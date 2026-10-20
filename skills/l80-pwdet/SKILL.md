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
2. Pick a profile. `quick` (the default) takes seconds; `full-static` reads every
   tensor and takes minutes on a large model. White-box needs Docker and an Intel
   Arc GPU, so do not suggest it otherwise.
3. Run it:

   ```sh
   L80 pwdet scan Qwen/Qwen3-0.6B --json                       # verdict + report path
   L80 pwdet scan ./my-model --profile full-static --json
   L80 pwdet scan org/derived --parent org/original --json     # compare with what it claims to derive from
   ```

4. Read the verdict from the JSON, and open `report.html` next to `report.json` for
   the detail. Explain the warn and fail findings to the user.
5. Only if the user wants a shareable page:
   `L80 pwdet scan <model> --publish [--title …] [--summary …]`. Add `--dry-run
   --payload-out p.json` to show them the exact page content first. For a scan
   that already ran, `L80 pwdet publish <report.json|its folder|reports/>` builds
   the same page without scanning again (same flags; a reports folder means its
   newest scan).

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
  model is proven safe.
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
