// Package diagnostics contains deterministic, replayable evidence rules.
package diagnostics

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"rootlens/internal/protocol"
	"sort"
	"time"
)

type Rule struct {
	ID, Title, Resource, Metric string
	Enter, Exit                 float64
	Steps                       []string
}

var Rules = []Rule{
	{"io-pressure", "Sustained I/O pressure", "storage", "io.some_pct", 10, 5, []string{"Compare device request time and queue occupancy around this interval.", "Check overlapping backup, recording, indexing and scan schedules."}},
	{"cpu-pressure", "CPU contention", "cpu", "cpu.some_pct", 10, 5, []string{"Compare per-core execution and steal time.", "Inspect competing workloads and CPU affinity or quotas."}},
	{"memory-pressure", "Sustained memory pressure", "memory", "memory.some_pct", 5, 2, []string{"Compare available memory, swap activity and OOM evidence.", "Review workload memory limits and changes before the incident."}},
	{"cpu-throttling", "Container CPU throttling", "cpu", "cpu.throttled_pct", 20, 5, []string{"Inspect this container's quota and any ancestor cgroup constraints.", "Compare workload demand with its configured CPU allocation."}},
}

type Point struct {
	At                       time.Time `json:"at"`
	Duration, Value, Periods float64
	Generation               string
}
type Stream struct {
	Points       []Point
	WindowStart  time.Time
	Peak         float64
	PeakCoverage float64
	Last         time.Time
	Generation   string
	Incident     *protocol.Incident
	Recovery     *time.Time
	PreviousID   string
	PreviousEnd  time.Time
}
type EventWindow struct {
	Starts       []time.Time
	LastDie      time.Time
	Incident     *protocol.Incident
	LastCoverage time.Time
}
type State struct {
	Streams   map[string]*Stream      `json:"streams"`
	Events    map[string]*EventWindow `json:"events"`
	LastFrame time.Time               `json:"last_frame"`
}

func NewState() *State {
	return &State{Streams: map[string]*Stream{}, Events: map[string]*EventWindow{}}
}

func InterruptAll(st *State, at time.Time) []protocol.Incident {
	out := []protocol.Incident{}
	for _, stream := range st.Streams {
		if stream.Incident != nil {
			end := stream.Last
			stream.Incident.State = "interrupted"
			stream.Incident.End = &end
			stream.Incident.Updated = at
			out = append(out, *stream.Incident)
			stream.Incident = nil
		}
	}
	for _, window := range st.Events {
		if window.Incident != nil {
			end := st.LastFrame
			window.Incident.State = "interrupted"
			window.Incident.End = &end
			window.Incident.Updated = at
			out = append(out, *window.Incident)
			window.Incident = nil
		}
	}
	return out
}
func stableID(host, entity, rule string, at time.Time) string {
	h := sha256.Sum256([]byte(host + "|" + entity + "|" + rule + "|" + at.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(h[:12])
}
func newIncident(host, entity string, r Rule, at time.Time, historical bool) *protocol.Incident {
	return &protocol.Incident{ID: stableID(host, entity, r.ID, at), HostID: host, EntityID: entity, Rule: r.ID, RuleVersion: 1, Title: r.Title, Resource: r.Resource, State: "open", Severity: "warning", Start: at, Updated: at, Evidence: []protocol.Evidence{}, Contributors: []protocol.Contributor{}, Steps: r.Steps, Limitations: []string{"Infrastructure measurements do not prove application latency or identify a root cause on their own."}, Historical: historical}
}
func Evaluate(st *State, host string, f protocol.Frame, kinds map[string]string, historical bool) []protocol.Incident {
	if f.At.After(st.LastFrame) {
		st.LastFrame = f.At
	}
	if st.Streams == nil {
		st.Streams = map[string]*Stream{}
	}
	if st.Events == nil {
		st.Events = map[string]*EventWindow{}
	}
	out := []protocol.Incident{}
	for _, sample := range f.Samples {
		if _, available := sample.Values["memory.oom_kills"]; available {
			if window := st.Events[sample.EntityID]; window != nil && window.Incident != nil && window.Incident.Title == "Observed OOM evidence" {
				window.LastCoverage = f.At
			}
		}
		for _, r := range Rules {
			kind := kinds[sample.EntityID]
			if (r.ID == "cpu-throttling" && kind != "container" && kind != "cgroup") || (r.ID != "cpu-throttling" && kind != "host") {
				continue
			}
			if r.ID == "cpu-throttling" && kind == "cgroup" {
				r.Title = "Ancestor cgroup CPU throttling"
				r.Steps = []string{"Inspect the ancestor CPU quota and workloads sharing this cgroup.", "Do not sum ancestor and child throttling counters or assume all descendants were equally affected."}
			}
			value, ok := sample.Values[r.Metric]
			if !ok {
				continue
			}
			key := sample.EntityID + "/" + r.ID
			s := st.Streams[key]
			if s == nil {
				s = &Stream{}
				st.Streams[key] = s
			}
			if !s.Last.IsZero() && !f.At.After(s.Last) {
				continue
			}
			gap := !s.Last.IsZero() && f.At.Sub(s.Last).Seconds() > 2*f.Interval+0.1
			reset := s.Generation != "" && s.Generation != sample.Generation
			if gap || reset {
				if s.Incident != nil && (reset || f.At.Sub(s.Last) > 60*time.Second) {
					end := s.Last
					s.Incident.State = "interrupted"
					s.Incident.End = &end
					s.Incident.Updated = f.At
					out = append(out, *s.Incident)
					s.PreviousID = s.Incident.ID
					s.PreviousEnd = end
					s.Incident = nil
				}
				s.Points = nil
				s.WindowStart = time.Time{}
				s.Recovery = nil
			}
			s.Last = f.At
			s.Generation = sample.Generation
			if s.WindowStart.IsZero() {
				s.WindowStart = f.At.Add(-time.Duration(f.Interval * float64(time.Second)))
			}
			s.Points = append(s.Points, Point{f.At, f.Interval, value, sample.Values["cpu.periods"], sample.Generation})
			cut := f.At.Add(-60 * time.Second)
			n := 0
			for _, p := range s.Points {
				if p.At.After(cut) {
					s.Points[n] = p
					n++
				}
			}
			s.Points = s.Points[:n]
			coverage, qualifying, sum, periods := 0.0, 0.0, 0.0, 0.0
			for _, p := range s.Points {
				d := math.Min(p.Duration, p.At.Sub(cut).Seconds())
				coverage += d
				sum += p.Value * d
				periods += p.Periods * d / p.Duration
				if p.Value >= r.Enter {
					qualifying += d
				}
			}
			if s.Incident == nil && f.At.Sub(s.WindowStart) >= 60*time.Second && coverage >= 48 && qualifying >= 48 && (r.ID != "cpu-throttling" || periods >= 20) {
				start := s.Points[0].At.Add(-time.Duration(s.Points[0].Duration * float64(time.Second)))
				s.Incident = newIncident(host, sample.EntityID, r, start, historical)
				s.Peak = 0
				if start.Sub(s.PreviousEnd) <= 5*time.Minute {
					s.Incident.PreviousID = s.PreviousID
				}
			}
			if s.Incident != nil {
				s.Incident.Updated = f.At
				avg := sum / math.Max(coverage, 1)
				if r.ID == "cpu-throttling" && periods > 0 {
					weighted := 0.0
					for _, p := range s.Points {
						d := math.Min(p.Duration, p.At.Sub(cut).Seconds())
						weighted += p.Value * p.Periods * d / p.Duration
					}
					avg = weighted / periods
				}
				if avg > s.Peak {
					s.Peak = avg
					s.PeakCoverage = math.Min(coverage/60, 1)
				}
				text := fmt.Sprintf("%s reached a measured 60-second average of %.1f%% during this incident.", r.Title, s.Peak)
				if r.ID == "cpu-throttling" {
					text = fmt.Sprintf("The throttled-period fraction reached %.1f%% in a measured window; this is not a percentage of wall time lost.", s.Peak)
				}
				s.Incident.Evidence = []protocol.Evidence{{Level: "measured_fact", Text: text, Metric: r.Metric, Value: s.Peak, Threshold: r.Enter, Coverage: s.PeakCoverage}}
				if value < r.Exit {
					if s.Recovery == nil {
						t := f.At.Add(-time.Duration(f.Interval * float64(time.Second)))
						s.Recovery = &t
					}
					if f.At.Sub(*s.Recovery) >= 120*time.Second {
						t := f.At
						s.Incident.State = "resolved"
						s.Incident.End = &t
					}
				} else {
					s.Recovery = nil
				}
				out = append(out, *s.Incident)
				if s.Incident.State == "resolved" {
					s.PreviousID = s.Incident.ID
					s.PreviousEnd = f.At
					s.Incident = nil
					s.Points = nil
					s.Recovery = nil
				}
			}
		}
		if sample.Values["memory.oom_kills"] > 0 {
			out = append(out, ObserveEvents(st, host, []protocol.Event{{ID: "counter", EntityID: sample.EntityID, At: f.At, Type: "oom_kill", Message: "An OOM kill counter increased."}}, historical)...)
		}
	}
	return out
}
func ObserveEvents(st *State, host string, events []protocol.Event, historical bool) []protocol.Incident {
	if st.Events == nil {
		st.Events = map[string]*EventWindow{}
	}
	out := []protocol.Incident{}
	for _, e := range events {
		if e.Type != "die" && e.Type != "start" && e.Type != "oom" && e.Type != "oom_kill" {
			continue
		}
		w := st.Events[e.EntityID]
		if w == nil {
			w = &EventWindow{}
			st.Events[e.EntityID] = w
		}
		w.LastCoverage = e.At
		if e.Type == "die" {
			w.LastDie = e.At
			continue
		}
		n := 0
		for _, t := range w.Starts {
			if e.At.Sub(t) <= 10*time.Minute {
				w.Starts[n] = t
				n++
			}
		}
		w.Starts = w.Starts[:n]
		if e.Type == "start" && !w.LastDie.IsZero() && !e.At.Before(w.LastDie) {
			w.Starts = append(w.Starts, e.At)
			w.LastDie = time.Time{}
		}
		oom := e.Type == "oom" || e.Type == "oom_kill"
		if !oom && len(w.Starts) < 3 {
			continue
		}
		if w.Incident == nil {
			r := Rule{ID: "instability", Title: "Container restart instability", Resource: "lifecycle", Steps: []string{"Inspect the workload's own logs and resource limits.", "Compare restarts with pressure and configuration changes."}}
			w.Incident = newIncident(host, e.EntityID, r, e.At, historical)
		}
		w.Incident.Updated = e.At
		if oom {
			w.Incident.Title = "Observed OOM evidence"
			w.Incident.Severity = "critical"
			w.Incident.Evidence = []protocol.Evidence{{Level: "measured_fact", Text: "An OOM event or kill counter was observed. Other restarts are not assumed to be OOM-related."}}
		} else {
			w.Incident.Evidence = []protocol.Evidence{{Level: "measured_fact", Text: fmt.Sprintf("%d starts following observed termination occurred within ten minutes. No OOM cause is inferred.", len(w.Starts))}}
		}
		out = append(out, *w.Incident)
	}
	return out
}
func Tick(st *State, now time.Time) []protocol.Incident {
	out := []protocol.Incident{}
	for key, s := range st.Streams {
		if s.Incident != nil && now.Sub(s.Last) > 60*time.Second {
			t := s.Last
			s.Incident.State = "interrupted"
			s.Incident.End = &t
			s.Incident.Updated = now
			out = append(out, *s.Incident)
			s.PreviousID = s.Incident.ID
			s.PreviousEnd = t
			s.Incident = nil
			s.Points = nil
			s.Recovery = nil
		}
		if s.Incident == nil && now.Sub(s.Last) > 15*time.Minute {
			delete(st.Streams, key)
		}
	}
	for key, w := range st.Events {
		if w.Incident != nil && !w.LastCoverage.IsZero() && now.Sub(w.LastCoverage) > 60*time.Second {
			t := w.LastCoverage
			w.Incident.State = "interrupted"
			w.Incident.End = &t
			w.Incident.Updated = now
			out = append(out, *w.Incident)
			w.Incident = nil
		} else if w.Incident != nil && now.Sub(w.Incident.Updated) > 10*time.Minute {
			t := w.Incident.Updated.Add(10 * time.Minute)
			w.Incident.State = "resolved"
			w.Incident.End = &t
			out = append(out, *w.Incident)
			w.Incident = nil
		}
		if w.Incident == nil && (len(w.Starts) == 0 || now.Sub(w.Starts[len(w.Starts)-1]) > 15*time.Minute) && now.Sub(w.LastDie) > 15*time.Minute {
			delete(st.Events, key)
		}
	}
	return out
}

type Activity struct {
	Entity                          protocol.Entity
	Baseline, Current               []float64
	BaselineSeconds, CurrentSeconds float64
	CurrentBytes                    float64
}

func median(a []float64) float64 {
	if len(a) == 0 {
		return 0
	}
	a = append([]float64(nil), a...)
	sort.Float64s(a)
	n := len(a)
	if n%2 == 0 {
		return (a[n/2-1] + a[n/2]) / 2
	}
	return a[n/2]
}

// Rank never compares container accounting against host device totals.
func Rank(activities []Activity, duration float64) []protocol.Contributor {
	totals := map[string]float64{}
	for _, a := range activities {
		if a.Entity.Metadata["device"] != "" && a.Entity.Metadata["mapping"] == "validated" {
			amount := a.CurrentBytes
			if amount == 0 {
				amount = median(a.Current) * a.CurrentSeconds
			}
			totals[a.Entity.Metadata["device"]] += amount
		}
	}
	out := []protocol.Contributor{}
	for _, a := range activities {
		device := a.Entity.Metadata["device"]
		if device == "" || a.Entity.Metadata["mapping"] != "validated" || len(a.Current) == 0 {
			continue
		}
		current := median(a.Current)
		base := median(a.Baseline)
		if current <= 0 {
			continue
		}
		label := "Active workload; insufficient baseline"
		if a.BaselineSeconds >= 300 {
			label = "Active workload; no observed increase"
		}
		if a.BaselineSeconds >= 300 && current > base {
			label = "Possible contributor"
		}
		if a.CurrentSeconds < duration*.8 {
			label = "Active workload; incomplete coverage"
		}
		amount := a.CurrentBytes
		if amount == 0 {
			amount = current * a.CurrentSeconds
		}
		out = append(out, protocol.Contributor{EntityID: a.Entity.Metadata["container_id"], Name: a.Entity.Name, Device: device, Baseline: base, Activity: current, Share: amount / math.Max(totals[device], 1) * 100, Coverage: math.Min(1, a.CurrentSeconds/math.Max(duration, 1)), Label: label})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Activity-out[i].Baseline, out[j].Activity-out[j].Baseline
		if a != b {
			return a > b
		}
		if out[i].EntityID != out[j].EntityID {
			return out[i].EntityID < out[j].EntityID
		}
		return out[i].Device < out[j].Device
	})
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}
