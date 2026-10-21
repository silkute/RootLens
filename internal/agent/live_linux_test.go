//go:build linux

package agent

import (
	"os"
	"testing"
	"time"
)

func TestLiveLinuxCollector(t *testing.T) {
	if os.Getenv("ROOTLENS_LIVE") != "1" {
		t.Skip("set ROOTLENS_LIVE=1 in an isolated Linux test environment")
	}
	c := NewCollector(Config{Sampling: 5, CollectProcesses: true})
	c.Collect(time.Now())
	time.Sleep(time.Second)
	f, entities, caps, _ := c.Collect(time.Now())
	if len(entities) < 1 || caps["cpu"].Status != "available" || caps["memory"].Status != "available" {
		t.Fatalf("host collectors unavailable: %+v", caps)
	}
	found := false
	for _, sample := range f.Samples {
		if sample.EntityID == "host" {
			if value, ok := sample.Values["cpu.busy_pct"]; !ok || value < 0 || value > 100 {
				t.Fatal("CPU metric invalid")
			}
			if sample.Values["memory.total_bytes"] <= 0 {
				t.Fatal("memory unavailable")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("host sample missing")
	}
	if c.ProcessSnapshot == nil || len(c.ProcessSnapshot.Processes) == 0 {
		t.Fatal("enabled live process summary is empty")
	}
	t.Logf("Collected %d entities with %d capabilities; Linux container environment only", len(entities), len(caps))
}
