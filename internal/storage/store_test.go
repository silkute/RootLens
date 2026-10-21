package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"rootlens/internal/protocol"
	"testing"
	"time"
)

func fixtureStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	if e = s.Write(context.Background(), func(tx *sql.Tx) error {
		_, e := tx.Exec("INSERT INTO hosts(id,name) VALUES('h','Test host')")
		return e
	}); e != nil {
		t.Fatal(e)
	}
	return s
}
func testBatch(at time.Time) protocol.Batch {
	return protocol.Batch{Protocol: 1, HostID: "h", AgentVersion: "0.1.0", BootID: "boot", Epoch: "epoch", Sequence: "1", Sampling: 5, SentAt: at, Entities: []protocol.Entity{{ID: "host", Kind: "host", Name: "Test", Generation: "boot"}}, Frames: []protocol.Frame{{At: at, Interval: 5, Samples: []protocol.Sample{{EntityID: "host", Generation: "boot", Values: map[string]float64{"io.some_pct": 25, "cpu.busy_pct": 30}}}}}, Capabilities: map[string]protocol.Capability{}}
}
func TestIngestionAtomicDedupAndConflict(t *testing.T) {
	s := fixtureStore(t)
	ctx := context.Background()
	b := testBatch(time.Now().UTC())
	body, _ := json.Marshal(b)
	duplicate, e := s.Ingest(ctx, b, body)
	if e != nil || duplicate {
		t.Fatalf("first batch: %v", e)
	}
	duplicate, e = s.Ingest(ctx, b, body)
	if e != nil || !duplicate {
		t.Fatal("retry not idempotent")
	}
	b.Frames[0].Samples[0].Values["io.some_pct"] = 99
	body, _ = json.Marshal(b)
	if _, e = s.Ingest(ctx, b, body); !errors.Is(e, ErrConflict) {
		t.Fatalf("conflict accepted: %v", e)
	}
	var n int
	s.DB.QueryRow("SELECT COUNT(*) FROM metric_samples").Scan(&n)
	if n != 1 {
		t.Fatalf("wrong sample count %d", n)
	}
	b.Sequence = "2"
	body, _ = json.Marshal(b)
	if _, e = s.Ingest(ctx, b, body); !errors.Is(e, ErrOrder) {
		t.Fatal("reused source interval accepted")
	}
}
func TestPersistedRulesResumeAndBackup(t *testing.T) {
	s := fixtureStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 15; i++ {
		b := testBatch(now.Add(time.Duration(i-15) * 5 * time.Second))
		b.Sequence = stringSequence(i + 1)
		body, _ := json.Marshal(b)
		if _, e := s.Ingest(ctx, b, body); e != nil {
			t.Fatal(e)
		}
	}
	inc, e := s.Incidents(ctx, "h", "", 10)
	if e != nil || len(inc) != 1 || inc[0].State != "open" {
		t.Fatalf("persisted incident %+v %v", inc, e)
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	if e = s.Backup(ctx, backup); e != nil {
		t.Fatal(e)
	}
	restored, e := Open(backup)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	inc, e = restored.Incidents(ctx, "h", "", 10)
	if e != nil || len(inc) != 1 {
		t.Fatal("backup lost incident")
	}
	if e = restored.Tick(ctx, now.Add(2*time.Minute)); e != nil {
		t.Fatal(e)
	}
	inc, _ = restored.Incidents(ctx, "h", "", 10)
	if inc[0].State != "interrupted" {
		t.Fatal("restart lost evaluation state")
	}
}
func stringSequence(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return string(digits[i])
	}
	return string(digits[i/10]) + string(digits[i%10])
}
func TestWeightedRollupAndCounterRate(t *testing.T) {
	b := Bucket{}
	add(&b, map[string]float64{"cpu.busy_pct": 20, "disk.read_bytes": 100, "disk.read_ops": 1, "disk.read_ms": 100}, 5, 1)
	add(&b, map[string]float64{"cpu.busy_pct": 80, "disk.read_bytes": 600, "disk.read_ops": 9, "disk.read_ms": 90}, 15, 1)
	p := point(b, time.Now(), 60, "g")
	if p.Values["cpu.busy_pct"] != 65 {
		t.Fatal("unweighted gauge average")
	}
	if p.Values["disk.read_bytes"] != 35 {
		t.Fatal("wrong rate")
	}
	if p.Max["disk.read_bytes"] != 40 {
		t.Fatal("peak rate must preserve the interval denominator")
	}
	if p.Values["disk.await_ms"] != 19 {
		t.Fatal("latency averaged averages")
	}
	if p.Coverage != 1.0/3.0 {
		t.Fatal("coverage missing")
	}
}
func TestFutureSchemaRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	s.DB.Exec("PRAGMA user_version=99")
	s.Close()
	if s, e = Open(path); e == nil {
		s.Close()
		t.Fatal("future schema accepted")
	}
}
func TestMetricGapsRemainAbsent(t *testing.T) {
	s := fixtureStore(t)
	now := time.Now().UTC()
	b := testBatch(now.Add(-time.Minute))
	body, _ := json.Marshal(b)
	s.Ingest(context.Background(), b, body)
	b.Sequence = "2"
	b.Frames[0].At = now
	body, _ = json.Marshal(b)
	s.Ingest(context.Background(), b, body)
	m, e := s.Metrics(context.Background(), "h", "host", now.Add(-2*time.Minute), now, 600)
	if e != nil || len(m.Series) != 1 || len(m.Series[0].Points) > 4 || len(m.Series[0].Points) < 2 {
		t.Fatalf("gaps interpolated %+v %v", m, e)
	}
	covered := 0.0
	for _, p := range m.Series[0].Points {
		covered += p.Coverage * float64(m.Resolution)
	}
	if math.Abs(covered-10) > .001 {
		t.Fatalf("raw query lost interval coverage: %f", covered)
	}
}
