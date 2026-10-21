package agent

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"rootlens/internal/protocol"
	"sort"
	"strconv"
	"strings"
	"time"
)

type processCounter struct {
	Start string
	Ticks uint64
}

func parseProcessStat(data string) (string, processCounter, float64, error) {
	left, right := strings.Index(data, "("), strings.LastIndex(data, ")")
	if left < 0 || right <= left {
		return "", processCounter{}, 0, fmt.Errorf("invalid_process_stat")
	}
	name := data[left+1 : right]
	if len(name) > 64 {
		name = name[:64]
	}
	fields := strings.Fields(data[right+1:])
	if len(fields) < 22 {
		return "", processCounter{}, 0, fmt.Errorf("short_process_stat")
	}
	user, e1 := strconv.ParseUint(fields[11], 10, 64)
	system, e2 := strconv.ParseUint(fields[12], 10, 64)
	pages, e3 := strconv.ParseUint(fields[21], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || user+system < user {
		return "", processCounter{}, 0, fmt.Errorf("invalid_process_counter")
	}
	return name, processCounter{Start: fields[19], Ticks: user + system}, float64(pages) * float64(os.Getpagesize()), nil
}
func (c *Collector) processes(now time.Time, caps map[string]protocol.Capability) {
	if !c.Config.CollectProcesses {
		caps["processes"] = protocol.Capability{Status: "unsupported", Reason: "Process summaries are opt-in. Enable collect_processes in the local agent configuration.", At: now}
		return
	}
	if c.ProcessSnapshot != nil && now.Sub(c.processAt) < 15*time.Second {
		caps["processes"] = protocol.Capability{Status: "available", Reason: "Current process names, CPU and memory only; no command lines or history.", At: c.ProcessSnapshot.At}
		return
	}
	entries, e := os.ReadDir(c.Config.Proc)
	if e != nil {
		caps["processes"] = status(e, now)
		return
	}
	// AMD64/ARM64 Linux auxv entries contain the kernel's accounting clock rate.
	tickRate := 0.0
	if aux, e := os.ReadFile(filepath.Join(c.Config.Proc, "self/auxv")); e == nil {
		for i := 0; i+16 <= len(aux); i += 16 {
			if binary.LittleEndian.Uint64(aux[i:]) == 17 {
				tickRate = float64(binary.LittleEndian.Uint64(aux[i+8:]))
				break
			}
		}
	}
	previous := c.processPrevious
	next := map[int]processCounter{}
	samples := []protocol.Process{}
	limited := false
	started := time.Now()
	elapsed := now.Sub(c.processAt).Seconds()
	for _, entry := range entries {
		pid, e := strconv.Atoi(entry.Name())
		if e != nil || pid <= 0 || !entry.IsDir() {
			continue
		}
		if len(next) >= 10000 || time.Since(started) > 250*time.Millisecond {
			limited = true
			break
		}
		text, e := read(filepath.Join(c.Config.Proc, entry.Name(), "stat"))
		if e != nil {
			limited = true
			continue
		}
		name, counter, rss, e := parseProcessStat(text)
		if e != nil {
			limited = true
			continue
		}
		process := protocol.Process{PID: pid, Name: name, RSSBytes: rss}
		if old, ok := previous[pid]; ok && old.Start == counter.Start && tickRate > 0 && elapsed > 0 && elapsed < 60 {
			if delta, valid := Delta(old.Ticks, counter.Ticks); valid {
				value := float64(delta) / tickRate / elapsed * 100
				process.CPUPercent = &value
			}
		}
		next[pid] = counter
		samples = append(samples, process)
	}
	leaders := map[int]protocol.Process{}
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].RSSBytes == samples[j].RSSBytes {
			return samples[i].PID < samples[j].PID
		}
		return samples[i].RSSBytes > samples[j].RSSBytes
	})
	for i, p := range samples {
		if i == 10 {
			break
		}
		leaders[p.PID] = p
	}
	cpu := func(p protocol.Process) float64 {
		if p.CPUPercent == nil {
			return -1
		}
		return *p.CPUPercent
	}
	sort.Slice(samples, func(i, j int) bool {
		if cpu(samples[i]) == cpu(samples[j]) {
			return samples[i].PID < samples[j].PID
		}
		return cpu(samples[i]) > cpu(samples[j])
	})
	for i, p := range samples {
		if i == 10 {
			break
		}
		leaders[p.PID] = p
	}
	out := []protocol.Process{}
	for _, p := range leaders {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if cpu(out[i]) == cpu(out[j]) {
			return out[i].RSSBytes > out[j].RSSBytes
		}
		return cpu(out[i]) > cpu(out[j])
	})
	c.ProcessSnapshot = &protocol.ProcessSnapshot{At: now.UTC(), Processes: out, Visible: len(samples), Limited: limited}
	c.processPrevious = next
	c.processAt = now
	reason := "Current leaders among visible processes; command lines and history are not collected."
	if limited {
		reason = "Summary is incomplete due to permissions, exited processes, or the collection budget."
	}
	caps["processes"] = protocol.Capability{Status: "available", Reason: reason, At: now}
}
