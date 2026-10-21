package diagnostics

import (
	"rootlens/internal/protocol"
	"testing"
	"time"
)

func frame(at time.Time, value float64) protocol.Frame {
	return protocol.Frame{At: at, Interval: 5, Samples: []protocol.Sample{{EntityID: "host", Generation: "boot", Values: map[string]float64{"io.some_pct": value}}}}
}
func TestPressureRequiresSustainedEvidenceAndMeasuredRecovery(t *testing.T) {
	st := NewState()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	kinds := map[string]string{"host": "host"}
	var found []protocol.Incident
	for i := 1; i <= 12; i++ {
		got := Evaluate(st, "h", frame(start.Add(time.Duration(i)*5*time.Second), 25), kinds, false)
		if i < 10 && len(got) > 0 {
			t.Fatal("short burst created an incident")
		}
		found = append(found, got...)
	}
	if len(found) == 0 {
		t.Fatal("sustained pressure not detected")
	}
	if found[0].Evidence[0].Level != "measured_fact" {
		t.Fatal("unexpected inference")
	}
	for i := 13; i < 36; i++ {
		for _, inc := range Evaluate(st, "h", frame(start.Add(time.Duration(i)*5*time.Second), 0), kinds, false) {
			if inc.State == "resolved" {
				t.Fatal("premature recovery")
			}
		}
	}
	got := Evaluate(st, "h", frame(start.Add(180*time.Second), 0), kinds, false)
	if len(got) != 1 || got[0].State != "resolved" {
		t.Fatalf("expected recovery, got %+v", got)
	}
}
func TestMissingMetricsInterruptRatherThanResolve(t *testing.T) {
	st := NewState()
	start := time.Now()
	for i := 0; i < 12; i++ {
		Evaluate(st, "h", frame(start.Add(time.Duration(i)*5*time.Second), 22), map[string]string{"host": "host"}, false)
	}
	got := Tick(st, start.Add(120*time.Second))
	if len(got) != 1 || got[0].State != "interrupted" {
		t.Fatalf("missing data must interrupt: %+v", got)
	}
}
func TestHighUtilizationWithoutPressureDoesNotDiagnoseSaturation(t *testing.T) {
	st := NewState()
	for i := 0; i < 30; i++ {
		f := frame(time.Now().Add(time.Duration(i)*5*time.Second), 0)
		f.Samples[0].Values["cpu.busy_pct"] = 100
		f.Samples[0].Values["disk.busy_pct"] = 100
		if got := Evaluate(st, "h", f, map[string]string{"host": "host"}, false); len(got) > 0 {
			t.Fatal("utilization alone was diagnosed")
		}
	}
}
func TestCounterGenerationInterrupts(t *testing.T) {
	st := NewState()
	start := time.Now()
	for i := 0; i < 12; i++ {
		Evaluate(st, "h", frame(start.Add(time.Duration(i)*5*time.Second), 20), map[string]string{"host": "host"}, false)
	}
	f := frame(start.Add(60*time.Second), 20)
	f.Samples[0].Generation = "new-run"
	got := Evaluate(st, "h", f, map[string]string{"host": "host"}, false)
	if len(got) != 1 || got[0].State != "interrupted" {
		t.Fatalf("generation transition: %+v", got)
	}
}
func TestRestartIsNotOOM(t *testing.T) {
	st := NewState()
	start := time.Now()
	events := []protocol.Event{}
	for i := 0; i < 3; i++ {
		at := start.Add(time.Duration(i) * time.Minute)
		events = append(events, protocol.Event{EntityID: "c", Type: "die", At: at}, protocol.Event{EntityID: "c", Type: "start", At: at.Add(time.Second)})
	}
	got := ObserveEvents(st, "h", events, false)
	if len(got) != 1 || got[0].Title != "Container restart instability" {
		t.Fatalf("restart evidence: %+v", got)
	}
	got = ObserveEvents(st, "h", []protocol.Event{{EntityID: "c", Type: "oom", At: start.Add(4 * time.Minute)}}, false)
	if len(got) != 1 || got[0].Title != "Observed OOM evidence" {
		t.Fatal("OOM was not recorded")
	}
}
func TestThrottlingRequiresObservedPeriods(t *testing.T) {
	st := NewState()
	for i := 0; i < 20; i++ {
		f := frame(time.Now().Add(time.Duration(i)*5*time.Second), 0)
		f.Samples[0].Values = map[string]float64{"cpu.throttled_pct": 99, "cpu.periods": 0}
		if len(Evaluate(st, "h", f, map[string]string{"host": "container"}, false)) != 0 {
			t.Fatal("zero periods must not trigger")
		}
	}
}
func TestAttributionRequiresMappingAndBaseline(t *testing.T) {
	a := Activity{Entity: protocol.Entity{ID: "io", Name: "backup", Metadata: map[string]string{"device": "8:0", "container_id": "c", "mapping": "unverified"}}, Current: []float64{200}, Baseline: []float64{100}, BaselineSeconds: 600, CurrentSeconds: 60}
	if len(Rank([]Activity{a}, 60)) != 0 {
		t.Fatal("unmapped workload attributed")
	}
	a.Entity.Metadata["mapping"] = "validated"
	a.BaselineSeconds = 20
	got := Rank([]Activity{a}, 60)
	if len(got) != 1 || got[0].Label != "Active workload; insufficient baseline" {
		t.Fatal("insufficient baseline overstated")
	}
	a.BaselineSeconds = 600
	got = Rank([]Activity{a}, 60)
	if got[0].Label != "Possible contributor" || got[0].Share != 100 {
		t.Fatal("valid observed share missing")
	}
}
