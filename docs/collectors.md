# Collector capability and permission matrix

| Source | Purpose | Default permission | Limits |
|---|---|---|---|
| proc/stat | Host/per-core execution, iowait, steal | Read-only, usually unprivileged | Iowait is not a saturation measure |
| proc/meminfo, vmstat | Available memory, swap allocation/activity | Read-only | High use alone is not memory pressure |
| proc/pressure | CPU/memory/I/O stall fractions | PSI-enabled kernel | Missing files disable corresponding rules |
| proc/diskstats, sys/dev/block | Block bytes, operations, average latency, queue, topology | Read-only | No percentiles; stacked layers must not be summed |
| proc/net/dev | Host network traffic and errors | Host network namespace | Default filters omit loopback/veth |
| statfs | Selected local filesystem capacity | Path visibility and traversal | One bounded worker per configured path; no NFS/CIFS |
| sys/class/hwmon | Temperatures | Readable sensor files | No probing, driver loading or privileged SMART |
| Docker API | Inventory, lifecycle, basic stats | Explicit socket/proxy access | Daemon event replay is bounded |
| cgroups v2 | Resource accounting, PSI, quotas, OOM counters | Readable validated hierarchy | Missing controller fields remain absent |

Each collector reports availability independently. No automatic permission elevation,
kernel parameter changes or destructive actions exist. The hub is not mounted into
the host's proc, sysfs, cgroup or Docker interfaces.

Set `collect_processes: true` to enable current process summaries. Every fifteen
seconds, a bounded scan selects up to ten CPU and ten RSS leaders among visible
processes. PID/start-time identity prevents reused PIDs from inheriting CPU deltas.
CPU baseline or clock-rate uncertainty stays unavailable. Permissions and the 250ms
scan budget can limit coverage. No command lines, environments or process history are
collected. The hub stores only the latest snapshot in memory and marks it stale after
45 seconds. Basic CPU/container monitoring can work on legacy hosts; full diagnostics
require modern capabilities and are not guaranteed for any NAS brand as a whole.

Native and container agents differ in namespace visibility. The supplied Compose
agent explicitly shares host network/cgroup namespaces and mounts only proc/sysfs/
cgroups read-only. An agent that cannot validate a container cgroup falls back to
basic Docker stats instead of guessing a directory.

Container I/O is compared only within observed block-device accounting layers.
Buffered writeback, shared files, filesystem support and host activity prevent exact
application attribution. Network stats are sampled at the configured frame cadence
to retain correct interval totals; requests have bounded concurrency and deadlines.

Rootless Docker, all cgroups v1 layouts, remote filesystems, ZFS attribution and
native NAS packages remain unqualified. Filesystem sensor identities are installation
local; keep configured filesystem ordering stable to preserve their history.
