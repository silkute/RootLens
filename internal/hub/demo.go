package hub

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"rootlens/internal/protocol"
	"rootlens/internal/storage"
	"strings"
	"time"
)

// SeedDemo uses the real ingestion and diagnostic paths. All values are synthetic.
func SeedDemo(ctx context.Context, s *storage.Store) error {
	var count int
	if e := s.DB.QueryRow("SELECT COUNT(*) FROM hosts").Scan(&count); e != nil {
		return e
	}
	if count > 0 {
		return nil
	}
	now := time.Now().UTC().Truncate(5 * time.Second)
	names := []string{"homelab-nas", "docker-node", "edge-server"}
	for n, name := range names {
		host := fmt.Sprintf("demo-%d", n+1)
		if e := s.Write(ctx, func(tx *sql.Tx) error {
			_, e := tx.Exec("INSERT INTO hosts(id,name) VALUES(?,?)", host, name)
			return e
		}); e != nil {
			return e
		}
		entities := []protocol.Entity{{ID: "host", Kind: "host", Name: name, Generation: "demo-boot", Metadata: map[string]string{"architecture": "amd64", "os": "Linux", "cpu": "Intel N95", "kernel": "6.8.0"}}, {ID: "device:8:0", Kind: "device", Name: "sda", Generation: "demo-boot", Metadata: map[string]string{"device": "8:0", "layer": "block-device"}}, {ID: "fs:0", Kind: "filesystem", Name: "/volume1", Generation: "demo-boot"}, {ID: "net:eth0", Kind: "network", Name: "eth0", Generation: "demo-boot"}}
		for j, name := range []string{"frigate", "immich-server", "postgres", "home-assistant", "restic-backup"} {
			id := fmt.Sprintf("container-%d", j)
			entities = append(entities, protocol.Entity{ID: id, Kind: "container", Name: name, Generation: "run-1", Metadata: map[string]string{"status": "running", "image": name + ":stable", "restarts": "0", "cgroup": "v2"}}, protocol.Entity{ID: id + ":8:0", Kind: "container_io", Name: name, Generation: "run-1", Metadata: map[string]string{"container_id": id, "device": "8:0", "mapping": "validated"}})
		}
		for chunk := 0; chunk < 6; chunk++ {
			b := protocol.Batch{Protocol: 1, AgentVersion: "0.1.0-demo", HostID: host, BootID: "demo-boot", Epoch: "demo-epoch", Sequence: fmt.Sprint(chunk + 1), Sampling: 5, SentAt: now, Entities: entities, Capabilities: map[string]protocol.Capability{}}
			for _, key := range []string{"cpu", "memory", "disk", "network", "psi.cpu", "psi.memory", "psi.io", "docker"} {
				b.Capabilities[key] = protocol.Capability{Status: "available", At: now}
			}
			for j := 0; j < 120; j++ {
				i := chunk*120 + j
				at := now.Add(time.Duration(i-719) * 5 * time.Second)
				wave := (math.Sin(float64(i)/13) + 1) / 2
				pressure := 0.2 + wave
				incident := n == 0 && i > 430 && i < 600
				if incident {
					pressure = 18 + wave*12
				}
				if n == 0 && i > 695 {
					pressure = 16 + wave*8
				}
				cpu := 12 + float64(n)*8 + wave*14
				mem := 43 + float64(n)*9 + wave*3
				f := protocol.Frame{At: at, Interval: 5, Samples: []protocol.Sample{{EntityID: "host", Generation: "demo-boot", Values: map[string]float64{"cpu.busy_pct": cpu, "cpu.count": 4, "cpu.steal_pct": 0, "cpu.iowait_pct": pressure / 2, "memory.total_bytes": 8 * 1073741824, "memory.used_bytes": mem / 100 * 8 * 1073741824, "memory.available_bytes": (100 - mem) / 100 * 8 * 1073741824, "memory.used_pct": mem, "swap.used_bytes": 0, "load.1": cpu/20 + pressure/8, "load.5": cpu / 24, "load.15": 1.2, "io.some_pct": pressure, "io.full_pct": pressure / 4, "cpu.some_pct": wave / 2, "memory.some_pct": 0.2, "uptime.seconds": 42*86400 + float64(i)*5}}, {EntityID: "device:8:0", Generation: "demo-boot", Values: map[string]float64{"disk.read_bytes": (4 + pressure*2) * 1048576 * 5, "disk.write_bytes": (2 + wave*5) * 1048576 * 5, "disk.read_ops": 500 + pressure*30, "disk.write_ops": 100 + wave*40, "disk.read_ms": 500 + pressure*900, "disk.write_ms": 100 + pressure*100, "disk.await_ms": 2 + pressure, "disk.queue": pressure / 8, "disk.busy_pct": 20 + pressure*2}}, {EntityID: "fs:0", Generation: "demo-boot", Values: map[string]float64{"fs.used_pct": 62 + float64(n)*9, "fs.total_bytes": 4 * 1099511627776, "fs.used_bytes": (62 + float64(n)*9) / 100 * 4 * 1099511627776}}, {EntityID: "net:eth0", Generation: "demo-boot", Values: map[string]float64{"net.rx_bytes": (3 + wave*5) * 1048576 * 5, "net.tx_bytes": (1 + wave*2) * 1048576 * 5, "net.errors": 0}}}}
				for k := 0; k < 5; k++ {
					id := fmt.Sprintf("container-%d", k)
					activity := float64(k+1) * 400000
					if k == 0 && pressure > 10 {
						activity = 41 * 1048576
					}
					f.Samples = append(f.Samples, protocol.Sample{EntityID: id, Generation: "run-1", Values: map[string]float64{"cpu.cores": 0.04 + float64(k)*0.03 + wave*.08, "memory.used_bytes": float64(k+1) * 130 * 1048576, "memory.limit_bytes": 2 * 1073741824, "net.rx_bytes": wave * 1048576, "net.tx_bytes": wave * 524288, "cpu.periods": 50, "cpu.throttled_periods": 0, "cpu.throttled_pct": 0}}, protocol.Sample{EntityID: id + ":8:0", Generation: "run-1", Values: map[string]float64{"disk.read_bytes": activity * 5, "disk.write_bytes": float64(k) * 300000, "disk.read_ops": 250, "disk.write_ops": 50}})
				}
				b.Frames = append(b.Frames, f)
			}
			if chunk == 3 {
				b.Events = []protocol.Event{{ID: "demo-start-" + host, EntityID: "container-0", At: b.Frames[0].At, Type: "start", Message: "Frigate started a recording workload (synthetic example)."}}
			}
			body, _ := json.Marshal(b)
			if _, e := s.Ingest(ctx, b, body); e != nil {
				return e
			}
		}
	}
	return nil
}

// AdvanceDemo continues synthetic telemetry so the preview also exercises polling.
func AdvanceDemo(ctx context.Context, s *storage.Store) error {
	hosts, err := s.Hosts(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(5 * time.Second)
	for _, host := range hosts {
		if !strings.HasPrefix(host.ID, "demo-") {
			continue
		}
		if host.LastSource != nil && !now.After(*host.LastSource) {
			continue
		}
		entities, e := s.Entities(ctx, host.ID, "")
		if e != nil {
			return e
		}
		var sequence int64
		if e = s.DB.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0)+1 FROM ingest_batches WHERE host_id=? AND epoch='demo-epoch'", host.ID).Scan(&sequence); e != nil {
			return e
		}
		f := protocol.Frame{At: now, Interval: 5, Samples: []protocol.Sample{}}
		for _, entity := range entities {
			var payload string
			if e = s.DB.QueryRowContext(ctx, "SELECT payload FROM metric_samples WHERE host_id=? AND entity_id=? ORDER BY at DESC LIMIT 1", host.ID, entity.ID).Scan(&payload); e != nil {
				continue
			}
			var values map[string]float64
			if e = json.Unmarshal([]byte(payload), &values); e != nil {
				return e
			}
			if entity.Kind == "host" {
				values["cpu.busy_pct"] = 18 + 6*math.Sin(float64(now.Unix())/37)
				values["uptime.seconds"] += 5
			}
			f.Samples = append(f.Samples, protocol.Sample{EntityID: entity.ID, Generation: entity.Generation, Values: values})
		}
		for key, capability := range host.Capabilities {
			capability.At = now
			host.Capabilities[key] = capability
		}
		b := protocol.Batch{Protocol: 1, HostID: host.ID, AgentVersion: "0.1.0-demo", BootID: "demo-boot", Epoch: "demo-epoch", Sequence: fmt.Sprint(sequence), SentAt: now, Sampling: 5, Entities: entities, Frames: []protocol.Frame{f}, Capabilities: host.Capabilities}
		body, _ := json.Marshal(b)
		if _, e = s.Ingest(ctx, b, body); e != nil {
			return e
		}
	}
	return nil
}
