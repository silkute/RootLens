package protocol

import (
	"math"
	"testing"
	"time"
)

func TestValidationRejectsUnboundedAndUnknownInput(t *testing.T) {
	now := time.Now()
	b := Batch{Protocol: 1, HostID: "h", BootID: "b", Epoch: "e", Sequence: "1", Sampling: 5, Entities: []Entity{{ID: "host", Kind: "host", Generation: "g"}}, Frames: []Frame{{At: now, Interval: 5, Samples: []Sample{{EntityID: "host", Generation: "g", Values: map[string]float64{"cpu.busy_pct": 10}}}}}}
	if e := b.Validate(now); e != nil {
		t.Fatal(e)
	}
	b.Frames[0].Samples[0].Values["secret.path"] = 1
	if e := b.Validate(now); e == nil {
		t.Fatal("unknown metric accepted")
	}
	delete(b.Frames[0].Samples[0].Values, "secret.path")
	b.Frames[0].Samples[0].Values["cpu.busy_pct"] = math.NaN()
	if e := b.Validate(now); e == nil {
		t.Fatal("NaN accepted")
	}
}
