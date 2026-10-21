# Validation and release qualification

This is a working v0.1 development build, not a production-qualified release.
The following observations were made on 21 October 2026. They must not be treated as
NAS benchmarks, native ARM64 evidence or a substitute for a soak test.

## Executed checks

- Go unit/integration tests on Windows AMD64.
- Go tests in an AMD64 Linux container, including a live proc/PSI collector smoke
  test that collected 39 entities and reported 11 collector capabilities.
- Linux race-detector run across all packages.
- Go vet, frontend type checking, and production Vite build.
- Browser navigation, filtering, host charts, chart tables, diagnostics and reports.
- Overview layouts at 360, 390, 768 and 1440 pixels in dark and light themes.
- Automated accessibility checks with no serious/critical violations across all six main screens in both themes after
  correcting an accessible-name mismatch and light-theme secondary-text contrast.
- Mobile navigation through every main screen without page overflow.
- Sixty Docker proxy allow/deny checks against a deliberately permissive fake Engine,
  including mutating methods, archive/log/exec-style paths, encoding and query cases.
- Real TLS test-server pipeline from proc/PSI fixture collection through authenticated
  ingestion, persisted incident detection and retained evidence.
- Session/CSRF protections, enrollment expiry and replay, cross-host rejection,
  credential revocation, duplicate/conflicting batches and newer-schema refusal.
- Backup/restore, weighted rollup correctness, missing data, generation changes,
  no diagnosis from activity alone, and historical replay without live mutation.
- Linux AMD64/ARM64 and Windows AMD64 compilation; Docker hub and agent image builds. ARM64 binary startup also passed under emulation (not native hardware).

`govulncheck` reported no reachable or imported-package vulnerabilities. It reported
GO-2026-5932 in the required `golang.org/x/crypto` module's unmaintained OpenPGP package.
RootLens imports Argon2, not OpenPGP; the scanner reported the affected package was
not imported or called. There is no listed fixed version for that module advisory.
The npm dependency audit reported zero vulnerabilities at the time of installation.

## Measured storage microbenchmark

Environment: Windows AMD64, AMD Ryzen 9 7950X3D, Go 1.27.3, local storage. Command:

```sh
go test ./internal/storage -run '^$' -bench BenchmarkIngest30Entities -benchtime=100x -benchmem
```

One recorded run of 100 batches, each containing 30 synthetic entities:

| Measurement | Result |
|---|---:|
| Mean ingestion time | 6.36 ms/batch |
| Database size divided by batch count | 15,770 bytes/batch |
| Allocated memory | 951,963 bytes/batch |
| Allocations | 8,087/batch |

This short benchmark includes SQLite persistence and rollup work. It does not establish
steady-state retention size, tail latency, whole-agent overhead or N95 performance.
Allocation volume is not resident memory. The byte figure includes amortized schema,
indexes and rollups; do not multiply it indefinitely as a retention-growth forecast.

## Remaining release gates

1. Test native AMD64 and ARM64 machines, including a real NAS model/OS combination.
2. Validate cgroup/device attribution with real competing Docker workloads on ext4,
   btrfs, RAID/device-mapper and unsupported filesystems. Unknown mapping must not
   produce contributor claims.
3. Run bounded CPU, quota, memory/OOM and disposable-file I/O stress scenarios with
   an independent application-latency probe. No production devices were stressed here.
4. Measure complete agent + proxy + Docker daemon overhead with 0/20/100 containers,
   and hub overhead with 1/10 hosts on N95-class hardware, SSD and HDD storage.
5. Perform a 72-hour soak, abrupt power-loss/disk-full tests and the full retention
   horizon. Verify WAL, metadata, incident evidence and entity churn remain bounded.
6. Exercise actual future-version migrations with pre-migration backup and restore.
   Only the initial schema exists in this release.
7. Review certificate renewal and real reverse-proxy deployments with their own
   authentication-rate policies and private data-directory permissions.
8. Run the GitHub workflows in the chosen repository and verify signed release
   artifacts, multi-architecture manifests, SBOMs and provenance before publishing.

Public source is maintained at https://github.com/silkute/RootLens. No versioned
release has been published. Local checks described here are separate from the
GitHub Actions results shown on the repository's commits.

## Explicit implementation boundaries

- Process summaries are opt-in, bounded, and current-only. Process name/PID/CPU/RSS
  parsing and disabled-by-default behavior have fixture tests; real visibility and
  overhead under hidepid/NAS policies remain part of device qualification.
- Historical ad-hoc replay requires native-resolution data and is limited to six
  hours / 250,000 entity samples / 10,000 events per request. Older saved incident
  reports remain available with retained evidence.
- Container network collection follows the frame cadence rather than a separate
  ten-second schedule to preserve accurate counter denominators.
- cgroups v2 is the diagnostic target. Missing ancestors/controllers or restrictive
  NAS namespaces can limit attribution; no blanket NAS/rootless compatibility claim.
- Remote notifications, SSO, eBPF, AI and Kubernetes are outside v0.1.

## Reproduction

Use `go test ./...`, `go test -race ./...` on a supported compiler environment,
`ROOTLENS_LIVE=1 go test ./internal/agent -run TestLiveLinuxCollector -v`, and
`docker compose -f tests/proxy/compose.yml up --abort-on-container-exit --exit-code-from test`.
The proxy test uses only synthetic sockets, never the real Docker socket.

Start `rootlens demo` before `npm run test:e2e` in `web`. The demo exercises actual
ingestion and rules using synthetic input; it is not live host-monitoring evidence.
