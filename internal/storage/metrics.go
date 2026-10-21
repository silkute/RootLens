package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"rootlens/internal/protocol"
	"sort"
	"time"
)

type Aggregate struct {
	Sum      float64 `json:"sum"`
	Duration float64 `json:"duration"`
	Min      float64 `json:"min"`
	Max      float64 `json:"max"`
	Total    float64 `json:"total"`
}
type Bucket struct {
	Duration float64              `json:"duration"`
	Metrics  map[string]Aggregate `json:"metrics"`
}

func add(b *Bucket, values map[string]float64, duration, fraction float64) {
	if b.Metrics == nil {
		b.Metrics = map[string]Aggregate{}
	}
	b.Duration += duration
	for k, v := range values {
		extreme := v
		if protocol.Registry[k].Kind == "delta" && duration > 0 {
			extreme = v * fraction / duration
		}
		a, ok := b.Metrics[k]
		if !ok {
			a.Min = extreme
			a.Max = extreme
		}
		a.Min = math.Min(a.Min, extreme)
		a.Max = math.Max(a.Max, extreme)
		a.Sum += v * duration
		a.Duration += duration
		if protocol.Registry[k].Kind == "delta" {
			a.Total += v * fraction
		}
		b.Metrics[k] = a
	}
}
func rollup(tx *sql.Tx, host string, f protocol.Frame, p protocol.Sample) error {
	end := float64(f.At.UnixNano()) / 1e9
	start := end - f.Interval
	for _, resolution := range []int64{60, 900} {
		for cursor := start; cursor < end; {
			bucket := int64(math.Floor(cursor/float64(resolution))) * resolution
			stop := math.Min(end, float64(bucket+resolution))
			d := stop - cursor
			if d <= 0 {
				break
			}
			var b Bucket
			var payload string
			err := tx.QueryRow("SELECT payload FROM metric_rollups WHERE host_id=? AND entity_id=? AND resolution=? AND bucket=? AND generation=?", host, p.EntityID, resolution, bucket, p.Generation).Scan(&payload)
			if err == nil {
				if err = json.Unmarshal([]byte(payload), &b); err != nil {
					return err
				}
			} else if err != sql.ErrNoRows {
				return err
			}
			add(&b, p.Values, d, d/f.Interval)
			if _, err = tx.Exec("INSERT INTO metric_rollups VALUES(?,?,?,?,?,?) ON CONFLICT(host_id,entity_id,resolution,bucket,generation) DO UPDATE SET payload=excluded.payload", host, p.EntityID, resolution, bucket, p.Generation, JSON(b)); err != nil {
				return err
			}
			cursor = stop
		}
	}
	return nil
}
func merge(a *Bucket, b Bucket) {
	if a.Metrics == nil {
		a.Metrics = map[string]Aggregate{}
	}
	a.Duration += b.Duration
	for k, v := range b.Metrics {
		p, ok := a.Metrics[k]
		if !ok {
			p.Min = v.Min
			p.Max = v.Max
		}
		p.Sum += v.Sum
		p.Duration += v.Duration
		p.Total += v.Total
		p.Min = math.Min(p.Min, v.Min)
		p.Max = math.Max(p.Max, v.Max)
		a.Metrics[k] = p
	}
}
func point(b Bucket, at time.Time, resolution int, generation string) protocol.MetricPoint {
	p := protocol.MetricPoint{At: at, Values: map[string]float64{}, Max: map[string]float64{}, Coverage: math.Min(1, b.Duration/float64(resolution)), Generation: generation}
	for k, a := range b.Metrics {
		if a.Duration <= 0 {
			continue
		}
		if protocol.Registry[k].Kind == "delta" {
			p.Values[k] = a.Total / a.Duration
			p.Max[k] = a.Max
		} else {
			p.Values[k] = a.Sum / a.Duration
			p.Max[k] = a.Max
		}
	}
	ops := b.Metrics["disk.read_ops"].Total + b.Metrics["disk.write_ops"].Total
	if ops > 0 {
		p.Values["disk.await_ms"] = (b.Metrics["disk.read_ms"].Total + b.Metrics["disk.write_ms"].Total) / ops
	}
	periods := b.Metrics["cpu.periods"].Total
	if periods > 0 {
		p.Values["cpu.throttled_pct"] = 100 * b.Metrics["cpu.throttled_periods"].Total / periods
	}
	return p
}
func (s *Store) Entities(ctx context.Context, host, kind string) ([]protocol.Entity, error) {
	rows, e := s.DB.QueryContext(ctx, "SELECT id,kind,name,generation,metadata FROM entities WHERE host_id=? AND (?='' OR kind=?) ORDER BY kind,name LIMIT 1000", host, kind, kind)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []protocol.Entity{}
	for rows.Next() {
		var entity protocol.Entity
		var meta string
		if e = rows.Scan(&entity.ID, &entity.Kind, &entity.Name, &entity.Generation, &meta); e != nil {
			return nil, e
		}
		json.Unmarshal([]byte(meta), &entity.Metadata)
		result = append(result, entity)
	}
	return result, rows.Err()
}
func (s *Store) Metrics(ctx context.Context, host, entityID string, from, to time.Time, maxPoints int) (protocol.MetricsResponse, error) {
	if maxPoints < 10 || maxPoints > 1200 {
		maxPoints = 600
	}
	resolution := int(math.Ceil(to.Sub(from).Seconds() / float64(maxPoints)))
	if resolution < 5 {
		resolution = 5
	}
	r, e := s.Settings(ctx)
	if e != nil {
		return protocol.MetricsResponse{}, e
	}
	source := 0
	if resolution >= 900 || from.Before(time.Now().Add(-time.Duration(r.MinuteDays)*24*time.Hour)) {
		source = 900
	} else if resolution >= 60 || from.Before(time.Now().Add(-time.Duration(r.RawHours)*time.Hour)) {
		source = 60
	}
	if source > 0 {
		resolution = int(math.Ceil(float64(resolution)/float64(source))) * source
	}
	out := protocol.MetricsResponse{Resolution: resolution, From: from, To: to, Series: []protocol.MetricSeries{}, Registry: protocol.Registry}
	entities, e := s.Entities(ctx, host, "")
	if e != nil {
		return out, e
	}
	for _, entity := range entities {
		if entityID != "" && entity.ID != entityID && !(entity.Kind == "container_io" && entity.Metadata["container_id"] == entityID) {
			continue
		}
		if entityID == "" && (entity.Kind == "container_io" || entity.Kind == "core" || entity.Kind == "container" || entity.Kind == "cgroup") {
			continue
		}
		type key struct {
			bucket int64
			gen    string
		}
		buckets := map[key]*Bucket{}
		if source == 0 {
			rows, err := s.DB.QueryContext(ctx, "SELECT at,interval,generation,payload FROM metric_samples WHERE host_id=? AND entity_id=? AND at>=? AND at<=? ORDER BY at LIMIT 50000", host, entity.ID, Stamp(from), Stamp(to))
			if err != nil {
				return out, err
			}
			for rows.Next() {
				var at, gen, payload string
				var duration float64
				if err = rows.Scan(&at, &duration, &gen, &payload); err != nil {
					rows.Close()
					return out, err
				}
				var values map[string]float64
				if err = json.Unmarshal([]byte(payload), &values); err != nil {
					rows.Close()
					return out, err
				}
				end := float64(ParseTime(at).UnixNano()) / 1e9
				begin := math.Max(end-duration, float64(from.UnixNano())/1e9)
				for cursor := begin; cursor < end; {
					bucket := int64(math.Floor(cursor/float64(resolution))) * int64(resolution)
					stop := math.Min(end, float64(bucket+int64(resolution)))
					d := stop - cursor
					if d <= 0 {
						break
					}
					k := key{bucket, gen}
					b := buckets[k]
					if b == nil {
						b = &Bucket{}
						buckets[k] = b
					}
					add(b, values, d, d/duration)
					cursor = stop
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return out, err
			}
		} else {
			rows, err := s.DB.QueryContext(ctx, "SELECT bucket,generation,payload FROM metric_rollups WHERE host_id=? AND entity_id=? AND resolution=? AND bucket>=? AND bucket<=? ORDER BY bucket LIMIT 50000", host, entity.ID, source, from.Unix()/int64(source)*int64(source), to.Unix())
			if err != nil {
				return out, err
			}
			for rows.Next() {
				var timestamp int64
				var gen, payload string
				if err = rows.Scan(&timestamp, &gen, &payload); err != nil {
					rows.Close()
					return out, err
				}
				var next Bucket
				if err = json.Unmarshal([]byte(payload), &next); err != nil {
					rows.Close()
					return out, err
				}
				k := key{timestamp / int64(resolution) * int64(resolution), gen}
				b := buckets[k]
				if b == nil {
					b = &Bucket{}
					buckets[k] = b
				}
				merge(b, next)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return out, err
			}
		}
		series := protocol.MetricSeries{Entity: entity, Points: []protocol.MetricPoint{}}
		keys := []key{}
		for k := range buckets {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].bucket == keys[j].bucket {
				return keys[i].gen < keys[j].gen
			}
			return keys[i].bucket < keys[j].bucket
		})
		for _, k := range keys {
			series.Points = append(series.Points, point(*buckets[k], time.Unix(k.bucket, 0).UTC(), resolution, k.gen))
		}
		out.Series = append(out.Series, series)
	}
	return out, nil
}
func (s *Store) Hosts(ctx context.Context) ([]protocol.Host, error) {
	rows, e := s.DB.QueryContext(ctx, "SELECT id,name,agent_version,boot_id,last_seen,last_source,sampling,capabilities FROM hosts ORDER BY name LIMIT 100")
	if e != nil {
		return nil, e
	}
	out := []protocol.Host{}
	for rows.Next() {
		var h protocol.Host
		var seen, source sql.NullString
		var caps string
		if e = rows.Scan(&h.ID, &h.Name, &h.AgentVersion, &h.BootID, &seen, &source, &h.Sampling, &caps); e != nil {
			rows.Close()
			return nil, e
		}
		json.Unmarshal([]byte(caps), &h.Capabilities)
		h.Status = "waiting"
		if seen.Valid {
			t := ParseTime(seen.String)
			h.LastSeen = &t
			h.Status = "online"
			if time.Since(t) > time.Duration(h.Sampling*3)*time.Second {
				h.Status = "stale"
			}
			if time.Since(t) > 60*time.Second {
				h.Status = "offline"
			}
		}
		if source.Valid {
			t := ParseTime(source.String)
			h.LastSource = &t
			if h.Status == "online" && time.Since(t) > time.Duration(h.Sampling*3)*time.Second {
				h.Status = "stale"
			}
		}
		out = append(out, h)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for i := range out {
		out[i].Latest = map[string]float64{}
		var p string
		if e = s.DB.QueryRowContext(ctx, "SELECT payload FROM metric_samples WHERE host_id=? AND entity_id='host' ORDER BY at DESC LIMIT 1", out[i].ID).Scan(&p); e == nil {
			json.Unmarshal([]byte(p), &out[i].Latest)
		}
		var filesystem string
		if e = s.DB.QueryRowContext(ctx, `SELECT m.payload FROM metric_samples m JOIN entities e ON e.host_id=m.host_id AND e.id=m.entity_id WHERE m.host_id=? AND e.kind='filesystem' ORDER BY m.at DESC,e.id LIMIT 1`, out[i].ID).Scan(&filesystem); e == nil {
			var values map[string]float64
			if json.Unmarshal([]byte(filesystem), &values) == nil {
				if value, ok := values["fs.used_pct"]; ok {
					out[i].Latest["fs.used_pct"] = value
				}
			}
		}
		_ = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM incidents WHERE host_id=? AND state='open'", out[i].ID).Scan(&out[i].ActiveIncidents)
	}
	return out, nil
}
func (s *Store) Incidents(ctx context.Context, host, before string, limit int) ([]protocol.Incident, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	if before == "" {
		before = "9999"
	}
	rows, e := s.DB.QueryContext(ctx, "SELECT payload FROM incidents WHERE (?='' OR host_id=?) AND start<? ORDER BY start DESC,id DESC LIMIT ?", host, host, before, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []protocol.Incident{}
	for rows.Next() {
		var payload string
		if e = rows.Scan(&payload); e != nil {
			return nil, e
		}
		var inc protocol.Incident
		if e = json.Unmarshal([]byte(payload), &inc); e != nil {
			return nil, e
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}
func (s *Store) Incident(ctx context.Context, id string) (protocol.Incident, error) {
	var p string
	var inc protocol.Incident
	e := s.DB.QueryRowContext(ctx, "SELECT payload FROM incidents WHERE id=?", id).Scan(&p)
	if e == nil {
		e = json.Unmarshal([]byte(p), &inc)
	}
	return inc, e
}
func (s *Store) Events(ctx context.Context, host, before string) ([]protocol.Event, error) {
	if before == "" {
		before = "9999"
	}
	rows, e := s.DB.QueryContext(ctx, "SELECT payload FROM events WHERE (?='' OR host_id=?) AND at<? ORDER BY at DESC,id DESC LIMIT 200", host, host, before)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []protocol.Event{}
	for rows.Next() {
		var p string
		if e = rows.Scan(&p); e != nil {
			return nil, e
		}
		var ev protocol.Event
		if e = json.Unmarshal([]byte(p), &ev); e != nil {
			return nil, e
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}
