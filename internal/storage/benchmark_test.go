package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"rootlens/internal/protocol"
	"testing"
	"time"
)

func BenchmarkIngest30Entities(b *testing.B) {
	s, e := Open(filepath.Join(b.TempDir(), "bench.db"))
	if e != nil {
		b.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	s.Write(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec("INSERT INTO hosts(id,name) VALUES('h','Benchmark')")
		return e
	})
	batch := testBatch(time.Now().Add(-24 * time.Hour))
	batch.Entities = nil
	batch.Frames[0].Samples = nil
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("container-%d", i)
		batch.Entities = append(batch.Entities, protocol.Entity{ID: id, Kind: "container", Name: id, Generation: "run"})
		batch.Frames[0].Samples = append(batch.Frames[0].Samples, protocol.Sample{EntityID: id, Generation: "run", Values: map[string]float64{"cpu.cores": 0.1, "memory.used_bytes": 256 * 1048576, "net.rx_bytes": 1024, "net.tx_bytes": 512, "cpu.periods": 50, "cpu.throttled_periods": 0, "cpu.throttled_pct": 0}})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		batch.Sequence = fmt.Sprint(i + 1)
		batch.Frames[0].At = batch.Frames[0].At.Add(5 * time.Second)
		payload, _ := json.Marshal(batch)
		if _, e = s.Ingest(ctx, batch, payload); e != nil {
			b.Fatal(e)
		}
	}
	b.StopTimer()
	s.Checkpoint(ctx)
	if info, e := os.Stat(s.Path); e == nil {
		b.ReportMetric(float64(info.Size())/float64(b.N), "db-bytes/batch")
	}
}
