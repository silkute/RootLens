package diagnostics

import (
	"fmt"
	"rootlens/internal/protocol"
	"testing"
	"time"
)

func TestEachSampledRuleAtSupportedCadences(t *testing.T) {
	for _, rule := range Rules {
		for _, interval := range []int{5, 10, 15} {
			t.Run(fmt.Sprintf("%s/%ds", rule.ID, interval), func(t *testing.T) {
				st := NewState()
				start := time.Now()
				kind := "host"
				if rule.ID == "cpu-throttling" {
					kind = "container"
				}
				kinds := map[string]string{"subject": kind}
				feed := func(seconds int, value float64) []protocol.Incident {
					return Evaluate(st, "h", protocol.Frame{At: start.Add(time.Duration(seconds) * time.Second), Interval: float64(interval), Samples: []protocol.Sample{{EntityID: "subject", Generation: "g", Values: map[string]float64{rule.Metric: value, "cpu.periods": 50}}}}, kinds, false)
				}
				for seconds := interval; seconds < 60; seconds += interval {
					if len(feed(seconds, rule.Enter+10)) > 0 {
						t.Fatal("rule fired before its full window")
					}
				}
				opened := feed(60, rule.Enter+10)
				if len(opened) != 1 || opened[0].State != "open" {
					t.Fatalf("expected open: %+v", opened)
				}
				for seconds := 60 + interval; seconds < 180; seconds += interval {
					for _, inc := range feed(seconds, 0) {
						if inc.State == "resolved" {
							t.Fatal("premature recovery")
						}
					}
				}
				resolved := feed(180, 0)
				if len(resolved) != 1 || resolved[0].State != "resolved" {
					t.Fatalf("expected recovery: %+v", resolved)
				}
				if resolved[0].Evidence[0].Value < rule.Enter {
					t.Fatal("recovery erased peak evidence")
				}
			})
		}
	}
}
func TestAncestorThrottlingIsDistinct(t *testing.T) {
	st := NewState()
	start := time.Now()
	var out []protocol.Incident
	for i := 1; i <= 12; i++ {
		out = Evaluate(st, "h", protocol.Frame{At: start.Add(time.Duration(i) * 5 * time.Second), Interval: 5, Samples: []protocol.Sample{{EntityID: "scope:x", Generation: "boot", Values: map[string]float64{"cpu.throttled_pct": 50, "cpu.periods": 50}}}}, map[string]string{"scope:x": "cgroup"}, false)
	}
	if len(out) != 1 || out[0].Title != "Ancestor cgroup CPU throttling" {
		t.Fatal("ancestor and container scopes conflated")
	}
}
