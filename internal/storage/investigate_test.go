package storage

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestHistoricalInvestigationDoesNotMutateLiveIncidents(t *testing.T) {
	s := fixtureStore(t)
	ctx := context.Background()
	start := time.Now().UTC().Add(-2 * time.Minute)
	for i := 0; i < 15; i++ {
		b := testBatch(start.Add(time.Duration(i) * 5 * time.Second))
		b.Sequence = stringSequence(i + 1)
		body, _ := json.Marshal(b)
		if _, e := s.Ingest(ctx, b, body); e != nil {
			t.Fatal(e)
		}
	}
	before, _ := s.Incidents(ctx, "h", "", 10)
	result, e := s.Investigate(ctx, "h", start.Add(-5*time.Second), start.Add(75*time.Second))
	if e != nil || len(result) != 1 || !result[0].Historical {
		t.Fatalf("historical replay: %+v %v", result, e)
	}
	after, _ := s.Incidents(ctx, "h", "", 10)
	if JSON(before) != JSON(after) {
		t.Fatal("manual analysis mutated persisted live reports")
	}
}
