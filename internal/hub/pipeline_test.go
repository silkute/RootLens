package hub

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"rootlens/internal/agent"
	"rootlens/internal/protocol"
	"rootlens/internal/storage"
	"testing"
	"time"
)

func TestCollectorTLSIngestionIncidentPipeline(t *testing.T) {
	h, server := testHub(t)
	credential := Token()
	h.Store.Write(context.Background(), func(tx *sql.Tx) error {
		tx.Exec("INSERT INTO hosts(id,name) VALUES('pipeline','Pipeline fixture')")
		_, e := tx.Exec("INSERT INTO agent_credentials VALUES('cred','pipeline',?,0,?)", Hash(credential), storage.Stamp(time.Now()))
		return e
	})
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	os.MkdirAll(filepath.Join(proc, "pressure"), 0700)
	os.MkdirAll(filepath.Join(proc, "sys/kernel/random"), 0700)
	os.WriteFile(filepath.Join(proc, "sys/kernel/random/boot_id"), []byte("pipeline-boot"), 0600)
	os.WriteFile(filepath.Join(proc, "meminfo"), []byte("MemTotal: 1000 kB\nMemAvailable: 500 kB\n"), 0600)
	c := agent.NewCollector(agent.Config{Proc: proc, Sys: filepath.Join(root, "sys"), Sampling: 5})
	start := time.Now().UTC().Add(-70 * time.Second)
	for i := 0; i < 14; i++ {
		os.WriteFile(filepath.Join(proc, "pressure/io"), []byte(fmt.Sprintf("some total=%d\nfull total=0\n", i*1000000)), 0600)
		at := start.Add(time.Duration(i) * 5 * time.Second)
		frame, entities, caps, events := c.Collect(at)
		batch := protocol.Batch{Protocol: 1, HostID: "pipeline", AgentVersion: protocol.Version, BootID: c.Boot, Epoch: "pipeline", Sequence: fmt.Sprint(i + 1), Sampling: 5, SentAt: at, Entities: entities, Frames: []protocol.Frame{frame}, Events: events, Capabilities: caps}
		batch.Processes = &protocol.ProcessSnapshot{At: at, Visible: 1, Processes: []protocol.Process{{PID: 42, Name: "private-process-canary", RSSBytes: 4096}}}
		resp := request(t, server, "POST", "/api/v1/agent/batches", "", credential, batch, nil, "")
		if resp.StatusCode != 200 {
			var body map[string]any
			json.NewDecoder(resp.Body).Decode(&body)
			resp.Body.Close()
			t.Fatalf("pipeline batch %d: %d %+v", i, resp.StatusCode, body)
		}
		closeResponse(resp)
	}
	incidents, e := h.Store.Incidents(context.Background(), "pipeline", "", 10)
	if e != nil || len(incidents) != 1 {
		t.Fatalf("pipeline did not detect pressure: %+v %v", incidents, e)
	}
	inc := incidents[0]
	if inc.Rule != "io-pressure" || len(inc.Contributors) != 0 || inc.Evidence[0].Value != 20 {
		t.Fatalf("pipeline overstated evidence: %+v", inc)
	}
	var evidenceCount int
	h.Store.DB.QueryRow("SELECT COUNT(*) FROM incident_evidence WHERE incident_id=?", inc.ID).Scan(&evidenceCount)
	if evidenceCount == 0 {
		t.Fatal("no retained evidence")
	}
	h.processMu.RLock()
	snapshot, ok := h.processes["pipeline"]
	h.processMu.RUnlock()
	if !ok || len(snapshot.Processes) != 1 {
		t.Fatal("latest process snapshot was not cached")
	}
	var stored int
	if e = h.Store.DB.QueryRow(`SELECT count(*) FROM (SELECT payload AS value FROM metric_samples UNION ALL SELECT payload FROM events UNION ALL SELECT payload FROM incidents UNION ALL SELECT payload FROM rule_state UNION ALL SELECT metadata FROM entities) WHERE value LIKE '%private-process-canary%'`).Scan(&stored); e != nil {
		t.Fatal(e)
	}
	if stored != 0 {
		t.Fatal("private process snapshot leaked into persistent history")
	}
}
