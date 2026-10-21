package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"rootlens/internal/protocol"
	"strconv"
	"strings"
)

// ancestors reports each shared scope once; its counters are never summed into a
// descendant's own quota counters. Only paths inside the validated hierarchy qualify.
func (d *Docker) ancestors(cgroup string, interval float64, seen map[string]bool, entities *[]protocol.Entity, samples *[]protocol.Sample) []string {
	root, e := filepath.EvalSymlinks(d.config.Cgroup)
	if e != nil {
		return nil
	}
	ids := []string{}
	for current, depth := filepath.Dir(cgroup), 0; current != root && depth < 8; current, depth = filepath.Dir(current), depth+1 {
		rel, e := filepath.Rel(root, current)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.Dir(current) == current {
			break
		}
		hash := sha256.Sum256([]byte(rel))
		id := "scope:" + hex.EncodeToString(hash[:12])
		text, e := read(filepath.Join(current, "cpu.max"))
		if e != nil {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) != 2 || fields[0] == "max" {
			continue
		}
		quota, e1 := strconv.ParseFloat(fields[0], 64)
		period, e2 := strconv.ParseFloat(fields[1], 64)
		if e1 != nil || e2 != nil || period <= 0 {
			continue
		}
		ids = append(ids, id)
		if seen[id] {
			continue
		}
		seen[id] = true
		values := map[string]float64{"cpu.quota_cores": quota / period}
		if text, e = read(filepath.Join(current, "cpu.stat")); e == nil {
			if raw, e := ParseKV(text); e == nil {
				if delta, ok := d.collector.delta("ancestor:"+id, raw); ok {
					values["cpu.cores"] = delta["usage_usec"] / (interval * 1e6)
					if observed, exists := delta["nr_periods"]; exists {
						values["cpu.periods"] = observed
						values["cpu.throttled_periods"] = delta["nr_throttled"]
						if observed > 0 {
							values["cpu.throttled_pct"] = 100 * delta["nr_throttled"] / observed
						}
					}
				}
			}
		}
		*entities = append(*entities, protocol.Entity{ID: id, Kind: "cgroup", Name: "Ancestor " + filepath.Base(current), Generation: d.collector.Boot, Metadata: map[string]string{"scope": "ancestor", "counter_scope": "own bandwidth limit"}})
		*samples = append(*samples, protocol.Sample{EntityID: id, Generation: d.collector.Boot, Values: values})
	}
	return ids
}
