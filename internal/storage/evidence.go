package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"rootlens/internal/protocol"
	"sort"
	"time"
)

// Device evidence describes resource contention, never a responsible workload.
func deviceEvidence(tx *sql.Tx, inc protocol.Incident) ([]protocol.Evidence, error) {
	rows, e := tx.Query(`SELECT e.id,e.name,m.at,m.interval,m.payload FROM metric_samples m JOIN entities e ON e.host_id=m.host_id AND e.id=m.entity_id WHERE m.host_id=? AND e.kind='device' AND m.at>=? AND m.at<=? ORDER BY m.at`, inc.HostID, Stamp(inc.Start.Add(-10*time.Minute)), Stamp(inc.Updated))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	type activity struct {
		name                                                                                string
		beforeOps, beforeMS, beforeSeconds, currentOps, currentMS, currentSeconds, queueSum float64
	}
	items := map[string]*activity{}
	for rows.Next() {
		var id, name, at, payload string
		var interval float64
		if e = rows.Scan(&id, &name, &at, &interval, &payload); e != nil {
			return nil, e
		}
		var v map[string]float64
		if e = json.Unmarshal([]byte(payload), &v); e != nil {
			return nil, e
		}
		a := items[id]
		if a == nil {
			a = &activity{name: name}
			items[id] = a
		}
		ops := v["disk.read_ops"] + v["disk.write_ops"]
		ms := v["disk.read_ms"] + v["disk.write_ms"]
		if ParseTime(at).Before(inc.Start) {
			a.beforeOps += ops
			a.beforeMS += ms
			a.beforeSeconds += interval
		} else {
			a.currentOps += ops
			a.currentMS += ms
			a.currentSeconds += interval
			a.queueSum += v["disk.queue"] * interval
		}
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	keys := []string{}
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := []protocol.Evidence{}
	for _, key := range keys {
		a := items[key]
		if a.beforeSeconds < 300 || a.currentSeconds < 48 || a.beforeOps <= 0 || a.currentOps <= 0 {
			continue
		}
		baseline := a.beforeMS / a.beforeOps
		current := a.currentMS / a.currentOps
		queue := a.queueSum / a.currentSeconds
		if current >= math.Max(5, 2*baseline) && queue >= 1 {
			out = append(out, protocol.Evidence{Level: "strongly_supported_inference", Text: fmt.Sprintf("Storage contention is supported by measured I/O stalls and %s request time increasing from %.1f to %.1f ms, with %.1f average outstanding requests. Device activity does not identify a root cause.", a.name, baseline, current, queue), Metric: "disk.await_ms", Value: current, Coverage: math.Min(1, a.currentSeconds/math.Max(1, inc.Updated.Sub(inc.Start).Seconds()))})
			if len(out) == 3 {
				break
			}
		}
	}
	return out, nil
}
