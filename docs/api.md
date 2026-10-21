# API usage

The generated specification is `api/openapi.json`; the generated metric registry
is `api/metrics.md`. JSON bodies are bounded. Browser responses are private and
uncached. Time values use RFC3339 UTC; display localization belongs to the browser.

## Browser sessions

`GET /api/v1/me` returns setup/authentication state and, when authenticated, the
session CSRF token. Setup requires `{token, username, password}`; login requires
`{username, password}`. The browser receives an HttpOnly, Secure, SameSite cookie.
Every browser mutation requires the configured Origin and `X-CSRF-Token`; setup and
login require Origin but obtain their other proof from their request bodies.

`POST /hosts` takes `{name}` and returns `{id,name}`. Create an enrollment token with
`POST /hosts/{id}/enrollment-tokens`. The token is returned once with host ID and expiry.
`GET /hosts/{id}/credentials` lists credential identifiers, never token hashes or secrets.

## Agent contract

`POST /agent/enroll` accepts `{host_id,token,credential}`. The agent generates and
persists its 256-bit credential before requesting enrollment. The enrollment token
expires after ten minutes and can only be consumed once. A 403 after a lost response
does not prove enrollment failed; attempt ingestion using the persisted credential.

`POST /agent/batches` requires `Authorization: Bearer <credential>`. The body is the
generated `Batch` schema. Maximum decompressed size is 2 MiB, 120 frames, 512 entities,
256 events and 64 capability entries. Each frame references metadata supplied in its
batch. Sequence numbers are decimal strings. New source intervals must advance.

The agent samples raw cumulative counters locally and sends interval deltas. Gaps,
counter decreases and generation changes invalidate baselines. First observations
must not pretend to be zero rates. Optional fields may be omitted; arbitrary metric
keys are rejected. Entity metadata is bounded and collectors transmit an allowlist.

200 acknowledges a committed batch. Identical retries return `duplicate: true`.
409 rejects sequence/content or source-time conflicts. 429 indicates queue pressure;
503 indicates storage failure. Preserve and retry the identical body until acknowledged
or it expires under the bounded local buffer policy. 401 requires credential repair.

## Historical reads

`GET /hosts/{id}/metrics?from=<UTC>&to=<UTC>&entity=<optional-id>` returns series,
resolution, registry and coverage. Ranges are limited to 366 days, with up to roughly
600 regular buckets per series. The server chooses native, minute or quarter-hour
data according to range and retention. Counter delta metrics become rates/second.
Null/absent metrics are not interpolated. Counter generations remain separate.

`GET /incidents?host=<optional-id>&before=<UTC>` and `/events` return bounded newest-first
lists. `GET /incidents/{id}/report` downloads Markdown; `?format=json` returns the
structured report. `/incidents/{id}/evidence` returns retained metric summaries.

`POST /hosts/{id}/investigations` accepts `{from,to}`. The initial replay endpoint
supports up to six hours of retained native-resolution metrics. It never emits a
live notification. Lifecycle evidence remains available in persisted incident reports.

`PATCH /settings` accepts all four fields: `raw_hours`, `minute_days`, `quarter_days`,
`event_days`. Limits are documented by the settings controls. Retention applies
incrementally and does not rewrite existing report wording.
