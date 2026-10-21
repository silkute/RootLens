//go:build linux

package agent

import (
	"fmt"
	"golang.org/x/sys/unix"
	"rootlens/internal/protocol"
	"time"
)

type filesystemResult struct {
	total, used float64
	err         error
}
type filesystemWorker struct {
	result  chan filesystemResult
	last    time.Time
	value   filesystemResult
	pending bool
}

func (c *Collector) filesystems(f *protocol.Frame, entities *[]protocol.Entity, caps map[string]protocol.Capability, now time.Time) {
	if len(c.Config.Filesystems) == 0 {
		caps["filesystem"] = protocol.Capability{Status: "unsupported", Reason: "Select local filesystem paths in the agent configuration.", At: now}
		return
	}
	for i, path := range c.Config.Filesystems {
		if i >= 32 {
			break
		}
		w := c.fsWorkers[path]
		if w == nil {
			w = &filesystemWorker{result: make(chan filesystemResult, 1)}
			c.fsWorkers[path] = w
		}
		if w.pending {
			select {
			case result := <-w.result:
				w.value = result
				w.pending = false
				w.last = now
			default:
			}
		}
		if !w.pending && (w.last.IsZero() || now.Sub(w.last) >= 60*time.Second) {
			w.pending = true
			go func(ch chan filesystemResult, p string) {
				var s unix.Statfs_t
				e := unix.Statfs(p, &s)
				if e == nil && (s.Type == unix.NFS_SUPER_MAGIC || s.Type == unix.CIFS_SUPER_MAGIC) {
					e = fmt.Errorf("network_filesystem_unsupported")
				}
				ch <- filesystemResult{float64(s.Blocks) * float64(s.Bsize), float64(s.Blocks-s.Bfree) * float64(s.Bsize), e}
			}(w.result, path)
		}
		key := fmt.Sprintf("filesystem.%d", i)
		if w.last.IsZero() {
			caps[key] = protocol.Capability{Status: "timeout", Reason: "Waiting for the bounded filesystem worker.", At: now}
			continue
		}
		caps[key] = status(w.value.err, w.last)
		if w.value.err != nil || now.Sub(w.last) > 120*time.Second {
			continue
		}
		id := fmt.Sprintf("fs:%d", i)
		*entities = append(*entities, protocol.Entity{ID: id, Kind: "filesystem", Name: path, Generation: c.Boot})
		v := map[string]float64{"fs.total_bytes": w.value.total, "fs.used_bytes": w.value.used}
		if w.value.total > 0 {
			v["fs.used_pct"] = w.value.used / w.value.total * 100
		}
		f.Samples = append(f.Samples, protocol.Sample{EntityID: id, Generation: c.Boot, Values: v})
	}
}
