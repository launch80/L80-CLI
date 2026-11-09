---
name: l80-test-report
description: Publish a structured report to launch80 as a shareable web page. Use when the user asks to publish, share, or "put on the web" a set of findings, benchmark results, a status update, or any summary made of metrics and sections — or when they mention launch80 or L80, or ask for a link they can send to someone. Do not use for editing an already-published page, or for pages that need custom HTML, images, or links.
---

# Publish a report to launch80

You write a JSON file. The `L80` CLI uploads it. The launch80 server validates it
against a schema and renders the page from a first-party template.

You never write HTML, and you never handle credentials — the CLI does both.

## Steps

1. Pick a template (below). Run `L80 templates list` if you are unsure.
2. Write the report to a JSON file, e.g. `report.json`.
3. Run: `L80 publish report.json --template bench.report.v1`
   (or `--template generic.report.v1`)
4. Give the user the URL from the `published:` line.

Name the template with `--template` so the command says which layout it is
producing. You may instead put `"$template": "<id>"` in the JSON — both work.
If you do both they must agree, or the command fails with
`E_TEMPLATE_CONFLICT` rather than guessing which you meant.

## Which template

**`bench.betterbench.v1` — a BetterBench run or its `results.json`.** Produced
only by `L80 betterbench`, which runs the benchmark and publishes the file as
written. Use it whenever BetterBench is involved.

**`bench.report.v1` — any other per-category percentile benchmark.** Hand-written
summary shape: tables, p50/p99, sample-gate flags.

**`generic.report.v1` — everything else.** Title, summary, a flat metric
list, and prose sections. Use it for status updates, test results, findings, and
any benchmark that is not per-category percentile data.

Choosing `generic.report.v1` for a BetterBench run is the single most common
mistake. It has no table, so p50/p99/1%-low collapse into unrelated flat metrics
and the distribution — the entire point of the measurement — is lost.

## BetterBench runs: `L80 betterbench`

**For anything BetterBench, use `L80 betterbench`. Do not map the file by hand.**

```sh
# Run the benchmark and publish the results file as written (template bench.betterbench.v1):
L80 betterbench --endpoint http://host:8080/v1 --model <name> [--quick] [--note engine=vLLM 0.9.3]
# Publish a results.json that already exists:
L80 betterbench --results results.json
```

What it does: runs `betterbench run` at the version this L80 pins (a new
enough copy on PATH, else bootstrapped with `uvx`, or
`L80_BETTERBENCH=/path/to/betterbench`), streams its output,
keeps `results.json` in the working directory, removes `env.endpoint` and
`env.host` (they never leave the machine), and publishes the file itself. The
server derives every table and figure from the raw samples with a port of
BetterBench's own report code, so the page matches the HTML report the tool
writes beside the file. Add `--title`, `--summary`, or `--section "Heading=Body"`
for prose; add `--dry-run --payload-out p.json` to inspect the exact payload
first. Flags after `--` go to `betterbench run` verbatim.

If the endpoint needs a key, pass `--api-key <key>` before any `--` (or have the
user set `L80_BETTERBENCH_API_KEY`); never put a key after `--`. Repeat any
`warning:` lines L80 prints (older BetterBench version, prefix-cache hits) to
the user before publishing.

For a run the user may want **verified by Launch80** later, run all phases and
record `--note engine=<stack and version>` and
`--note hardware=<GPUs>`. Choose the class with the user: a **full** run (the
default, 20 passes) carries p95/p99 and can be stamped on review; a **quick**
run (`--quick`, 5 passes) is much faster and can be verified only after a
second key reproduces it, on medians. Afterwards `L80 verify check <results.json>` shows
whether it qualifies and `L80 verify request <url> --note "…"` asks for review.
To help verify someone else's run, `L80 verify queue` lists requests and
`L80 verify reproduce <id> --endpoint <url>` reruns it on the user's hardware
and compares. Only stamp (`L80 verify approve`) when the user is a verifier
and explicitly asks you to; never approve or reject on your own judgement.

Before running: ask for the endpoint and model if not given, and tell the user
a full run takes a while (20 passes per category plus sweeps); `--quick` is a
5-pass quick run, verifiable only with a reproduction, and the page says so. After publishing, hand the user the
URL and repeat any caveats the page will show (quick mode, under-sampled
percentiles, truncated runs).

`--template bench.report.v1` instead produces the summary mapping below from
the same file, for when only the summary shape is wanted.

## bench.report.v1

Map a BetterBench `results.json` like this only if `L80 betterbench` cannot be
used. Otherwise this section is the reference for hand-written payloads from
other harnesses with per-category percentiles:

| BetterBench | payload |
|---|---|
| `env.model`, `env.notes` | `system.model`, `system.notes[]` |
| `betterbench_version`, `corpus_version`, `config.runs_per_category` | `harness` |
| weighted combined score | `headline[]` |
| single-stream rows, per category | `categories[]` |
| concurrency sweep | `concurrency[]` |
| prefill sweep | `prefill[]` |
| `sample_gate` shortfalls | `caveats[]`, and `categories[].undersampled: true` |

**Publish the mapped payload, never `results.json` itself.** The raw harness
output is not a template payload: it fails the schema, and it is usually far
over the 64 KB limit, so `E_PAYLOAD_TOO_LARGE` or `E_INPUT_NOT_TEMPLATE` on a
file you did not write is the sign `L80 betterbench` was skipped. (With
`L80 betterbench` or `--template bench.betterbench.v1` the raw file is right.)

```json
{
  "$template": "bench.report.v1",
  "title": "Qwen3-8B on vLLM 0.9.3",
  "subtitle": "Single 4090, mxfp4, 20 measured passes",
  "summary": "One paragraph (required, <= 600 chars).",
  "harness": {
    "name": "BetterBench", "version": "0.2.3",
    "corpus_version": "v1", "passes": "20 passes after 2 warmup"
  },
  "system": {
    "model": "Qwen3-8B",
    "engine": "vLLM 0.9.3",
    "hardware": "RTX 4090 24GB",
    "notes": [{ "label": "quant", "value": "mxfp4" }]
  },
  "headline": [{ "label": "Combined decode", "value": "142.4", "unit": "t/s" }],
  "categories": [
    {
      "name": "code", "runs": "20",
      "ttft_p50": "312", "ttft_p99": "498",
      "decode": "148.2", "decode_iqr": "6.1",
      "itl_low1": "96.1", "itl_med": "151.0", "itl_high99": "203.4",
      "prefill": "4210",
      "undersampled": true
    }
  ],
  "concurrency": [
    { "level": "8", "aggregate": "612.8", "ttft_p50": "918", "ttft_p99": "1840" }
  ],
  "prefill": [{ "depth": "8K", "prefill_tps": "4210", "ttft": "1.94" }],
  "caveats": ["ITL 99% high on `code` rests on 20 runs; treat the tail as indicative."],
  "sections": [{ "heading": "Setup", "body": "Plain text." }]
}
```

Rules specific to this template:

- **Never invent a number BetterBench did not report.** If a field is absent
  from the results file, leave it out — the column disappears. Do not average a
  percentile, and do not fill a gap with a plausible value.
- **A batched / speculative-decoding stack has no per-token ITL.** Use
  `update_p50`, `update_p99` and `tok_per_update` instead. Never convert one to
  the other.
- **Carry the sample gate through.** Any percentile BetterBench marked `†` gets
  `"undersampled": true` on its row plus a line in `caveats`.
- **Say when a run was quick.** If the run used quick mode (fewer than 20
  passes), say so in `caveats` in the first line, and leave out p95/p99.
- There is no `endpoint` field, on purpose. Do not put a LAN URL in `notes`.

## generic.report.v1

```json
{
  "$template": "generic.report.v1",
  "title": "Short page title (required, <= 120 chars)",
  "subtitle": "Optional one-liner (<= 160 chars)",
  "summary": "One paragraph of plain text (required, <= 600 chars)",
  "metrics": [
    { "label": "Decode", "value": "142", "unit": "tok/s", "note": "optional" }
  ],
  "sections": [
    {
      "heading": "What we tested",
      "body": "Plain text. Blank lines start new paragraphs.",
      "bullets": ["optional", "short items"]
    }
  ]
}
```

## Rules for both

- Plain text only. No HTML, no markdown, no URLs, no image references. Unknown or
  unsupported fields are **rejected by the server**, not silently dropped.
- Every displayed number is a **string** — format it yourself ("1,240", "12.4%").
- Limits: 64 KB total. `generic.report.v1` — 12 metrics, 10 sections.
  `bench.report.v1` — 12 categories, 10 concurrency levels, 10 prefill depths,
  6 headline metrics, 8 caveats.
- Published pages are public and carry a visible
  "Self-reported — not verified by launch80" badge. Tell the user this.
- Pages are immutable. To change something, publish again and share the new URL.

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

## If the command fails

Read the `error:` code and the `remedy:` line, then act on them.

- `E_SCHEMA_INVALID` — each listed `/path` is the exact field to fix. Fix and retry.
- `E_TEMPLATE_UNKNOWN` — run `L80 templates list` and use a listed id.
- `E_TEMPLATE_CONFLICT` — `--template` and the file's `$template` disagree.
  Decide which is right, change one, retry. Never publish twice to "see which works".
- `E_TEMPLATE_MISSING` — pass `--template <id>` or add the field.
- `E_TOKEN_MISSING` / `E_UNAUTHORIZED` — tell the user to check `L80 auth status`.
  **Never** ask the user to paste a token into the conversation.
- `E_RATE_LIMITED` / `E_QUOTA_EXCEEDED` — wait for the stated time. Do not retry in a loop.
- `E_INPUT_NOT_TEMPLATE` — the file is raw harness output (e.g. a BetterBench
  `results.json`) given to a summary template. Run `L80 betterbench --results <file>`.
- `E_PAYLOAD_TOO_LARGE` — a valid payload never exceeds the limit by much, so first
  confirm the file is a mapped payload and not harness output. If it is, shorten
  the sections and retry.
- `E_NETWORK` — run `L80 doctor`, then report the failure to the user.
- Any error whose remedy seems wrong for the situation, or a code this list does
  not mention — the CLI may be out of date. Run `L80 update --json`, act on its
  `actions`, then retry once.

To find a page published earlier, run `L80 history` (newest first; `--all`
for everything, `--json` for machine-readable output). Never ask the user to
dig the URL out of scrollback.

Never construct the HTTP request yourself. Always go through `L80 publish`.
