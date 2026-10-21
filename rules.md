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

## Release packaging (maintainers and installers)

`sh deploy/package-install.sh vVERSION OUTPUT_DIRECTORY` creates ZIP, tar.gz and
standalone `install.sh` artifacts, returning a nonzero exit status on invalid input
or failure. Both copies of the installer are pinned to the supplied version.
The version must be a container-tag-safe string beginning with `v` and a digit.
Callers provide `zip` and `tar`, then publish the bundles with the matching hub and
agent image tags. The release workflow builds Linux AMD64/ARM64 images, signs their
digests and publishes checksummed bundles only after both images succeed. New GHCR
packages must be made public in package settings before advertising anonymous pulls.

Installers use the bundle's `compose.yml`, or `deploy/compose.install.yml`, to pull
version-pinned images without build tools. `ROOTLENS_BIND_IP` defaults to loopback;
LAN callers must set the actual interface IP and use the same HTTPS origin and
certificate names during initialization. `ROOTLENS_PORT` defaults to 8443 and must
match the public origin's port. Both Compose variants also use
`ROOTLENS_INTERNAL_PORT` (default 8443) for their container listener and target;
it may differ from the public port. Native serving uses `--listen` or the fallback
`ROOTLENS_LISTEN`; the systemd example reads `/etc/rootlens/hub.env`. Keep `agent/`, the proxy policy and named
volumes when upgrading. The source-development Compose file remains independent.

`sh install.sh [--dir PATH] [--address IPV4] [--port PORT] [--non-interactive] [--docker] [--offline BUNDLE]`
is the Linux/root installer. Docker Engine, Compose and the listed shell utilities
must already exist. It uses a local Unix socket, checks the pinned release's bundle
hash, initializes TLS and enrolls one local agent before returning success. Exit
status is nonzero on failure; rerun with the same directory to resume. It preserves
existing identities and service versions, refuses unrelated installations or
address/profile conflicts, and does not perform upgrades. With a terminal it asks
for the HTTPS port, including when stdin is a pipe. `--port` overrides the prompt;
`--non-interactive` uses supplied/default/saved values and never displays tokens.
Port values must be decimal 1–65535, without leading zeroes. A changed port is
probed before services stop, then saved in `.env`, hub/agent endpoints and the
installer marker. `.port-change` records an unfinished change; retry that same
port to finish it. Preview.2 service images stay pinned when the current helper
reconfigures their port. Offline callers stage SHA256SUMS beside the bundle
and preload every required image. Docker monitoring requires explicit `--docker`.
Older deployments also need the current helper hub image for offline port changes.

`rootlens set-port --data HUB_DIR [--output AGENT_DIR] --port PORT` updates only the
saved HTTPS origin and optional local agent's matching hub URL. Stop both services
first; callers must also update their listener/Compose public binding and any
remote agents' local configuration. It returns the new origin or an error and
preserves CA, secrets, identities, database and unrelated config fields. Retry the
same port after interruption. `NormalizeHTTPSOrigin(value)` validates an HTTPS
origin and omits default port 443 to match browser serialization; initialization
and serving use this same normalization. Certificates name hosts, not ports.

`rootlens local-agent --data HUB_DIR --output AGENT_DIR [--host-name NAME] [--docker]`
prepares the installer's container configuration and ten-minute enrollment token.
This local-only CLI requires access to the hub's bootstrap secret, a stopped hub
and agent, and a private output directory writable by UID 10001. It refuses new
enrollment after administrator setup and preserves an already-enrolled agent.
Agents still generate/persist their own credentials and enroll through the existing
verified HTTPS endpoint. No new remote endpoint or agent privilege is introduced.
`--check` returns success only for an active credential with recent committed
telemetry; it may run while the hub serves. After stopping the agent, `--finish`
removes the consumed enrollment token and its configuration field. Checks/finish
require the same private config mount, but no bootstrap secret. Neither mode emits
credentials. Use Settings for additional hosts or revoked-agent replacements.

## Applicable review checklist

- Verify public contract compatibility and update the relevant section in place.
- Verify permissions, identity, input bounds and secret redaction.
- Verify missing data, counter resets and incident recovery semantics.
- Run affected parser, diagnostic, persistence, API and UI tests.
- Distinguish fixture/build evidence from Linux, device and release qualification.
- Preserve accessible sentence-case UI with no extra letter spacing.
