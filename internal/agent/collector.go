package agent

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"rootlens/internal/protocol"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Hub              string   `json:"hub"`
	HostID           string   `json:"host_id"`
	CAFile           string   `json:"ca_file"`
	CredentialFile   string   `json:"credential_file"`
	EnrollmentFile   string   `json:"enrollment_file,omitempty"`
	Sampling         int      `json:"sampling_seconds"`
	Proc             string   `json:"proc"`
	Sys              string   `json:"sys"`
	Cgroup           string   `json:"cgroup"`
	DockerSocket     string   `json:"docker_socket,omitempty"`
	Filesystems      []string `json:"filesystems,omitempty"`
	CollectProcesses bool     `json:"collect_processes,omitempty"`
}

func (c *Config) Defaults() {
	if c.Sampling == 0 {
		c.Sampling = 5
	}
	if c.Proc == "" {
		c.Proc = "/proc"
	}
	if c.Sys == "" {
		c.Sys = "/sys"
	}
	if c.Cgroup == "" {
		c.Cgroup = "/sys/fs/cgroup"
	}
}

type CounterSet map[string]uint64

func ParseKV(data string) (CounterSet, error) {
	out := CounterSet{}
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, e := strconv.ParseUint(f[1], 10, 64)
		if e != nil {
			return nil, fmt.Errorf("invalid_counter")
		}
		out[strings.TrimSuffix(f[0], ":")] = v
	}
	return out, nil
}
func ParsePSI(data string) (CounterSet, error) {
	out := CounterSet{}
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		for _, field := range f[1:] {
			if strings.HasPrefix(field, "total=") {
				v, e := strconv.ParseUint(strings.TrimPrefix(field, "total="), 10, 64)
				if e != nil {
					return nil, fmt.Errorf("invalid_pressure")
				}
				out[f[0]] = v
			}
		}
	}
	if _, ok := out["some"]; !ok {
		return nil, fmt.Errorf("pressure_missing_some")
	}
	return out, nil
}
func Delta(previous, current uint64) (uint64, bool) {
	if current < previous {
		return 0, false
	}
	return current - previous, true
}

type Collector struct {
	Config          Config
	Boot            string
	previous        map[string]CounterSet
	previousCycle   map[string]uint64
	cycle           uint64
	ProcessSnapshot *protocol.ProcessSnapshot
	processPrevious map[int]processCounter
	processAt       time.Time
	last            time.Time
	devices         map[string]bool
	docker          *Docker
	fsWorkers       map[string]*filesystemWorker
}

func NewCollector(config Config) *Collector {
	config.Defaults()
	boot, _ := os.ReadFile(filepath.Join(config.Proc, "sys/kernel/random/boot_id"))
	b := strings.TrimSpace(string(boot))
	if !protocol.ValidID(b) {
		b = "unknown-boot"
	}
	c := &Collector{Config: config, Boot: b, previous: map[string]CounterSet{}, previousCycle: map[string]uint64{}, devices: map[string]bool{}, fsWorkers: map[string]*filesystemWorker{}}
	if config.DockerSocket != "" {
		c.docker = NewDocker(config, c)
	}
	return c
}
func (c *Collector) delta(key string, current CounterSet) (map[string]float64, bool) {
	old, ok := c.previous[key]
	lastCycle := c.previousCycle[key]
	c.previous[key] = current
	c.previousCycle[key] = c.cycle
	if !ok || lastCycle+1 != c.cycle {
		return nil, false
	}
	out := map[string]float64{}
	for k, v := range current {
		p, present := old[k]
		if !present {
			return nil, false
		}
		d, valid := Delta(p, v)
		if !valid {
			return nil, false
		}
		out[k] = float64(d)
	}
	return out, true
}
func read(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	var b strings.Builder
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		b.WriteString(scanner.Text())
		b.WriteByte('\n')
		if b.Len() > 4<<20 {
			return "", fmt.Errorf("collector_file_limit")
		}
	}
	return b.String(), scanner.Err()
}
func status(err error, at time.Time) protocol.Capability {
	state := "available"
	reason := ""
	if err != nil {
		state = "error"
		reason = "Collector input could not be read or parsed."
		if os.IsPermission(err) {
			state = "permission_denied"
			reason = "Permission denied by host policy."
		} else if os.IsNotExist(err) {
			state = "unsupported"
			reason = "Required kernel interface is unavailable."
		}
	}
	return protocol.Capability{Status: state, Reason: reason, At: at}
}
func (c *Collector) Collect(now time.Time) (protocol.Frame, []protocol.Entity, map[string]protocol.Capability, []protocol.Event) {
	c.cycle++
	for key, last := range c.previousCycle {
		if last+12 < c.cycle {
			delete(c.previousCycle, key)
			delete(c.previous, key)
		}
	}
	interval := float64(c.Config.Sampling)
	clockJump := false
	if !c.last.IsZero() {
		interval = now.Sub(c.last).Seconds()
		clockJump = math.Abs(now.UTC().Sub(c.last.UTC()).Seconds()-interval) > 2
	}
	c.last = now
	if interval <= 0 || interval > 60 {
		c.previous = map[string]CounterSet{}
		interval = float64(c.Config.Sampling)
	}
	f := protocol.Frame{At: now.UTC(), Interval: interval, Samples: []protocol.Sample{}}
	entities := []protocol.Entity{{ID: "host", Kind: "host", Name: hostname(), Generation: c.Boot, Metadata: map[string]string{"architecture": runtime.GOARCH, "os": runtime.GOOS}}}
	caps := map[string]protocol.Capability{}
	events := []protocol.Event{}
	if clockJump {
		events = append(events, protocol.Event{ID: fmt.Sprintf("clock-jump-%d", now.UnixNano()), At: now.UTC(), Type: "clock_jump", Message: "Wall clock changed relative to monotonic collection time; timeline alignment is uncertain."})
	}
	values := map[string]float64{}
	data, err := read(filepath.Join(c.Config.Proc, "stat"))
	caps["cpu"] = status(err, now)
	if err == nil {
		count := 0
		for _, line := range strings.Split(data, "\n") {
			parts := strings.Fields(line)
			if len(parts) < 8 || !strings.HasPrefix(parts[0], "cpu") {
				continue
			}
			ticks := CounterSet{}
			valid := true
			for i, name := range []string{"user", "nice", "system", "idle", "iowait", "irq", "softirq", "steal"} {
				if i+1 >= len(parts) {
					ticks[name] = 0
					continue
				}
				v, e := strconv.ParseUint(parts[i+1], 10, 64)
				if e != nil {
					valid = false
					break
				}
				ticks[name] = v
			}
			if !valid {
				caps["cpu"] = status(fmt.Errorf("invalid_cpu"), now)
				continue
			}
			d, ok := c.delta(parts[0], ticks)
			if parts[0] != "cpu" {
				count++
			}
			if !ok {
				continue
			}
			total := 0.0
			for _, v := range d {
				total += v
			}
			if total <= 0 {
				continue
			}
			v := map[string]float64{"cpu.busy_pct": math.Max(0, (total-d["idle"]-d["iowait"]-d["steal"])/total*100), "cpu.iowait_pct": d["iowait"] / total * 100, "cpu.steal_pct": d["steal"] / total * 100}
			if parts[0] == "cpu" {
				for k, n := range v {
					values[k] = n
				}
			} else {
				id := "core:" + parts[0]
				entities = append(entities, protocol.Entity{ID: id, Kind: "core", Name: parts[0], Generation: c.Boot})
				f.Samples = append(f.Samples, protocol.Sample{EntityID: id, Generation: c.Boot, Values: v})
			}
		}
		values["cpu.count"] = float64(count)
	}
	data, err = read(filepath.Join(c.Config.Proc, "meminfo"))
	if err == nil {
		var memory CounterSet
		memory, err = ParseKV(data)
		if err == nil {
			total, available := float64(memory["MemTotal"])*1024, float64(memory["MemAvailable"])*1024
			if total > 0 {
				values["memory.total_bytes"] = total
				if _, ok := memory["MemAvailable"]; ok {
					values["memory.available_bytes"] = available
					values["memory.used_bytes"] = math.Max(0, total-available)
					values["memory.used_pct"] = (total - available) / total * 100
				}
			}
			values["swap.used_bytes"] = float64(memory["SwapTotal"]-memory["SwapFree"]) * 1024
		}
	}
	caps["memory"] = status(err, now)
	if data, e := read(filepath.Join(c.Config.Proc, "vmstat")); e == nil {
		if v, e := ParseKV(data); e == nil {
			if delta, ok := c.delta("vmstat", CounterSet{"pswpout": v["pswpout"]}); ok {
				values["swap.out_bytes"] = delta["pswpout"] * float64(os.Getpagesize())
			}
		}
	}
	data, err = read(filepath.Join(c.Config.Proc, "loadavg"))
	if err == nil {
		fields := strings.Fields(data)
		if len(fields) >= 3 {
			for i, k := range []string{"load.1", "load.5", "load.15"} {
				v, e := strconv.ParseFloat(fields[i], 64)
				if e == nil {
					values[k] = v
				}
			}
		}
	}
	caps["load"] = status(err, now)
	if data, e := read(filepath.Join(c.Config.Proc, "uptime")); e == nil {
		fields := strings.Fields(data)
		if len(fields) > 0 {
			if v, e := strconv.ParseFloat(fields[0], 64); e == nil {
				values["uptime.seconds"] = v
			}
		}
	}
	for _, resource := range []string{"cpu", "memory", "io"} {
		data, e := read(filepath.Join(c.Config.Proc, "pressure", resource))
		if e == nil {
			var v CounterSet
			v, e = ParsePSI(data)
			if e == nil {
				if d, ok := c.delta("psi:"+resource, v); ok {
					for kind, value := range d {
						if resource == "cpu" && kind == "full" {
							continue
						}
						values[resource+"."+kind+"_pct"] = math.Min(100, value/(interval*1e6)*100)
					}
				}
			}
		}
		caps["psi."+resource] = status(e, now)
	}
	f.Samples = append(f.Samples, protocol.Sample{EntityID: "host", Generation: c.Boot, Values: values})
	c.disk(&f, &entities, caps, now)
	c.network(&f, &entities, caps, now)
	c.filesystems(&f, &entities, caps, now)
	c.temperatures(&f, &entities, caps, now)
	if c.docker != nil {
		es, ss, cs, ev := c.docker.Collect(now, interval)
		entities = append(entities, es...)
		f.Samples = append(f.Samples, ss...)
		for k, v := range cs {
			caps[k] = v
		}
		events = append(events, ev...)
	} else {
		caps["docker"] = protocol.Capability{Status: "unsupported", Reason: "Docker monitoring is not configured.", At: now}
	}
	c.processes(now, caps)
	return f, entities, caps, events
}
func hostname() string {
	v, e := os.Hostname()
	if e != nil {
		return "Linux host"
	}
	return v
}
func (c *Collector) disk(f *protocol.Frame, entities *[]protocol.Entity, caps map[string]protocol.Capability, now time.Time) {
	data, err := read(filepath.Join(c.Config.Proc, "diskstats"))
	caps["disk"] = status(err, now)
	if err != nil {
		return
	}
	c.devices = map[string]bool{}
	for _, line := range strings.Split(data, "\n") {
		p := strings.Fields(line)
		if len(p) < 14 || strings.HasPrefix(p[2], "loop") || strings.HasPrefix(p[2], "ram") {
			continue
		}
		device := p[0] + ":" + p[1]
		id := "device:" + device
		raw := CounterSet{}
		valid := true
		for _, field := range []struct {
			index int
			name  string
		}{{3, "read_ops"}, {5, "read_sectors"}, {6, "read_ms"}, {7, "write_ops"}, {9, "write_sectors"}, {10, "write_ms"}, {12, "busy_ms"}, {13, "weighted_ms"}} {
			v, e := strconv.ParseUint(p[field.index], 10, 64)
			if e != nil {
				valid = false
				break
			}
			raw[field.name] = v
		}
		if !valid {
			continue
		}
		c.devices[device] = true
		meta := map[string]string{"device": device, "layer": "block-device"}
		slaves, _ := os.ReadDir(filepath.Join(c.Config.Sys, "dev/block", device, "slaves"))
		if len(slaves) > 0 {
			meta["layer"] = "stacked"
		}
		*entities = append(*entities, protocol.Entity{ID: id, Kind: "device", Name: p[2], Generation: c.Boot, Metadata: meta})
		d, ok := c.delta(id, raw)
		if !ok {
			continue
		}
		v := map[string]float64{"disk.read_bytes": d["read_sectors"] * 512, "disk.write_bytes": d["write_sectors"] * 512, "disk.read_ops": d["read_ops"], "disk.write_ops": d["write_ops"], "disk.read_ms": d["read_ms"], "disk.write_ms": d["write_ms"], "disk.busy_pct": math.Min(100, d["busy_ms"]/(f.Interval*1000)*100), "disk.queue": d["weighted_ms"] / (f.Interval * 1000)}
		if ops := d["read_ops"] + d["write_ops"]; ops > 0 {
			v["disk.await_ms"] = (d["read_ms"] + d["write_ms"]) / ops
		}
		f.Samples = append(f.Samples, protocol.Sample{EntityID: id, Generation: c.Boot, Values: v})
	}
}
func (c *Collector) network(f *protocol.Frame, entities *[]protocol.Entity, caps map[string]protocol.Capability, now time.Time) {
	data, e := read(filepath.Join(c.Config.Proc, "net/dev"))
	caps["network"] = status(e, now)
	if e != nil {
		return
	}
	for _, line := range strings.Split(data, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "lo" || strings.HasPrefix(name, "veth") {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 16 {
			continue
		}
		raw := CounterSet{}
		valid := true
		for k, index := range map[string]int{"rx": 0, "tx": 8, "rxerr": 2, "txerr": 10} {
			v, e := strconv.ParseUint(fields[index], 10, 64)
			if e != nil {
				valid = false
				break
			}
			raw[k] = v
		}
		if !valid {
			continue
		}
		id := "net:" + name
		if !protocol.ValidID(id) {
			continue
		}
		*entities = append(*entities, protocol.Entity{ID: id, Kind: "network", Name: name, Generation: c.Boot})
		if d, ok := c.delta(id, raw); ok {
			f.Samples = append(f.Samples, protocol.Sample{EntityID: id, Generation: c.Boot, Values: map[string]float64{"net.rx_bytes": d["rx"], "net.tx_bytes": d["tx"], "net.errors": d["rxerr"] + d["txerr"]}})
		}
	}
}
func (c *Collector) temperatures(f *protocol.Frame, entities *[]protocol.Entity, caps map[string]protocol.Capability, now time.Time) {
	paths, e := filepath.Glob(filepath.Join(c.Config.Sys, "class/hwmon/hwmon*/temp*_input"))
	if e != nil || len(paths) == 0 {
		caps["temperature"] = protocol.Capability{Status: "unsupported", Reason: "No readable temperature sensor was found.", At: now}
		return
	}
	caps["temperature"] = status(nil, now)
	for i, path := range paths {
		if i >= 16 {
			break
		}
		data, e := os.ReadFile(path)
		if e != nil {
			continue
		}
		value, e := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
		if e != nil || value < 0 {
			continue
		}
		id := fmt.Sprintf("sensor:%d", i)
		label, _ := os.ReadFile(strings.TrimSuffix(path, "_input") + "_label")
		name := strings.TrimSpace(string(label))
		if name == "" {
			name = filepath.Base(filepath.Dir(path)) + " " + filepath.Base(path)
		}
		*entities = append(*entities, protocol.Entity{ID: id, Kind: "sensor", Name: name, Generation: c.Boot})
		f.Samples = append(f.Samples, protocol.Sample{EntityID: id, Generation: c.Boot, Values: map[string]float64{"temperature.celsius": value / 1000}})
	}
}
