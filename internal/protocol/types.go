// Package protocol defines the bounded, versioned agent and browser contracts.
package protocol

import (
	"errors"
	"math"
	"regexp"
	"time"
)

const Version = "0.1.0"
const MaxBody = 2 << 20
const MaxEntities = 512

type SetupRequest struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
}
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type CreateHostRequest struct {
	Name string `json:"name"`
}
type EnrollmentRequest struct {
	Token      string `json:"token"`
	Credential string `json:"credential"`
	HostID     string `json:"host_id"`
}
type InvestigationRequest struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type Capability struct {
	Status string    `json:"status"`
	Reason string    `json:"reason,omitempty"`
	At     time.Time `json:"at"`
}
type Entity struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Name       string            `json:"name"`
	Generation string            `json:"generation"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}
type Sample struct {
	EntityID   string             `json:"entity_id"`
	Generation string             `json:"generation"`
	Values     map[string]float64 `json:"values"`
	Quality    []string           `json:"quality,omitempty"`
}
type Frame struct {
	At       time.Time `json:"at"`
	Interval float64   `json:"interval_seconds"`
	Samples  []Sample  `json:"samples"`
}
type Event struct {
	ID       string    `json:"id"`
	EntityID string    `json:"entity_id,omitempty"`
	At       time.Time `json:"at"`
	Type     string    `json:"type"`
	Message  string    `json:"message"`
}
type Batch struct {
	Protocol     int                   `json:"protocol"`
	Minor        int                   `json:"minor"`
	AgentVersion string                `json:"agent_version"`
	HostID       string                `json:"host_id"`
	BootID       string                `json:"boot_id"`
	Epoch        string                `json:"epoch"`
	Sequence     string                `json:"sequence"`
	SentAt       time.Time             `json:"sent_at"`
	Sampling     int                   `json:"sampling_seconds"`
	Entities     []Entity              `json:"entities"`
	Frames       []Frame               `json:"frames"`
	Events       []Event               `json:"events,omitempty"`
	Capabilities map[string]Capability `json:"capabilities"`
	Processes    *ProcessSnapshot      `json:"processes,omitempty"`
}
type Process struct {
	PID        int      `json:"pid"`
	Name       string   `json:"name"`
	CPUPercent *float64 `json:"cpu_percent,omitempty"`
	RSSBytes   float64  `json:"rss_bytes"`
}
type ProcessSnapshot struct {
	At        time.Time `json:"at"`
	Processes []Process `json:"processes"`
	Visible   int       `json:"visible"`
	Limited   bool      `json:"limited"`
}
type Ack struct {
	Sequence   string    `json:"sequence"`
	ServerTime time.Time `json:"server_time"`
	Protocol   int       `json:"protocol"`
	Duplicate  bool      `json:"duplicate"`
}
type Evidence struct {
	Level     string  `json:"level"`
	Text      string  `json:"text"`
	Metric    string  `json:"metric,omitempty"`
	Value     float64 `json:"value,omitempty"`
	Threshold float64 `json:"threshold,omitempty"`
	Coverage  float64 `json:"coverage,omitempty"`
}
type Contributor struct {
	EntityID string  `json:"entity_id"`
	Name     string  `json:"name"`
	Device   string  `json:"device"`
	Baseline float64 `json:"baseline"`
	Activity float64 `json:"activity"`
	Share    float64 `json:"share"`
	Coverage float64 `json:"coverage"`
	Label    string  `json:"label"`
}
type Incident struct {
	ID           string        `json:"id"`
	HostID       string        `json:"host_id"`
	EntityID     string        `json:"entity_id"`
	Rule         string        `json:"rule"`
	RuleVersion  int           `json:"rule_version"`
	Title        string        `json:"title"`
	Resource     string        `json:"resource"`
	State        string        `json:"state"`
	Severity     string        `json:"severity"`
	Start        time.Time     `json:"start"`
	End          *time.Time    `json:"end,omitempty"`
	Updated      time.Time     `json:"updated"`
	Evidence     []Evidence    `json:"evidence"`
	Contributors []Contributor `json:"contributors"`
	Steps        []string      `json:"steps"`
	Limitations  []string      `json:"limitations"`
	PreviousID   string        `json:"previous_id,omitempty"`
	Historical   bool          `json:"historical"`
}
type Host struct {
	ID              string                `json:"id"`
	Name            string                `json:"name"`
	AgentVersion    string                `json:"agent_version"`
	BootID          string                `json:"boot_id"`
	LastSeen        *time.Time            `json:"last_seen"`
	LastSource      *time.Time            `json:"last_source"`
	Sampling        int                   `json:"sampling_seconds"`
	Capabilities    map[string]Capability `json:"capabilities"`
	Status          string                `json:"status"`
	Latest          map[string]float64    `json:"latest"`
	ActiveIncidents int                   `json:"active_incidents"`
}
type MetricPoint struct {
	At         time.Time          `json:"at"`
	Values     map[string]float64 `json:"values"`
	Max        map[string]float64 `json:"max,omitempty"`
	Coverage   float64            `json:"coverage"`
	Generation string             `json:"generation,omitempty"`
}
type MetricSeries struct {
	Entity Entity        `json:"entity"`
	Points []MetricPoint `json:"points"`
}
type MetricsResponse struct {
	Resolution int               `json:"resolution_seconds"`
	Series     []MetricSeries    `json:"series"`
	From       time.Time         `json:"from"`
	To         time.Time         `json:"to"`
	Registry   map[string]Metric `json:"registry"`
}
type Metric struct {
	Unit        string `json:"unit"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
}

var Registry = map[string]Metric{
	"cpu.busy_pct":           {"%", "gauge", "CPU execution excluding idle, iowait and steal"},
	"cpu.iowait_pct":         {"%", "gauge", "Reported CPU iowait; not storage saturation"},
	"cpu.steal_pct":          {"%", "gauge", "Virtual CPU steal time"},
	"cpu.cores":              {"cores", "gauge", "Container CPU cores consumed"},
	"cpu.count":              {"cores", "gauge", "Online logical processors"},
	"cpu.quota_cores":        {"cores", "gauge", "Configured CPU quota"},
	"cpu.periods":            {"periods", "delta", "Observed bandwidth periods"},
	"cpu.throttled_periods":  {"periods", "delta", "Throttled bandwidth periods"},
	"cpu.throttled_pct":      {"%", "gauge", "Throttled-period fraction, not time lost"},
	"cpu.some_pct":           {"%", "gauge", "Interval CPU pressure some"},
	"memory.some_pct":        {"%", "gauge", "Interval memory pressure some"},
	"memory.full_pct":        {"%", "gauge", "Interval memory pressure full"},
	"io.some_pct":            {"%", "gauge", "Interval I/O pressure some"},
	"io.full_pct":            {"%", "gauge", "Interval I/O pressure full"},
	"memory.total_bytes":     {"B", "gauge", "Total host memory"},
	"memory.available_bytes": {"B", "gauge", "Host memory available"},
	"memory.used_bytes":      {"B", "gauge", "Host used or container charged memory"},
	"memory.working_bytes":   {"B", "gauge", "Container charged memory minus inactive file cache estimate"},
	"memory.used_pct":        {"%", "gauge", "Host memory used fraction"},
	"memory.limit_bytes":     {"B", "gauge", "Finite configured container memory limit"},
	"memory.oom_kills":       {"kills", "delta", "Observed OOM kills"},
	"swap.used_bytes":        {"B", "gauge", "Allocated swap"},
	"swap.out_bytes":         {"B", "delta", "Swap-out activity"},
	"load.1":                 {"tasks", "gauge", "One-minute load, not CPU usage"},
	"load.5":                 {"tasks", "gauge", "Five-minute load"},
	"load.15":                {"tasks", "gauge", "Fifteen-minute load"},
	"uptime.seconds":         {"s", "gauge", "Host uptime"},
	"disk.read_bytes":        {"B", "delta", "Accounted block read bytes"},
	"disk.write_bytes":       {"B", "delta", "Accounted block write bytes"},
	"disk.read_ops":          {"operations", "delta", "Completed reads"},
	"disk.write_ops":         {"operations", "delta", "Completed writes"},
	"disk.read_ms":           {"ms", "delta", "Summed read duration"},
	"disk.write_ms":          {"ms", "delta", "Summed write duration"},
	"disk.busy_pct":          {"%", "gauge", "Device busy estimate, not saturation"},
	"disk.queue":             {"requests", "gauge", "Time-weighted outstanding I/O"},
	"disk.await_ms":          {"ms", "ratio", "Operation-weighted average completed-request time"},
	"fs.used_bytes":          {"B", "gauge", "Filesystem occupied space"},
	"fs.total_bytes":         {"B", "gauge", "Filesystem capacity"},
	"fs.used_pct":            {"%", "gauge", "Filesystem space used, not device activity"},
	"net.rx_bytes":           {"B", "delta", "Received bytes"},
	"net.tx_bytes":           {"B", "delta", "Transmitted bytes"},
	"net.errors":             {"errors", "delta", "Network error counter increments"},
	"temperature.celsius":    {"°C", "gauge", "Readable sensor temperature"},
}
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,180}$`)
var sequencePattern = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)

func ValidID(s string) bool { return idPattern.MatchString(s) }
func (b Batch) Validate(now time.Time) error {
	if b.Processes != nil {
		p := b.Processes
		if len(p.Processes) > 20 || p.Visible < 0 || p.Visible > 10000 || p.At.IsZero() || p.At.After(now.Add(5*time.Minute)) || p.At.Before(now.Add(-48*time.Hour)) {
			return errors.New("invalid_process_snapshot")
		}
		for _, process := range p.Processes {
			if process.PID <= 0 || process.PID > 1<<31 || len(process.Name) > 64 || process.RSSBytes < 0 || process.RSSBytes > 1e22 || math.IsNaN(process.RSSBytes) || math.IsInf(process.RSSBytes, 0) {
				return errors.New("invalid_process")
			}
			if process.CPUPercent != nil && (math.IsNaN(*process.CPUPercent) || math.IsInf(*process.CPUPercent, 0) || *process.CPUPercent < 0 || *process.CPUPercent > 1e6) {
				return errors.New("invalid_process_cpu")
			}
		}
	}
	if b.Protocol != 1 {
		return errors.New("unsupported_protocol")
	}
	if !ValidID(b.HostID) || !ValidID(b.BootID) || !ValidID(b.Epoch) || !sequencePattern.MatchString(b.Sequence) {
		return errors.New("invalid_identity")
	}
	if b.Sampling != 5 && b.Sampling != 10 && b.Sampling != 15 {
		return errors.New("invalid_sampling")
	}
	if len(b.AgentVersion) > 32 || len(b.Frames) < 1 || len(b.Frames) > 120 || len(b.Entities) > MaxEntities || len(b.Events) > 256 || len(b.Capabilities) > 64 {
		return errors.New("batch_limit")
	}
	known := map[string]bool{}
	for _, e := range b.Entities {
		if !ValidID(e.ID) || !ValidID(e.Generation) || len(e.Name) > 256 || len(e.Metadata) > 16 || known[e.ID] {
			return errors.New("invalid_entity")
		}
		switch e.Kind {
		case "host", "core", "device", "filesystem", "network", "container", "container_io", "sensor", "cgroup":
		default:
			return errors.New("invalid_entity_kind")
		}
		for k, v := range e.Metadata {
			if !ValidID(k) || len(v) > 512 {
				return errors.New("metadata_limit")
			}
		}
		known[e.ID] = true
	}
	var previous time.Time
	for _, f := range b.Frames {
		if f.At.IsZero() || f.At.After(now.Add(5*time.Minute)) || f.At.Before(now.Add(-48*time.Hour)) || (!previous.IsZero() && !f.At.After(previous)) {
			return errors.New("invalid_timestamp")
		}
		previous = f.At
		if math.IsNaN(f.Interval) || math.IsInf(f.Interval, 0) || f.Interval <= 0 || f.Interval > 60 || len(f.Samples) > MaxEntities {
			return errors.New("invalid_interval")
		}
		seen := map[string]bool{}
		for _, s := range f.Samples {
			if !known[s.EntityID] || seen[s.EntityID] || !ValidID(s.Generation) || len(s.Values) > 64 || len(s.Quality) > 8 {
				return errors.New("invalid_sample")
			}
			seen[s.EntityID] = true
			for _, q := range s.Quality {
				if len(q) > 64 {
					return errors.New("quality_limit")
				}
			}
			for k, v := range s.Values {
				if _, ok := Registry[k]; !ok {
					return errors.New("unknown_metric")
				}
				if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e22 {
					return errors.New("invalid_metric_value")
				}
			}
		}
	}
	for _, e := range b.Events {
		if !ValidID(e.ID) || len(e.Type) > 64 || len(e.Message) > 512 || (!known[e.EntityID] && e.EntityID != "") || e.At.IsZero() || e.At.After(now.Add(5*time.Minute)) || e.At.Before(now.Add(-48*time.Hour)) {
			return errors.New("invalid_event")
		}
	}
	for k, c := range b.Capabilities {
		if !ValidID(k) || len(c.Reason) > 256 {
			return errors.New("invalid_capability")
		}
		switch c.Status {
		case "available", "unsupported", "permission_denied", "timeout", "error":
		default:
			return errors.New("invalid_capability_status")
		}
	}
	return nil
}
