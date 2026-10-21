package diagnostics

import (
	"rootlens/internal/protocol"
	"testing"
	"time"
)

func TestDisconnectedOOMIncidentCannotResolve(t *testing.T) {
	now := time.Now()
	st := NewState()
	Evaluate(st, "host", protocol.Frame{At: now, Interval: 5}, nil, false)
	ObserveEvents(st, "host", []protocol.Event{{At: now, EntityID: "container", Type: "oom"}}, false)
	out := Tick(st, now.Add(11*time.Minute))
	if len(out) != 1 || out[0].State != "interrupted" {
		t.Fatalf("missing telemetry falsely resolved OOM: %+v", out)
	}
}
