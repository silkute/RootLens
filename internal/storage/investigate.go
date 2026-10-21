package storage

import (
	"context"
	"encoding/json"
	"errors"
	"rootlens/internal/diagnostics"
	"rootlens/internal/protocol"
	"sort"
	"time"
)

var ErrAnalysisLimit = errors.New("analysis_limit")

func (s *Store) Investigate(ctx context.Context, host string, from, to time.Time) ([]protocol.Incident, error) {
	entities, e := s.Entities(ctx, host, "")
	if e != nil {
		return nil, e
	}
	kinds := map[string]string{}
	for _, entity := range entities {
		kinds[entity.ID] = entity.Kind
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	eventRows, e := tx.QueryContext(ctx, "SELECT payload FROM events WHERE host_id=? AND at>=? AND at<=? ORDER BY at LIMIT 10001", host, Stamp(from), Stamp(to))
	if e != nil {
		return nil, e
	}
	events := []protocol.Event{}
	for eventRows.Next() {
		var p string
		if e = eventRows.Scan(&p); e != nil {
			eventRows.Close()
			return nil, e
		}
		var event protocol.Event
		if e = json.Unmarshal([]byte(p), &event); e != nil {
			eventRows.Close()
			return nil, e
		}
		events = append(events, event)
	}
	e = eventRows.Err()
	eventRows.Close()
	if e != nil {
		return nil, e
	}
	if len(events) > 10000 {
		return nil, ErrAnalysisLimit
	}
	rows, e := tx.QueryContext(ctx, "SELECT at,interval,entity_id,generation,payload FROM metric_samples WHERE host_id=? AND at>=? AND at<=? ORDER BY at,entity_id LIMIT 250001", host, Stamp(from), Stamp(to))
	if e != nil {
		return nil, e
	}
	st := diagnostics.NewState()
	results := map[string]protocol.Incident{}
	index, count := 0, 0
	for rows.Next() {
		count++
		if count > 250000 {
			rows.Close()
			return nil, ErrAnalysisLimit
		}
		var at, p string
		var f protocol.Frame
		var sample protocol.Sample
		if e = rows.Scan(&at, &f.Interval, &sample.EntityID, &sample.Generation, &p); e != nil {
			rows.Close()
			return nil, e
		}
		f.At = ParseTime(at)
		if e = json.Unmarshal([]byte(p), &sample.Values); e != nil {
			rows.Close()
			return nil, e
		}
		f.Samples = []protocol.Sample{sample}
		for _, inc := range diagnostics.Evaluate(st, host, f, kinds, true) {
			results[inc.ID] = inc
		}
		for index < len(events) && !events[index].At.After(f.At) {
			for _, inc := range diagnostics.ObserveEvents(st, host, events[index:index+1], true) {
				results[inc.ID] = inc
			}
			index++
		}
		for _, inc := range diagnostics.Tick(st, f.At) {
			results[inc.ID] = inc
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for index < len(events) {
		for _, inc := range diagnostics.ObserveEvents(st, host, events[index:index+1], true) {
			results[inc.ID] = inc
		}
		index++
	}
	out := []protocol.Incident{}
	for _, inc := range results {
		if inc.Rule == "io-pressure" {
			inc.Contributors, e = contributors(tx, inc)
			if e != nil {
				return nil, e
			}
			support, err := deviceEvidence(tx, inc)
			if err != nil {
				return nil, err
			}
			inc.Evidence = append(inc.Evidence, support...)
		}
		inc.Limitations = append(inc.Limitations, "This is a historical replay bounded by the selected interval; conditions may have started before that interval.")
		out = append(out, inc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start.Equal(out[j].Start) {
			return out[i].ID < out[j].ID
		}
		return out[i].Start.Before(out[j].Start)
	})
	return out, nil
}
