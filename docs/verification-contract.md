# "Verified by Launch80": server contract

Status: **proposed**. The L80 CLI (1.0.0 and later) implements the client side
described here. The launch80 server implements the rest. Until it does, the
CLI's verification commands report "this launch80 server does not accept
verification requests yet" (exit 8) and `L80 verify check` still works
locally.

## What verification means

A published `bench.betterbench.v1` page is **self-reported**: anyone with a
key can publish any numbers. A **verified** page is one a Launch80 verifier
has stamped: the run is complete and cold, the setup is described well enough
to reproduce, and, where the verifier chose to, they reproduced it themselves.

The workflow has three roles, all ordinary publish keys:

1. **Publisher**: the key that published the page asks for verification.
2. **Any other key**: sees the request in its queue (`L80 verify queue`, the
   console's Verify screen), can open it and reproduce the run on its own
   hardware. This is open to every valid key so the community can check runs.
3. **Verifier**: a key with `publisher_keys.is_verifier = true` stamps the
   page verified or rejects it. Reproducing first is optional; when the
   verifier did, their reproduction goes with the decision as evidence. A key
   never sees its own requests in the queue and may never decide on them.

- Only `bench.betterbench.v1` pages are eligible in this version.
- **Pages stay immutable.** Verification changes the page's *tier*
  (metadata the template renders as a badge), never the stored payload. A run
  that needs fixing is published again and the new page is verified.
- Verification is requested by the key that published the page and decided by
  a verifier key other than the publisher's.

## Tiers

`artifacts.trust_tier` gains values. Today every page is `unverified`.

| Tier | Meaning | Badge |
|---|---|---|
| `unverified` | Default. | "Self-reported — not verified by launch80" (unchanged) |
| `pending` | A request is open. | "Self-reported — verification requested" |
| `verified` | A verifier stamped it. | "Verified by Launch80 · <reviewed date>", plus "reproduced" when the decision carried a reproduction |
| `rejected` | A verifier declined it. Renders like `unverified`; the reason is shown to the owner only. | as `unverified` |

These map onto the AwesomeList vocabulary when results are exported there:
`verified` → `maintainer_verified`; everything else → `author_reported`.

## Data model

```sql
-- existing table; widen the tier
ALTER TABLE artifacts
  ADD CONSTRAINT artifacts_trust_tier_chk
  CHECK (trust_tier IN ('unverified', 'pending', 'verified', 'rejected'));
ALTER TABLE artifacts ADD COLUMN verified_at timestamptz NULL;

-- only verifiers may stamp; set by Launch80, never by the key holder
ALTER TABLE publisher_keys ADD COLUMN is_verifier boolean NOT NULL DEFAULT false;

CREATE TABLE verification_requests (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  artifact_guid  uuid NOT NULL REFERENCES artifacts(guid),
  key_id         text NOT NULL,              -- the requesting (publishing) key
  status         text NOT NULL DEFAULT 'requested'
                 CHECK (status IN ('requested', 'in_review', 'verified', 'rejected', 'withdrawn')),
  requested_at   timestamptz NOT NULL DEFAULT now(),
  reviewed_at    timestamptz NULL,
  reviewer_id    text NULL,                  -- the verifier key that decided
  note           text NULL,                  -- publisher's note, <= 2000 chars
  decision_note  text NULL,                  -- verifier's note / rejection reason, <= 2000 chars
  reproduction   jsonb NULL,                 -- the verifier's reproduction, when they ran one
  client_checks  jsonb NOT NULL DEFAULT '[]',
  server_checks  jsonb NOT NULL DEFAULT '[]',
  source_sha256  text NULL,                  -- from the publisher's ledger
  body_sha256    text NULL,
  content_hash   text NULL,
  l80_version    text NULL
);
-- at most one open request per page
CREATE UNIQUE INDEX verification_requests_open
  ON verification_requests (artifact_guid) WHERE status IN ('requested', 'in_review');
```

## Endpoints

All responses use the existing envelope `{ "ok": true, "data": … }` /
`{ "ok": false, "error": { "code", "message", "remedy", "details"? } }`, and
`Authorization: Bearer L80_<keyid>_<secret>`.

A `Verification` object:

```json
{
  "id": "uuid", "artifact_guid": "uuid", "url": "https://www.launch80.com/a/…", "title": "…",
  "key_id": "…", "status": "requested", "trust_tier": "pending",
  "requested_at": "RFC 3339", "reviewed_at": "RFC 3339 or omitted",
  "note": "…", "decision_note": "…",
  "client_checks": [{ "name": "cold_cache", "status": "pass", "detail": "…" }],
  "server_checks": [{ "name": "cold_cache", "status": "pass", "detail": "…" }],
  "mine": false,
  "reviewer_id": "…", "reproduction": { … },
  "requester_label": "…", "reviewer_label": "…"
}
```

`requester_label` and `reviewer_label` are the requesting and deciding keys'
labels (`publisher_keys.label`). The CLI and console show them wherever they
name who asked or who reviewed, and fall back to `key_id` / `reviewer_id` only
when a label is absent, so the server may omit those ids for other keys.

`mine` is true when the request was made with the calling key. The CLI uses it
to stop a key reviewing its own run before the server has to refuse.

### `GET /api/v1/keys/me`

Any valid key. `{ "key_id": "…", "label": "…", "is_verifier": false }`. The CLI
and console use it to show whether the key can stamp.

### `POST /api/v1/artifacts/{guid}/verification`

Owner key only. Body (what `L80 verify request` sends):

```json
{
  "note": "vLLM 0.9.3 on 2x R9700, launch command in the README of …",
  "client_checks": [{ "name": "run_class", "status": "pass", "detail": "full run: 20 passes per category after 3 warmup" }],
  "source_sha256": "hex", "body_sha256": "hex", "content_hash": "as returned at publish",
  "l80_version": "1.0.0"
}
```

1. Reject if the page is not `bench.betterbench.v1` (`E_VERIFY_INELIGIBLE`),
   not owned by the key (`E_UNAUTHORIZED`), or already has an open request
   (`E_VERIFY_EXISTS`, HTTP 409).
2. Run the server checks below on the **stored payload**. Store both check
   lists. If a server check fails, still create the request but answer
   `E_VERIFY_INELIGIBLE` (HTTP 422) with the failures in `details`, unless the
   server prefers to queue it for a reviewer anyway; the CLI only sends a
   request with failing client checks when the user passes `--skip-checks`.
3. Set the page's tier to `pending`. Return the `Verification` (HTTP 201).

`body_sha256` lets the server confirm the page is the bytes this machine sent:
it should equal sha256 of the stored body, if the server stores the body
verbatim. Document how `content_hash` is computed so the CLI can compare it
too.

### `GET /api/v1/artifacts/{guid}/verification`

Any valid key. The latest request for the page, or `E_NOT_FOUND`.

### `GET /api/v1/verifications?scope=queue|mine&status=requested`

Any valid key. An array of `Verification`, oldest first.

- `scope=queue`: requests made by **other** keys. Never the caller's own.
- `scope=mine`: the caller's own requests, with their decisions.
- `status` filters (`requested`, `in_review`, `verified`, `rejected`); omitted
  means every status.

### `GET /api/v1/verifications/{id}`

Any valid key. One `Verification`, with `mine` set for the caller.

### `GET /api/v1/artifacts/{guid}/payload`

Public, like the page itself. The body the page was published with, exactly as
stored, as `data`: for a BetterBench page, the results.json a verifier
reproduces. L80 reads the model, notes and settings from it to rerun the
benchmark, and the published headline figures to compare against.

### `POST /api/v1/verifications/{id}/decision`

Verifier keys only (`is_verifier`; otherwise `E_NOT_VERIFIER`, HTTP 403). The
caller may not be the request's key (`E_OWN_REQUEST`, HTTP 403). Body:

```json
{
  "decision": "verified" | "rejected",
  "note": "required for rejected",
  "reproduction": {
    "betterbench_version": "0.6.1", "model": "Qwen3-8B",
    "notes": { "engine": "vLLM 0.9.3", "hardware": "1x RTX 4090", "reproduces": "Qwen3-8B" },
    "runs_per_category": 20, "run_class": "full", "phases": ["decode", "prefill", "concurrency"],
    "metrics": [
      { "label": "Combined decode", "unit": "t/s", "published": 142.4, "reproduced": 139.8,
        "delta_pct": -1.8, "tolerance_pct": 10, "within": true }
    ],
    "within_tolerance": true, "results_sha256": "hex", "ran_at": "RFC 3339", "l80_version": "1.0.0"
  }
}
```

`reproduction` is optional. Store it as sent. Sets the request's status,
`reviewed_at`, `reviewer_id`, `decision_note`, `reproduction`, and the page's
tier (`verified` + `verified_at`, or `rejected`). The request must be open
(`requested` or `in_review`), else `E_VERIFY_CLOSED` (HTTP 409).

Tolerances the CLI applies before it lets a verifier stamp a reproduction that
disagrees (it then needs `--accept-differences` and a note): combined decode
±10%, ITL 1% low ±15%, TTFT p50 ±25%, peak aggregate ±15%, prefill ±20%.

**Quick runs (L80 1.1).** A quick run (see Run classes) is stamped only with a
reproduction that is within its own, wider tolerances: combined decode ±15%,
ITL ±25%, TTFT p50 ±40%, peak aggregate ±25%, prefill ±30%, everything else
±25%. Tail figures (p95, p99, 1% lows) are left out of the comparison, since a
few passes cannot hold them. `--accept-differences` does not apply. The server
refuses `verified` on a quick run unless `reproduction.within_tolerance` is
true (`E_REPRODUCTION_REQUIRED`, HTTP 422).

### Existing endpoints

- `GET /api/v1/artifacts` (history) already returns `trust_tier`; add
  `verification: { status, requested_at, reviewed_at }` when a request exists.
- `POST /api/v1/artifacts` keeps returning `trust_tier: "unverified"`.
- `GET /api/v1/health` adds `"verification": true` and
  `"betterbench_max_supported": "0.6.1"`, so the CLI can detect support
  instead of inferring it from a failed call.

### Error codes

| Code | HTTP | CLI exit |
|---|---|---|
| `E_VERIFY_EXISTS` | 409 | 4 |
| `E_VERIFY_INELIGIBLE` | 422 | 4 |
| `E_VERIFY_CLOSED` | 409 | 4 |
| `E_NOT_VERIFIER` | 403 | 5 |
| `E_OWN_REQUEST` | 403 | 5 |

`E_OUTSIDE_TOLERANCE` (exit 4) is client-side only.

## Run classes

A BetterBench page's class is derived from its stored `config`, never taken
from the client (`betterbench.RunClass` in the CLI):

| Class | Rule | Verifiable |
|---|---|---|
| `full` | `runs_per_category >= 20` and `warmup >= 1` | yes; a reproduction is optional |
| `quick` | `5 <= runs_per_category < 20` and `warmup >= 1` (BetterBench `--quick` is 5 passes after 1 warmup) | yes, only with a reproduction within quick tolerances; medians only |
| `smoke` | fewer than 5 passes, or no warmup | no |

## Server checks

The same checks the CLI runs (`internal/betterbench/precheck.go`), recomputed
from the stored payload, so a modified client cannot talk its way past them.
A `fail` makes the page ineligible; a `warn` is shown to the reviewer.

| Check | Fails when | Warns when |
|---|---|---|
| `run_class` (was `full_run` before 1.1) | the class is `smoke` (Run classes) | |
| `phases` | `single_stream`, `prefill` or `concurrency` is empty | |
| `cold_cache` | `config.unique_nonce` is false, or any `cached_tokens` entry > 0 (single-stream runs, prefill rows, concurrency rows), the count `report.cache_hits()` computes | no request reported `cached_tokens` |
| `requests` | more than 2% of single-stream requests failed | any single-stream or concurrency request failed |
| `truncation` | | any run's `finish_reason` is `length` |
| `version` | `betterbench_version` missing or older than 0.6.1 (the first version that records `cached_tokens`) | newer than `betterbench_max_supported` |
| `notes` | `env.notes.engine` missing, or no hardware: neither `env.notes.hardware` nor `env.gpu` content | |
| `sample_gate` | | `sample_gate.under_sampled` is non-empty (normal: a TTFT p99 from 20 passes is always under-sampled) |

Client-only check: `provenance` (the local results file still hashes to what
was published). The server cannot see the file; it compares `body_sha256`
instead.

## What a verifier looks at

The server checks get a run to the queue; a verifier decides. Suggested
review: the page's numbers are plausible for the stated hardware and engine;
the engine version and launch flags are stated (note, or a linked recipe);
there is no sign of a warm cache or batching games. For headline claims, the
verifier reproduces the run with `L80 verify reproduce` (or the console's `p`)
on matching hardware, and the comparison is attached to the stamp.

## Rendering changes in `bench.betterbench.v1`

Some of these are needed regardless of verification, because BetterBench 0.6.x
already writes them:

1. **Badge** per tier, as above.
2. **Cache-hit chip**: when any `cached_tokens` > 0 on a run with
   `unique_nonce`, show "⚠ N from server prefix cache — not cold", as
   BetterBench's own HTML report does. The schema already accepts the field.
3. **Phases**: draw only the phases present; a partial run says which it holds.
4. **Concurrency levels**: draw the levels the run contains. 0.6.0's default
   is `[1, 2, 4, 8]`, not `[…, 16]`.
5. **PP t/s** is no longer a single-stream column (0.5.0); keep reading
   `pp_tps` for older files.

## Server work for L80 1.0 (checklist)

Everything the launch80 API server, database and website need before L80 1.0
is announced. The sections above give the detail for each item.

**Database**
- [ ] `publisher_keys.is_verifier boolean not null default false`; set it to
      true on the keys you trust.
- [ ] `artifacts.trust_tier` accepts `unverified | pending | verified | rejected`;
      add `artifacts.verified_at`.
- [ ] Store each artifact's body verbatim, so `GET …/payload` returns the bytes
      that were published and `content_hash` can be recomputed.
- [ ] New `verification_requests` table (see Data model), with a
      `reproduction jsonb` column for the verifier's figures.

**New endpoints**
- [ ] `GET /api/v1/keys/me` → `{key_id, label, is_verifier}`
- [ ] `POST /api/v1/artifacts/{guid}/verification` (owner key only) and
      `GET /api/v1/artifacts/{guid}/verification`
- [ ] `GET /api/v1/verifications?scope=queue|mine&status=`. The queue holds
      requests from *other* keys, visible to any valid key.
- [ ] `GET /api/v1/verifications/{id}`
- [ ] `GET /api/v1/artifacts/{guid}/payload`
- [ ] `POST /api/v1/verifications/{id}/decision {decision, note, reproduction?}`:
      verifier keys only (`E_NOT_VERIFIER`), never on your own request
      (`E_OWN_REQUEST`), a note is required to reject, and `reproduction` is
      optional because a verifier may approve on review alone.
- [ ] Error codes as in the table above.

**Existing endpoints**
- [ ] `GET /api/v1/artifacts` (history) returns `trust_tier`, `title` and the
      `verification` summary.
- [ ] `GET /api/v1/health` adds `verification: true` and
      `betterbench_max_supported: "0.6.1"`.
- [ ] Unknown `/api/…` routes return a JSON `404` envelope, not a `307`
      redirect to the website.

**Checks and safety**
- [ ] Recompute the client pre-checks from the stored body (Server checks);
      never trust `client_checks` alone.
- [ ] Queue privacy: show key *labels* to other users, never `key_id`s or
      token material.
- [ ] Audit log of every request and decision; rate-limit requests and
      decisions per key.

**Rendering (`bench.betterbench.v1` pages)**
- [ ] Tier badges, including "reproduced" when the approval carried a
      reproduction.
- [ ] Cache-hit chip, phases present only, the run's own concurrency levels,
      `pp_tps` for older files (Rendering changes above).

**Website**
- [ ] `https://www.launch80.com/install.sh` downloads releases from
      `launch80/L80-CLI` (the repo was renamed from L80-Skills).

## Server work for L80 1.1: quick runs (checklist)

- [ ] `run_class` column on `verification_requests` and `artifacts`, computed
      from the stored payload with the Run classes rule; never from the client.
- [ ] Server check `run_class` replaces `full_run`: fail only for `smoke`.
      Accept both names in stored `client_checks`.
- [ ] Decision: `verified` on a `quick` request requires a `reproduction` with
      `within_tolerance: true`, else `E_REPRODUCTION_REQUIRED` (HTTP 422).
      Store `reproduction.run_class` as sent.
- [ ] Return `run_class` in `GET /api/v1/verifications`, `…/verifications/{id}`,
      `…/artifacts/{guid}/verification` and the history list.
- [ ] Badge in `bench.betterbench.v1`: "Verified by Launch80 · quick run
      (N passes)", always linking to the reproduction; a full run keeps the
      plain badge. The page hides p95/p99 and 1% low cells on quick runs, or
      marks them indicative.
- [ ] Older CLIs (1.0.x) refuse quick runs locally; they still get the
      server's decision, so nothing else changes for them.

## Client behaviour (implemented)

```sh
L80 verify check results.json      # local pre-checks; exit 0 verifiable, 4 not
L80 verify check <url | guid>      # same, plus provenance via the local ledger
L80 verify request <url | guid> [--note "how to reproduce"] [--skip-checks]
L80 verify status <url | guid>
L80 verify mine                                    # your requests and their decisions
L80 verify queue [--status requested|…|all]        # other keys' requests (any key)
L80 verify show <id>                               # what to reproduce, published figures
L80 verify reproduce <id> --endpoint <url> [--model m] [--api-key k]
L80 verify approve <id> [--note …] [--accept-differences]   # verifier keys; quick runs need a reproduction
L80 verify reject <id> --reason …                            # verifier keys
L80 history [--tier verified]      # TIER column
L80 verify queue | mine             # CLASS column (full | quick), from the server
```

The ledger (`~/.config/L80/history.jsonl`) records, for every publish since
0.5.0, `source_sha256`, `body_sha256`, `content_hash` and `trust_tier`. Pages
published by older versions can still be requested; their provenance check is
a warning.
