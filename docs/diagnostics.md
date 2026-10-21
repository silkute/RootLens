# Interpreting diagnostics

RootLens does not claim to infer a root cause from correlation. Reports distinguish
measured facts, supported resource interpretations, possible workload contributions
and missing evidence. A silent rule is not proof that an application is healthy.

## Initial rules

| Rule | Entry | Recovery |
|---|---|---|
| I/O pressure | Host `io.some_pct` >=10% for a 60s window | <5% for 120s |
| CPU contention | Host `cpu.some_pct` >=10% for a 60s window | <5% for 120s |
| Memory pressure | Host `memory.some_pct` >=5% for a 60s window | <2% for 120s |
| Container throttling | >=20% throttled periods, >=20 observed periods, 60s window | <5% for 120s |
| Instability | Observed OOM evidence, or 3 starts after observed termination in 10m | 10 quiet minutes |

Sampled rules require 80% qualifying coverage. Counter generations and gaps reset
pending evidence; missing measurements never resolve incidents. An active incident
becomes interrupted after a prolonged gap. These thresholds are engineering defaults
awaiting broader calibration, not universal performance limits.

Peak measured window evidence is retained through recovery. Throttled-period fraction
is not a percentage of wall time lost. An OOM event is evidence of an observed OOM
condition; it does not establish the cause of unrelated restarts.

## Workload evidence

I/O reports compare the preceding ten-minute baseline with the incident. Five minutes
of baseline coverage is required before calling a workload newly increased. Otherwise
it is described only as an active workload. Rank at most three mapped workloads by
incremental accounted activity.

Shares always refer to **observed container block activity on the same device**.
They are not a percentage of all host I/O. Background processes, writeback, shared
inodes and layered devices may be missing or differently accounted. Seeing a workload
under pressure does not show that it created that pressure.

Reports suggest investigation, never automatic remediation. Use application logs or
latency probes to establish user-visible impact. Repeated workloads and unrelated
changes should be investigated before concluding causation.
