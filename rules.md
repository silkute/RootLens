# RootLens system usage guide

RootLens is a read-only Linux diagnostics system. The hub owns authentication,
history and incident evaluation; agents own host collection. Nothing received
from the hub may execute commands or increase agent privileges.

## Telemetry contract (agent to hub)

`POST /api/v1/agent/batches` accepts protocol v1 JSON over verified HTTPS with a
host-bound bearer credential. Callers supply boot/epoch identity, increasing
sequence, entity metadata, timestamped frames, interval deltas, collector health
and optional events. Maximum request: 2 MiB; maximum frames: 120. A successful
acknowledgement means SQLite committed the batch. Retry identical batches on
transport failure; never reuse a sequence for different content. Missing metrics
are absent, never zero. Counter resets invalidate the first interval.

Example: a host frame contains `cpu.busy_pct` and `io.some_pct`; a container frame
contains `cpu.cores` and `memory.used_bytes`. Metric keys and units are defined by
the shared protocol registry. User-controlled labels must not create metric keys.

The authoritative types live in `internal/protocol`; `go run ./cmd/contracts`
generates `api/openapi.json`, the metric registry and TypeScript interfaces. Do not
edit generated interfaces directly. `Batch.Validate(now)` returns a bounded error
code for incompatible protocol, identities, timestamps, dimensions or values.
Collector callers must validate before buffering; server callers validate again
after binding the authenticated host. API delta metrics are returned as rates.

## Diagnostics (hub only)

Rules consume persisted frames through the shared diagnostic engine. Evidence
distinguishes measured facts, supported inferences, possible contributors and
insufficient evidence. A missing signal interrupts an incident; it cannot resolve
one. Live ingestion and historical investigations use the same evaluator. Changes
to rule meaning require a new rule version. Never infer causation from activity
alone or equate device busy time with saturation.

`Evaluate(state, hostID, frame, entityKinds, historical)` returns incident updates.
`ObserveEvents` applies lifecycle evidence; `Tick` handles observed recovery and
missing data. Persist the returned updates with state atomically. For historical
requests use `Store.Investigate(ctx, hostID, from, to)`: it returns findings without
mutating live reports or notifications, and fails explicitly when its budget is
exceeded. Native-resolution history is required; the initial maximum range is six
hours. Retained report evidence remains available after raw history expires.

## Browser API (administrator only)

REST endpoints use an opaque HttpOnly session cookie. Mutating requests must have
the configured same origin and the session CSRF token in `X-CSRF-Token`. Query
responses include measurement timestamps, coverage and resolution. Components
retain stale data visibly and show unsupported capabilities separately from zero.
No credential belongs in localStorage, a URL, or a log. Browser text uses sentence
case and normal letter spacing.

Use `api<T>(path, options)` and `mutate<T>(path, method, data)` in the web app. These
helpers attach the session CSRF token and bound request duration. Handle `APIError`
codes without clearing last-good measurements. Absolute `from`/`to` query values in
the hash route preserve incident intervals while navigating host views.

## Storage (hub only)

All mutations run through the serialized SQLite writer. Ingestion, incident state
and evaluation cursor commit atomically. Reads have bounded time ranges and point
counts. Counter totals and gauge weighted sums are rolled up using covered time;
derived latency uses operation-weighted totals. Backups use SQLite facilities,
never copy the live main file alone. Database volumes must be local filesystems.

## Collector use (agent only)

Collectors report availability and errors independently. Paths are local agent
configuration, not remote instructions. Docker data is allowlisted before it
leaves the agent. Container identity is the complete Docker ID; each restart has
a new counter generation. No privileged container or automatic capability grants.

Optional `collect_processes` snapshots contain at most twenty current PID/name/CPU/RSS
entries. Send them only through the authenticated batch contract. The hub updates an
in-memory latest snapshot after successful ingestion; process lists must never enter
the metric, event, metadata-history or report tables. Never collect argv or environments.

## Applicable review checklist

- Verify public contract compatibility and update the relevant section in place.
- Verify permissions, identity, input bounds and secret redaction.
- Verify missing data, counter resets and incident recovery semantics.
- Run affected parser, diagnostic, persistence, API and UI tests.
- Distinguish fixture/build evidence from Linux, device and release qualification.
- Preserve accessible sentence-case UI with no extra letter spacing.
