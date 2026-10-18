# "Verified by Launch80": server contract

Status: **proposed**. The L80 CLI (1.0.0 and later) implements the client side
described here. The launch80 server implements the rest. Until it does, the
CLI's verification commands report "this launch80 server does not accept
verification requests yet" (exit 8) and `L80 verify check` still works
locally.

## What verification means

A published `bench.betterbench.v1` page is **self-reported**: anyone with a
key can publish any numbers. A **verified** page is one a Launch80 reviewer
has checked: the run is complete and cold, the setup is described well enough
to reproduce, and, where the reviewer chooses, the result has been reproduced.

- Only `bench.betterbench.v1` pages are eligible in this version.
- **Pages stay immutable.** Verification changes the page's *tier*
  (metadata the template renders as a badge), never the stored payload. A run
  that needs fixing is published again and the new page is verified.
- Verification is requested by the key that published the page and decided by
  a key with the reviewer role.

## Tiers

`artifacts.trust_tier` gains values. Today every page is `unverified`.

| Tier | Meaning | Badge |
|---|---|---|
| `unverified` | Default. | "Self-reported — not verified by launch80" (unchanged) |
| `pending` | A request is open. | "Self-reported — verification requested" |
| `verified` | A reviewer approved it. | "Verified by Launch80 · <reviewed date>" |
| `rejected` | A reviewer declined it. Renders like `unverified`; the reason is shown to the owner only. | as `unverified` |

These map onto the AwesomeList vocabulary when results are exported there:
`verified` → `maintainer_verified`; everything else → `author_reported`.

## Data model

```sql
-- existing table; widen the tier
ALTER TABLE artifacts
  ADD CONSTRAINT artifacts_trust_tier_chk
  CHECK (trust_tier IN ('unverified', 'pending', 'verified', 'rejected'));
ALTER TABLE artifacts ADD COLUMN verified_at timestamptz NULL;

-- keys gain a role
ALTER TABLE api_keys ADD COLUMN role text NOT NULL DEFAULT 'publisher'
  CHECK (role IN ('publisher', 'reviewer'));

CREATE TABLE verification_requests (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  artifact_guid  uuid NOT NULL REFERENCES artifacts(guid),
  key_id         text NOT NULL,              -- the requesting (publishing) key
  status         text NOT NULL DEFAULT 'requested'
                 CHECK (status IN ('requested', 'in_review', 'verified', 'rejected', 'withdrawn')),
  requested_at   timestamptz NOT NULL DEFAULT now(),
  reviewed_at    timestamptz NULL,
  reviewer_id    text NULL,                  -- reviewer key id
  note           text NULL,                  -- publisher's note, <= 2000 chars
  decision_note  text NULL,                  -- reviewer's note / rejection reason, <= 2000 chars
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
  "id": "uuid", "artifact_guid": "uuid", "url": "https://launch80.com/a/…", "title": "…",
  "key_id": "…", "status": "requested", "trust_tier": "pending",
  "requested_at": "RFC 3339", "reviewed_at": "RFC 3339 or omitted",
  "note": "…", "decision_note": "…",
  "client_checks": [{ "name": "cold_cache", "status": "pass", "detail": "…" }],
  "server_checks": [{ "name": "cold_cache", "status": "pass", "detail": "…" }]
}
```

### `POST /api/v1/artifacts/{guid}/verification`

Owner key only. Body (what `L80 verify request` sends):

```json
{
  "note": "vLLM 0.9.3 on 2x R9700, launch command in the README of …",
  "client_checks": [{ "name": "full_run", "status": "pass", "detail": "20 passes per category after 3 warmup" }],
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

Owner or reviewer. The latest request for the page, or `E_NOT_FOUND`.

### `GET /api/v1/review/queue?status=requested`

Reviewer role only (`E_FORBIDDEN_ROLE`, HTTP 403, otherwise). An array of
`Verification`, oldest first. `status` may be `requested`, `in_review`,
`verified`, `rejected`; omitted means every status. Opening a request in the
review UI may move it to `in_review`.

### `POST /api/v1/review/{id}/decision`

Reviewer role only. Body `{ "decision": "verified" | "rejected", "note": "…" }`;
a rejection requires a note. Sets the request's status, `reviewed_at`,
`reviewer_id`, `decision_note`, and the page's tier (`verified` +
`verified_at`, or `rejected`). A reviewer may not decide on a page their own
key published.

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
| `E_FORBIDDEN_ROLE` | 403 | 5 |

## Server checks

The same checks the CLI runs (`internal/betterbench/precheck.go`), recomputed
from the stored payload, so a modified client cannot talk its way past them.
A `fail` makes the page ineligible; a `warn` is shown to the reviewer.

| Check | Fails when | Warns when |
|---|---|---|
| `full_run` | `config.runs_per_category < 20` or `config.warmup < 1` (any `--quick` run) | |
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

## What a reviewer looks at

The server checks get a run to the queue; a reviewer decides. Suggested
review: the page's numbers are plausible for the stated hardware and engine;
the engine version and launch flags are stated (note, or a linked recipe);
there is no sign of a warm cache or batching games; for headline claims,
someone reproduces the run (AwesomeList's reproduction-report flow) and the
reproduction is linked in `decision_note`.

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

## Client behaviour (implemented)

```sh
L80 verify check results.json      # local pre-checks; exit 0 verifiable, 4 not
L80 verify check <url | guid>      # same, plus provenance via the local ledger
L80 verify request <url | guid> [--note "how to reproduce"] [--skip-checks]
L80 verify status <url | guid>
L80 review list [--status requested|in_review|verified|rejected|all]   # reviewer keys
L80 review approve <id> [--note …]
L80 review reject <id> --reason …
L80 history [--tier verified]      # TIER column
```

The ledger (`~/.config/L80/history.jsonl`) records, for every publish since
0.5.0, `source_sha256`, `body_sha256`, `content_hash` and `trust_tier`. Pages
published by older versions can still be requested; their provenance check is
a warning.
