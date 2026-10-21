# Metric registry

Generated from the Go transport registry. Chart API delta metrics are rates per second.

| Key | Source unit | Kind | Interpretation |
|---|---|---|---|
| `cpu.busy_pct` | % | gauge | CPU execution excluding idle, iowait and steal |
| `cpu.cores` | cores | gauge | Container CPU cores consumed |
| `cpu.count` | cores | gauge | Online logical processors |
| `cpu.iowait_pct` | % | gauge | Reported CPU iowait; not storage saturation |
| `cpu.periods` | periods | delta | Observed bandwidth periods |
| `cpu.quota_cores` | cores | gauge | Configured CPU quota |
| `cpu.some_pct` | % | gauge | Interval CPU pressure some |
| `cpu.steal_pct` | % | gauge | Virtual CPU steal time |
| `cpu.throttled_pct` | % | gauge | Throttled-period fraction, not time lost |
| `cpu.throttled_periods` | periods | delta | Throttled bandwidth periods |
| `disk.await_ms` | ms | ratio | Operation-weighted average completed-request time |
| `disk.busy_pct` | % | gauge | Device busy estimate, not saturation |
| `disk.queue` | requests | gauge | Time-weighted outstanding I/O |
| `disk.read_bytes` | B | delta | Accounted block read bytes |
| `disk.read_ms` | ms | delta | Summed read duration |
| `disk.read_ops` | operations | delta | Completed reads |
| `disk.write_bytes` | B | delta | Accounted block write bytes |
| `disk.write_ms` | ms | delta | Summed write duration |
| `disk.write_ops` | operations | delta | Completed writes |
| `fs.total_bytes` | B | gauge | Filesystem capacity |
| `fs.used_bytes` | B | gauge | Filesystem occupied space |
| `fs.used_pct` | % | gauge | Filesystem space used, not device activity |
| `io.full_pct` | % | gauge | Interval I/O pressure full |
| `io.some_pct` | % | gauge | Interval I/O pressure some |
| `load.1` | tasks | gauge | One-minute load, not CPU usage |
| `load.15` | tasks | gauge | Fifteen-minute load |
| `load.5` | tasks | gauge | Five-minute load |
| `memory.available_bytes` | B | gauge | Host memory available |
| `memory.full_pct` | % | gauge | Interval memory pressure full |
| `memory.limit_bytes` | B | gauge | Finite configured container memory limit |
| `memory.oom_kills` | kills | delta | Observed OOM kills |
| `memory.some_pct` | % | gauge | Interval memory pressure some |
| `memory.total_bytes` | B | gauge | Total host memory |
| `memory.used_bytes` | B | gauge | Host used or container charged memory |
| `memory.used_pct` | % | gauge | Host memory used fraction |
| `memory.working_bytes` | B | gauge | Container charged memory minus inactive file cache estimate |
| `net.errors` | errors | delta | Network error counter increments |
| `net.rx_bytes` | B | delta | Received bytes |
| `net.tx_bytes` | B | delta | Transmitted bytes |
| `swap.out_bytes` | B | delta | Swap-out activity |
| `swap.used_bytes` | B | gauge | Allocated swap |
| `temperature.celsius` | °C | gauge | Readable sensor temperature |
| `uptime.seconds` | s | gauge | Host uptime |
