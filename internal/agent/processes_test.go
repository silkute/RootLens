package agent

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"rootlens/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestProcessParserAndOptIn(t *testing.T) {
	fields := make([]string, 22)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = "R"
	fields[11] = "10"
	fields[12] = "5"
	fields[19] = "42"
	fields[21] = "4"
	stat := "12 (worker (job)) " + strings.Join(fields, " ")
	name, counter, rss, e := parseProcessStat(stat)
	if e != nil || name != "worker (job)" || counter.Ticks != 15 || counter.Start != "42" || rss != float64(4*os.Getpagesize()) {
		t.Fatalf("process parsing: %q %+v %f %v", name, counter, rss, e)
	}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "12"), 0700)
	os.WriteFile(filepath.Join(root, "12/stat"), []byte(stat), 0600)
	c := NewCollector(Config{Proc: root})
	caps := map[string]protocol.Capability{}
	c.processes(time.Now(), caps)
	if c.ProcessSnapshot != nil || caps["processes"].Status != "unsupported" {
		t.Fatal("process collection must be opt-in")
	}
	c.Config.CollectProcesses = true
	when := time.Now()
	c.processes(when, caps)
	if c.ProcessSnapshot == nil || len(c.ProcessSnapshot.Processes) != 1 {
		t.Fatal("enabled summary missing")
	}
	if c.ProcessSnapshot.Processes[0].CPUPercent != nil {
		t.Fatal("initial baseline cannot claim CPU rate")
	}
	if fmt.Sprint(c.ProcessSnapshot.Processes[0].PID) != "12" {
		t.Fatal("PID identity")
	}
	os.MkdirAll(filepath.Join(root, "self"), 0700)
	aux := make([]byte, 32)
	binary.LittleEndian.PutUint64(aux, 17)
	binary.LittleEndian.PutUint64(aux[8:], 100)
	if e = os.WriteFile(filepath.Join(root, "self/auxv"), aux, 0600); e != nil {
		t.Fatal(e)
	}
	fields[11] = "40"
	os.WriteFile(filepath.Join(root, "12/stat"), []byte("12 (worker (job)) "+strings.Join(fields, " ")), 0600)
	c.processes(when.Add(15*time.Second), caps)
	percent := c.ProcessSnapshot.Processes[0].CPUPercent
	if percent == nil || *percent != 2 {
		t.Fatalf("CPU tick denominator incorrect: %v", percent)
	}
	fields[19] = "99"
	fields[11] = "9999"
	os.WriteFile(filepath.Join(root, "12/stat"), []byte("12 (new worker) "+strings.Join(fields, " ")), 0600)
	c.processes(when.Add(30*time.Second), caps)
	if c.ProcessSnapshot.Processes[0].CPUPercent != nil {
		t.Fatal("reused PID inherited the previous process CPU baseline")
	}
}
