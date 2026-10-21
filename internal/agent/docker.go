package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"rootlens/internal/protocol"
	"strconv"
	"strings"
	"sync"
	"time"
)

type containerInfo struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	State  string            `json:"State"`
	Image  string            `json:"Image"`
	Labels map[string]string `json:"Labels"`
}
type inspectInfo struct {
	State struct {
		Pid       int    `json:"Pid"`
		StartedAt string `json:"StartedAt"`
		OOMKilled bool   `json:"OOMKilled"`
	} `json:"State"`
	HostConfig struct {
		Memory    int64 `json:"Memory"`
		NanoCpus  int64 `json:"NanoCpus"`
		CPUQuota  int64 `json:"CpuQuota"`
		CPUPeriod int64 `json:"CpuPeriod"`
	} `json:"HostConfig"`
	RestartCount int `json:"RestartCount"`
}
type container struct {
	Entity  protocol.Entity
	PID     int
	Cgroup  string
	Running bool
}
type Docker struct {
	config         Config
	collector      *Collector
	client         *http.Client
	prefix         string
	containers     map[string]*container
	lastInventory  time.Time
	mu             sync.Mutex
	events         []protocol.Event
	eventStarted   bool
	eventConnected bool
	lostEvents     int
	cancel         context.CancelFunc
}

func NewDocker(config Config, c *Collector) *Docker {
	transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", config.DockerSocket)
	}, MaxIdleConns: 4, MaxIdleConnsPerHost: 4, ResponseHeaderTimeout: 3 * time.Second}
	return &Docker{config: config, collector: c, client: &http.Client{Transport: transport}, containers: map[string]*container{}}
}
func (d *Docker) get(ctx context.Context, path string, out any) error {
	req, e := http.NewRequestWithContext(ctx, "GET", "http://docker"+path, nil)
	if e != nil {
		return e
	}
	resp, e := d.client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("docker_status_%d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}
func (d *Docker) inventory(now time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if d.prefix == "" {
		var v struct {
			APIVersion string `json:"ApiVersion"`
		}
		if e := d.get(ctx, "/version", &v); e != nil {
			return e
		}
		parts := strings.Split(v.APIVersion, ".")
		if len(parts) != 2 {
			return fmt.Errorf("invalid_docker_version")
		}
		minor, e := strconv.Atoi(parts[1])
		if e != nil || parts[0] != "1" || minor < 41 {
			return fmt.Errorf("docker_api_requires_1_41")
		}
		if minor > 55 {
			minor = 55
		}
		d.prefix = fmt.Sprintf("/v1.%d", minor)
	}
	var list []containerInfo
	if e := d.get(ctx, d.prefix+"/containers/json?all=true", &list); e != nil {
		return e
	}
	if len(list) > 100 {
		return fmt.Errorf("container_limit_100")
	}
	next := map[string]*container{}
	for _, item := range list {
		if !protocol.ValidID(item.ID) || len(item.ID) != 64 {
			continue
		}
		var inspect inspectInfo
		if e := d.get(ctx, d.prefix+"/containers/"+item.ID+"/json", &inspect); e != nil {
			if old := d.containers[item.ID]; old != nil {
				next[item.ID] = old
			}
			continue
		}
		name := item.ID[:12]
		if len(item.Names) > 0 {
			name = strings.TrimPrefix(item.Names[0], "/")
		}
		g := sha256.Sum256([]byte(d.collector.Boot + inspect.State.StartedAt))
		generation := hex.EncodeToString(g[:12])
		meta := map[string]string{"status": item.State, "image": item.Image, "restarts": strconv.Itoa(inspect.RestartCount)}
		if len(inspect.State.StartedAt) < 100 {
			meta["started_at"] = inspect.State.StartedAt
		}
		if inspect.State.OOMKilled {
			eventID := sha256.Sum256([]byte(item.ID + generation + "oom-state"))
			d.push(protocol.Event{ID: hex.EncodeToString(eventID[:16]), EntityID: item.ID, At: now.UTC(), Type: "oom", Message: "Docker OOMKilled state was observed; the original event time is unknown."})
		}
		if inspect.HostConfig.Memory > 0 {
			meta["memory_limit"] = strconv.FormatInt(inspect.HostConfig.Memory, 10)
		}
		for _, key := range []string{"com.docker.compose.project", "com.docker.compose.service"} {
			if value := item.Labels[key]; value != "" && len(value) < 256 {
				meta[key] = value
			}
		}
		cg, e := ResolveCgroup(d.config.Proc, d.config.Cgroup, inspect.State.Pid)
		if e == nil && !strings.Contains(filepath.ToSlash(cg), item.ID) {
			e = fmt.Errorf("container_identity_mismatch")
			cg = ""
		}
		if e != nil {
			meta["cgroup"] = "unavailable"
		} else {
			meta["cgroup"] = "v2"
		}
		next[item.ID] = &container{Entity: protocol.Entity{ID: item.ID, Kind: "container", Name: name, Generation: generation, Metadata: meta}, PID: inspect.State.Pid, Cgroup: cg, Running: item.State == "running"}
	}
	d.containers = next
	for key := range d.collector.previous {
		candidate := strings.TrimPrefix(key, "container:")
		if len(candidate) >= 64 {
			id := candidate[:64]
			if _, e := hex.DecodeString(id); e == nil {
				ctr, ok := next[id]
				if !ok || !strings.Contains(key, ctr.Entity.Generation) {
					delete(d.collector.previous, key)
				}
			}
		}
	}
	d.lastInventory = now
	if !d.eventStarted {
		ctx, cancel := context.WithCancel(context.Background())
		d.cancel = cancel
		d.eventStarted = true
		go d.watch(ctx)
	}
	return nil
}

// ResolveCgroup validates a host PID mapping and refuses traversal or escapes.
func ResolveCgroup(proc, root string, pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("container_not_running")
	}
	before, e := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "stat"))
	if e != nil {
		return "", e
	}
	data, e := read(filepath.Join(proc, strconv.Itoa(pid), "cgroup"))
	if e != nil {
		return "", e
	}
	var rel string
	for _, line := range strings.Split(data, "\n") {
		if strings.HasPrefix(line, "0::") {
			rel = strings.TrimPrefix(line, "0::")
			break
		}
	}
	if rel == "" || strings.Contains(rel, "..") || rel == "/" {
		return "", fmt.Errorf("unmapped_cgroup")
	}
	resolvedRoot, e := filepath.EvalSymlinks(root)
	if e != nil {
		return "", e
	}
	candidate, e := filepath.EvalSymlinks(filepath.Join(resolvedRoot, strings.TrimPrefix(rel, "/")))
	if e != nil {
		return "", e
	}
	relative, e := filepath.Rel(resolvedRoot, candidate)
	if e != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("cgroup_escape")
	}
	after, e := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "stat"))
	if e != nil {
		return "", e
	}
	if processStart(string(before)) == "" || processStart(string(before)) != processStart(string(after)) {
		return "", fmt.Errorf("pid_reused")
	}
	if _, e = os.Stat(filepath.Join(candidate, "cgroup.procs")); e != nil {
		return "", e
	}
	return candidate, nil
}
func processStart(stat string) string {
	i := strings.LastIndex(stat, ")")
	if i < 0 {
		return ""
	}
	f := strings.Fields(stat[i+1:])
	if len(f) < 20 {
		return ""
	}
	return f[19]
}
func (d *Docker) Collect(now time.Time, interval float64) ([]protocol.Entity, []protocol.Sample, map[string]protocol.Capability, []protocol.Event) {
	caps := map[string]protocol.Capability{}
	d.mu.Lock()
	for _, event := range d.events {
		if event.Type == "start" || event.Type == "die" || event.Type == "destroy" || event.Type == "update" {
			d.lastInventory = time.Time{}
			break
		}
	}
	d.mu.Unlock()
	var inventoryErr error
	if now.Sub(d.lastInventory) >= 30*time.Second {
		inventoryErr = d.inventory(now)
	}
	caps["docker"] = status(inventoryErr, now)
	if inventoryErr != nil && len(d.containers) == 0 {
		return nil, nil, caps, nil
	}
	entities := []protocol.Entity{}
	samples := []protocol.Sample{}
	// Sample network counters with the frame cadence so delta totals retain their
	// true denominator. Four workers and a shared deadline bound daemon overhead.
	statsByID := d.collectStats()
	ancestorSeen := map[string]bool{}
	for id, ctr := range d.containers {
		entities = append(entities, ctr.Entity)
		if !ctr.Running {
			continue
		}
		values := map[string]float64{}
		generation := ctr.Entity.Generation
		if ctr.Cgroup != "" {
			// A changed cgroup membership invalidates the mapping before reading counters.
			cg, e := ResolveCgroup(d.config.Proc, d.config.Cgroup, ctr.PID)
			if e != nil || cg != ctr.Cgroup {
				caps["cgroup."+id[:12]] = protocol.Capability{Status: "error", Reason: "Container cgroup mapping changed; awaiting inventory refresh.", At: now}
				continue
			}
			cpuData, e := read(filepath.Join(cg, "cpu.stat"))
			if e == nil {
				if cpu, e := ParseKV(cpuData); e == nil {
					if delta, ok := d.collector.delta("container:"+id+":"+generation+":cpu", cpu); ok {
						values["cpu.cores"] = delta["usage_usec"] / (interval * 1e6)
						if periods, ok := delta["nr_periods"]; ok {
							values["cpu.periods"] = periods
							values["cpu.throttled_periods"] = delta["nr_throttled"]
							if periods > 0 {
								values["cpu.throttled_pct"] = delta["nr_throttled"] / periods * 100
							}
						}
					}
				}
			}
			caps["cgroup."+id[:12]] = status(e, now)
			ancestorIDs := d.ancestors(cg, interval, ancestorSeen, &entities, &samples)
			if len(ancestorIDs) > 0 {
				ctr.Entity.Metadata["ancestor_scope_ids"] = strings.Join(ancestorIDs, ",")
			}
			if text, e := read(filepath.Join(cg, "cpu.max")); e == nil {
				p := strings.Fields(text)
				if len(p) == 2 && p[0] != "max" {
					q, e1 := strconv.ParseFloat(p[0], 64)
					period, e2 := strconv.ParseFloat(p[1], 64)
					if e1 == nil && e2 == nil && period > 0 {
						values["cpu.quota_cores"] = q / period
					}
				}
			}
			for filename, key := range map[string]string{"memory.current": "memory.used_bytes", "memory.max": "memory.limit_bytes"} {
				if text, e := read(filepath.Join(cg, filename)); e == nil {
					if v, e := strconv.ParseFloat(strings.TrimSpace(text), 64); e == nil {
						values[key] = v
					}
				}
			}
			if text, e := read(filepath.Join(cg, "memory.stat")); e == nil {
				if m, e := ParseKV(text); e == nil {
					used, ok := values["memory.used_bytes"]
					if ok && used >= float64(m["inactive_file"]) {
						values["memory.working_bytes"] = used - float64(m["inactive_file"])
					}
				}
			}
			if text, e := read(filepath.Join(cg, "memory.events.local")); e == nil {
				if m, e := ParseKV(text); e == nil {
					if delta, ok := d.collector.delta("container:"+id+":"+generation+":oom", CounterSet{"oom_kill": m["oom_kill"]}); ok {
						values["memory.oom_kills"] = delta["oom_kill"]
					}
				}
			}
			for _, resource := range []string{"cpu", "memory", "io"} {
				if text, e := read(filepath.Join(cg, resource+".pressure")); e == nil {
					if p, e := ParsePSI(text); e == nil {
						if delta, ok := d.collector.delta(id+":"+generation+":"+resource, p); ok {
							for kind, v := range delta {
								if resource == "cpu" && kind == "full" {
									continue
								}
								values[resource+"."+kind+"_pct"] = v / (interval * 1e6) * 100
							}
						}
					}
				}
			}
			if text, e := read(filepath.Join(cg, "io.stat")); e == nil {
				for _, line := range strings.Split(text, "\n") {
					p := strings.Fields(line)
					if len(p) < 2 {
						continue
					}
					device := p[0]
					if !protocol.ValidID(device) {
						continue
					}
					raw := CounterSet{}
					valid := true
					for _, field := range p[1:] {
						k, v, ok := strings.Cut(field, "=")
						if !ok {
							continue
						}
						n, e := strconv.ParseUint(v, 10, 64)
						if e != nil {
							valid = false
							break
						}
						raw[k] = n
					}
					if !valid {
						continue
					}
					ioID := id + ":" + device
					mapping := "unverified"
					if d.collector.devices[device] {
						if _, e := os.Stat(filepath.Join(d.config.Sys, "dev/block", device)); e == nil {
							mapping = "validated"
						}
					}
					entities = append(entities, protocol.Entity{ID: ioID, Kind: "container_io", Name: ctr.Entity.Name, Generation: generation, Metadata: map[string]string{"device": device, "container_id": id, "mapping": mapping}})
					if delta, ok := d.collector.delta(ioID+":"+generation, raw); ok {
						samples = append(samples, protocol.Sample{EntityID: ioID, Generation: generation, Values: map[string]float64{"disk.read_bytes": delta["rbytes"], "disk.write_bytes": delta["wbytes"], "disk.read_ops": delta["rios"], "disk.write_ops": delta["wios"]}})
					}
				}
			}
		} else {
			caps["cgroup."+id[:12]] = protocol.Capability{Status: "unsupported", Reason: "Validated cgroups v2 mapping unavailable; Docker basic statistics only.", At: now}
		}
		if stats, available := statsByID[id]; available {
			raw := CounterSet{"rx": 0, "tx": 0}
			for _, n := range stats.Networks {
				raw["rx"] += n.Rx
				raw["tx"] += n.Tx
			}
			if delta, ok := d.collector.delta(id+":"+generation+":network", raw); ok {
				values["net.rx_bytes"] = delta["rx"]
				values["net.tx_bytes"] = delta["tx"]
			}
			if ctr.Cgroup == "" {
				values["memory.used_bytes"] = float64(stats.Memory.Usage)
				if delta, ok := d.collector.delta(id+":"+generation+":fallback", CounterSet{"cpu": stats.CPU.Usage.Total}); ok {
					values["cpu.cores"] = delta["cpu"] / (interval * 1e9)
				}
			}
		} else {
			delete(d.collector.previous, id+":"+generation+":network")
			delete(d.collector.previous, id+":"+generation+":fallback")
			caps["docker.stats"] = protocol.Capability{Status: "timeout", Reason: "Some container statistics exceeded the bounded collection deadline.", At: now}
		}
		samples = append(samples, protocol.Sample{EntityID: id, Generation: generation, Values: values})
	}
	d.mu.Lock()
	pending := d.events
	d.events = nil
	lost := d.lostEvents
	d.lostEvents = 0
	connected := d.eventConnected
	d.mu.Unlock()
	events := []protocol.Event{}
	eventStatus := "available"
	eventReason := "Docker lifecycle stream connected."
	if !connected {
		eventStatus = "error"
		eventReason = "Docker lifecycle stream is disconnected; event coverage is incomplete."
	}
	if lost > 0 {
		if len(pending) > 254 {
			pending = pending[len(pending)-254:]
		}
		events = append(events, protocol.Event{ID: fmt.Sprintf("events-overflow-%d", now.UnixNano()), At: now.UTC(), Type: "telemetry_gap", Message: fmt.Sprintf("Docker event buffer exceeded its limit; at least %d events were dropped.", lost)})
		eventStatus = "error"
		eventReason = "Docker event buffer overflowed; event coverage is incomplete."
	}
	caps["docker.events"] = protocol.Capability{Status: eventStatus, Reason: eventReason, At: now}
	for _, e := range pending {
		if e.EntityID != "" && d.containers[e.EntityID] == nil && protocol.ValidID(e.EntityID) && len(e.EntityID) == 64 {
			seen := false
			for _, entity := range entities {
				if entity.ID == e.EntityID {
					seen = true
					break
				}
			}
			if !seen {
				entities = append(entities, protocol.Entity{ID: e.EntityID, Kind: "container", Name: e.EntityID[:12], Generation: "event-only", Metadata: map[string]string{"status": "unknown", "cgroup": "unavailable"}})
			}
		}
		if e.EntityID == "" || protocol.ValidID(e.EntityID) {
			events = append(events, e)
		}
	}
	available, unavailable := 0, 0
	for key, capability := range caps {
		if strings.HasPrefix(key, "cgroup.") {
			if capability.Status == "available" {
				available++
			} else {
				unavailable++
			}
			delete(caps, key)
		}
	}
	if available+unavailable > 0 {
		state := "available"
		if unavailable > 0 {
			state = "unsupported"
		}
		caps["cgroups.v2"] = protocol.Capability{Status: state, Reason: fmt.Sprintf("%d containers have readable validated cgroups; %d have limited visibility.", available, unavailable), At: now}
	}
	return entities, samples, caps, events
}

type dockerStats struct {
	CPU struct {
		Usage struct {
			Total uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
	} `json:"cpu_stats"`
	Memory struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		Rx uint64 `json:"rx_bytes"`
		Tx uint64 `json:"tx_bytes"`
	} `json:"networks"`
}

func (d *Docker) collectStats() map[string]dockerStats {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	jobs := make(chan string, len(d.containers))
	for id, c := range d.containers {
		if c.Running {
			jobs <- id
		}
	}
	close(jobs)
	results := map[string]dockerStats{}
	var mu sync.Mutex
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for id := range jobs {
				if ctx.Err() != nil {
					return
				}
				var stats dockerStats
				if d.get(ctx, d.prefix+"/containers/"+id+"/stats?stream=false&one-shot=true", &stats) == nil {
					mu.Lock()
					results[id] = stats
					mu.Unlock()
				}
			}
		}()
	}
	workers.Wait()
	return results
}
func (d *Docker) watch(ctx context.Context) {
	since := time.Now().Unix()
	for ctx.Err() == nil {
		filters := url.QueryEscape(`{"type":["container"],"event":["start","die","oom","destroy","restart","update"]}`)
		req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://docker%s/events?since=%d&filters=%s", d.prefix, since, filters), nil)
		resp, e := d.client.Do(req)
		if e == nil && resp.StatusCode == 200 {
			d.mu.Lock()
			d.eventConnected = true
			d.mu.Unlock()
			decoder := json.NewDecoder(resp.Body)
			for ctx.Err() == nil {
				var event struct {
					Action   string `json:"Action"`
					Time     int64  `json:"time"`
					TimeNano int64  `json:"timeNano"`
					Actor    struct {
						ID string `json:"ID"`
					} `json:"Actor"`
				}
				if e = decoder.Decode(&event); e != nil {
					break
				}
				at := time.Unix(event.Time, 0)
				if event.TimeNano != 0 {
					at = time.Unix(0, event.TimeNano)
				}
				since = at.Unix()
				hash := sha256.Sum256([]byte(event.Actor.ID + event.Action + at.Format(time.RFC3339Nano)))
				d.push(protocol.Event{ID: hex.EncodeToString(hash[:16]), EntityID: event.Actor.ID, At: at.UTC(), Type: event.Action, Message: "Docker reported a container " + event.Action + " event."})
			}
			resp.Body.Close()
		} else if resp != nil {
			resp.Body.Close()
		}
		d.mu.Lock()
		d.eventConnected = false
		d.mu.Unlock()
		d.push(protocol.Event{ID: fmt.Sprintf("docker-gap-%d", time.Now().UnixNano()), At: time.Now().UTC(), Type: "telemetry_gap", Message: "Docker event stream disconnected. Events may be missing; current inventory will be reconciled."})
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}
func (d *Docker) push(e protocol.Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.events) >= 256 {
		d.events = d.events[1:]
		d.lostEvents++
	}
	d.events = append(d.events, e)
}
