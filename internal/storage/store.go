package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"rootlens/internal/diagnostics"
	"rootlens/internal/platform"
	"rootlens/internal/protocol"
	"sync"
	"time"
)

var ErrConflict = errors.New("sequence_conflict")
var ErrOrder = errors.New("out_of_order")

type Store struct {
	DB   *sql.DB
	Path string
	mu   sync.Mutex
}

func JSON(v any) string            { b, _ := json.Marshal(v); return string(b) }
func ParseTime(s string) time.Time { t, _ := time.Parse(time.RFC3339Nano, s); return t }
func Stamp(t time.Time) string     { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }
func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.ToSlash(abs)+"?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	s := &Store{DB: db, Path: abs}
	if err = s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	for _, privateFile := range []string{abs, abs + "-wal", abs + "-shm"} {
		if e := os.Chmod(privateFile, 0600); e != nil && !os.IsNotExist(e) {
			db.Close()
			return nil, e
		}
	}
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) Write(ctx context.Context, fn func(*sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) migrate() error {
	var version int
	if err := s.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 1 {
		return errors.New("database_newer_than_binary")
	}
	if version == 1 {
		return nil
	}
	return s.Write(context.Background(), func(tx *sql.Tx) error { _, err := tx.Exec(schema); return err })
}

const schema = `
CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL,checksum TEXT NOT NULL);
CREATE TABLE users(id INTEGER PRIMARY KEY CHECK(id=1),username TEXT NOT NULL UNIQUE,password_hash TEXT NOT NULL);
CREATE TABLE sessions(hash TEXT PRIMARY KEY,csrf TEXT NOT NULL,expires TEXT NOT NULL);
CREATE TABLE hosts(id TEXT PRIMARY KEY,name TEXT NOT NULL,agent_version TEXT NOT NULL DEFAULT '',boot_id TEXT NOT NULL DEFAULT '',epoch TEXT NOT NULL DEFAULT '',last_seen TEXT,last_source TEXT,sampling INTEGER NOT NULL DEFAULT 5,capabilities TEXT NOT NULL DEFAULT '{}');
CREATE TABLE enrollment_tokens(hash TEXT PRIMARY KEY,host_id TEXT NOT NULL REFERENCES hosts(id),expires TEXT NOT NULL,consumed INTEGER NOT NULL DEFAULT 0);
CREATE TABLE agent_credentials(id TEXT PRIMARY KEY,host_id TEXT NOT NULL REFERENCES hosts(id),hash TEXT NOT NULL UNIQUE,revoked INTEGER NOT NULL DEFAULT 0,created TEXT NOT NULL);
CREATE TABLE entities(host_id TEXT NOT NULL REFERENCES hosts(id),id TEXT NOT NULL,kind TEXT NOT NULL,name TEXT NOT NULL,generation TEXT NOT NULL,metadata TEXT NOT NULL,last_seen TEXT NOT NULL,PRIMARY KEY(host_id,id));
CREATE TABLE entity_versions(host_id TEXT NOT NULL,entity_id TEXT NOT NULL,at TEXT NOT NULL,metadata TEXT NOT NULL,PRIMARY KEY(host_id,entity_id,at));
CREATE TABLE container_runs(host_id TEXT NOT NULL,entity_id TEXT NOT NULL,generation TEXT NOT NULL,start TEXT NOT NULL,end TEXT,PRIMARY KEY(host_id,entity_id,generation));
CREATE TABLE ingest_batches(host_id TEXT NOT NULL REFERENCES hosts(id),epoch TEXT NOT NULL,sequence INTEGER NOT NULL,hash TEXT NOT NULL,received TEXT NOT NULL,PRIMARY KEY(host_id,epoch,sequence));
CREATE TABLE metric_samples(host_id TEXT NOT NULL,entity_id TEXT NOT NULL,at TEXT NOT NULL,interval REAL NOT NULL,generation TEXT NOT NULL,payload TEXT NOT NULL,quality TEXT NOT NULL,PRIMARY KEY(host_id,entity_id,at));
CREATE INDEX samples_time ON metric_samples(at);
CREATE TABLE metric_rollups(host_id TEXT NOT NULL,entity_id TEXT NOT NULL,resolution INTEGER NOT NULL,bucket INTEGER NOT NULL,generation TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(host_id,entity_id,resolution,bucket,generation));
CREATE INDEX rollups_time ON metric_rollups(resolution,bucket);
CREATE TABLE events(id TEXT NOT NULL,host_id TEXT NOT NULL,entity_id TEXT NOT NULL,at TEXT NOT NULL,received TEXT NOT NULL,type TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(host_id,id));
CREATE INDEX events_time ON events(host_id,at);
CREATE TABLE incidents(id TEXT PRIMARY KEY,host_id TEXT NOT NULL,entity_id TEXT NOT NULL,rule TEXT NOT NULL,start TEXT NOT NULL,updated TEXT NOT NULL,state TEXT NOT NULL,payload TEXT NOT NULL);
CREATE INDEX incidents_time ON incidents(host_id,start);
CREATE TABLE incident_evidence(incident_id TEXT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,entity_id TEXT NOT NULL,resolution INTEGER NOT NULL,bucket INTEGER NOT NULL,generation TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(incident_id,entity_id,resolution,bucket,generation));
CREATE TABLE rule_state(host_id TEXT PRIMARY KEY REFERENCES hosts(id),payload TEXT NOT NULL);
CREATE TABLE settings(id INTEGER PRIMARY KEY CHECK(id=1),payload TEXT NOT NULL);
INSERT INTO settings VALUES(1,'{"raw_hours":48,"minute_days":14,"quarter_days":180,"event_days":180}');
CREATE TABLE audit_events(id INTEGER PRIMARY KEY AUTOINCREMENT,at TEXT NOT NULL,action TEXT NOT NULL,subject TEXT NOT NULL);
INSERT INTO schema_migrations VALUES(1,strftime('%Y-%m-%dT%H:%M:%fZ','now'),'initial-v1');
PRAGMA user_version=1;
`

func Audit(tx *sql.Tx, action, subject string) error {
	_, err := tx.Exec("INSERT INTO audit_events(at,action,subject) VALUES(?,?,?)", Stamp(time.Now()), action, subject)
	return err
}
func (s *Store) Ingest(ctx context.Context, b protocol.Batch, body []byte) (bool, error) {
	h := sha256.Sum256(body)
	digest := hex.EncodeToString(h[:])
	duplicate := false
	now := time.Now().UTC()
	err := s.Write(ctx, func(tx *sql.Tx) error {
		var existing string
		err := tx.QueryRow("SELECT hash FROM ingest_batches WHERE host_id=? AND epoch=? AND sequence=?", b.HostID, b.Epoch, b.Sequence).Scan(&existing)
		if err == nil {
			if existing != digest {
				return ErrConflict
			}
			duplicate = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var lastSource, oldBoot, oldEpoch string
		if err = tx.QueryRow("SELECT COALESCE(last_source,''),boot_id,epoch FROM hosts WHERE id=?", b.HostID).Scan(&lastSource, &oldBoot, &oldEpoch); err != nil {
			return err
		}
		if lastSource != "" && !b.Frames[0].At.After(ParseTime(lastSource)) {
			return ErrOrder
		}
		var count int
		if err = tx.QueryRow("SELECT COUNT(*) FROM entities WHERE host_id=?", b.HostID).Scan(&count); err != nil {
			return err
		}
		if count+len(b.Entities) > 10000 {
			return errors.New("entity_history_limit")
		}
		var state diagnostics.State
		var encoded string
		err = tx.QueryRow("SELECT payload FROM rule_state WHERE host_id=?", b.HostID).Scan(&encoded)
		if err == nil {
			if err = json.Unmarshal([]byte(encoded), &state); err != nil {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if oldBoot != "" && (oldBoot != b.BootID || oldEpoch != b.Epoch) {
			for _, inc := range diagnostics.InterruptAll(&state, b.Frames[0].At) {
				if err = saveIncident(tx, inc); err != nil {
					return err
				}
			}
			state = *diagnostics.NewState()
		}
		kinds := map[string]string{}
		for _, e := range b.Entities {
			kinds[e.ID] = e.Kind
			var prev string
			_ = tx.QueryRow("SELECT metadata FROM entities WHERE host_id=? AND id=?", b.HostID, e.ID).Scan(&prev)
			meta := JSON(e.Metadata)
			if _, err = tx.Exec(`INSERT INTO entities VALUES(?,?,?,?,?,?,?) ON CONFLICT(host_id,id) DO UPDATE SET name=excluded.name,generation=excluded.generation,metadata=excluded.metadata,last_seen=excluded.last_seen`, b.HostID, e.ID, e.Kind, e.Name, e.Generation, meta, Stamp(b.Frames[len(b.Frames)-1].At)); err != nil {
				return err
			}
			if prev != meta {
				if _, err = tx.Exec("INSERT OR IGNORE INTO entity_versions VALUES(?,?,?,?)", b.HostID, e.ID, Stamp(b.Frames[0].At), meta); err != nil {
					return err
				}
			}
			if e.Kind == "container" {
				if _, err = tx.Exec("UPDATE container_runs SET end=? WHERE host_id=? AND entity_id=? AND generation<>? AND end IS NULL", Stamp(b.Frames[0].At), b.HostID, e.ID, e.Generation); err != nil {
					return err
				}
				if _, err = tx.Exec("INSERT OR IGNORE INTO container_runs(host_id,entity_id,generation,start) VALUES(?,?,?,?)", b.HostID, e.ID, e.Generation, Stamp(b.Frames[0].At)); err != nil {
					return err
				}
			}
		}
		for _, f := range b.Frames {
			for _, p := range f.Samples {
				if _, err = tx.Exec("INSERT INTO metric_samples VALUES(?,?,?,?,?,?,?)", b.HostID, p.EntityID, Stamp(f.At), f.Interval, p.Generation, JSON(p.Values), JSON(p.Quality)); err != nil {
					return err
				}
				if err = rollup(tx, b.HostID, f, p); err != nil {
					return err
				}
			}
			for _, inc := range diagnostics.Evaluate(&state, b.HostID, f, kinds, now.Sub(f.At) > 30*time.Second) {
				if err = saveIncident(tx, inc); err != nil {
					return err
				}
			}
		}
		newEvents := []protocol.Event{}
		for _, e := range b.Events {
			res, er := tx.Exec("INSERT OR IGNORE INTO events VALUES(?,?,?,?,?,?,?)", e.ID, b.HostID, e.EntityID, Stamp(e.At), Stamp(now), e.Type, JSON(e))
			if er != nil {
				return er
			}
			n, _ := res.RowsAffected()
			if n > 0 {
				newEvents = append(newEvents, e)
			}
		}
		for _, event := range newEvents {
			for _, inc := range diagnostics.ObserveEvents(&state, b.HostID, []protocol.Event{event}, now.Sub(event.At) > 30*time.Second) {
				if err = saveIncident(tx, inc); err != nil {
					return err
				}
			}
		}
		if b.Capabilities["docker.events"].Status == "available" {
			for _, window := range state.Events {
				window.LastCoverage = b.Frames[len(b.Frames)-1].At
			}
		}
		if _, err = tx.Exec("INSERT INTO rule_state VALUES(?,?) ON CONFLICT(host_id) DO UPDATE SET payload=excluded.payload", b.HostID, JSON(state)); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE hosts SET agent_version=?,boot_id=?,epoch=?,last_seen=?,last_source=?,sampling=?,capabilities=? WHERE id=?", b.AgentVersion, b.BootID, b.Epoch, Stamp(now), Stamp(b.Frames[len(b.Frames)-1].At), b.Sampling, JSON(b.Capabilities), b.HostID); err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO ingest_batches VALUES(?,?,?,?,?)", b.HostID, b.Epoch, b.Sequence, digest, Stamp(now))
		return err
	})
	return duplicate, err
}
func saveIncident(tx *sql.Tx, inc protocol.Incident) error {
	var oldPayload string
	var old protocol.Incident
	_ = tx.QueryRow("SELECT payload FROM incidents WHERE id=?", inc.ID).Scan(&oldPayload)
	_ = json.Unmarshal([]byte(oldPayload), &old)
	if old.ID != "" {
		inc.Contributors = old.Contributors
		inc.PreviousID = old.PreviousID
		for _, evidence := range old.Evidence {
			if evidence.Level == "strongly_supported_inference" {
				inc.Evidence = append(inc.Evidence, evidence)
			}
		}
	}
	if inc.Rule == "io-pressure" && (old.ID == "" || inc.Updated.Unix()/60 != old.Updated.Unix()/60 || inc.State != "open") {
		contributors, err := contributors(tx, inc)
		if err != nil {
			return err
		}
		inc.Contributors = contributors
		support, err := deviceEvidence(tx, inc)
		if err != nil {
			return err
		}
		if len(support) > 0 {
			facts := []protocol.Evidence{}
			for _, ev := range inc.Evidence {
				if ev.Level != "strongly_supported_inference" {
					facts = append(facts, ev)
				}
			}
			inc.Evidence = append(facts, support...)
		}
		if len(contributors) == 0 {
			inc.Limitations = append(inc.Limitations, "Reliable workload-to-device attribution is unavailable or no accounted container I/O was observed.")
		} else {
			inc.Limitations = append(inc.Limitations, "Shares refer only to observed container block activity. Cache, writeback, shared files and host processes prevent complete attribution.")
		}
	}
	_, err := tx.Exec(`INSERT INTO incidents VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET updated=excluded.updated,state=excluded.state,payload=excluded.payload`, inc.ID, inc.HostID, inc.EntityID, inc.Rule, Stamp(inc.Start), Stamp(inc.Updated), inc.State, JSON(inc))
	if err != nil {
		return err
	}
	resolution := 60
	if inc.Updated.Sub(inc.Start) > time.Hour {
		resolution = 900
	}
	_, err = tx.Exec(`INSERT OR REPLACE INTO incident_evidence SELECT ?,entity_id,resolution,bucket,generation,payload FROM metric_rollups WHERE host_id=? AND resolution=? AND bucket>=? AND bucket<=?`, inc.ID, inc.HostID, resolution, inc.Start.Add(-10*time.Minute).Unix(), inc.Updated.Unix())
	return err
}
func contributors(tx *sql.Tx, inc protocol.Incident) ([]protocol.Contributor, error) {
	rows, err := tx.Query(`SELECT e.id,e.name,COALESCE((SELECT metadata FROM entity_versions v WHERE v.host_id=m.host_id AND v.entity_id=m.entity_id AND v.at<=m.at ORDER BY v.at DESC LIMIT 1),e.metadata),m.at,m.interval,m.payload,m.generation FROM metric_samples m JOIN entities e ON e.host_id=m.host_id AND e.id=m.entity_id WHERE m.host_id=? AND e.kind='container_io' AND m.at>=? AND m.at<=? ORDER BY m.at`, inc.HostID, Stamp(inc.Start.Add(-10*time.Minute)), Stamp(inc.Updated))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	activities := map[string]*diagnostics.Activity{}
	for rows.Next() {
		var id, name, meta, at, payload, generation string
		var duration float64
		if err = rows.Scan(&id, &name, &meta, &at, &duration, &payload, &generation); err != nil {
			return nil, err
		}
		var metadata map[string]string
		if err = json.Unmarshal([]byte(meta), &metadata); err != nil {
			return nil, err
		}
		if metadata["mapping"] != "validated" {
			continue
		}
		key := id + "|" + generation
		a := activities[key]
		if a == nil {
			a = &diagnostics.Activity{Entity: protocol.Entity{ID: id, Name: name}}
			json.Unmarshal([]byte(meta), &a.Entity.Metadata)
			activities[key] = a
		}
		var v map[string]float64
		if err = json.Unmarshal([]byte(payload), &v); err != nil {
			return nil, err
		}
		rate := (v["disk.read_bytes"] + v["disk.write_bytes"]) / duration
		if ParseTime(at).Before(inc.Start) {
			a.Baseline = append(a.Baseline, rate)
			a.BaselineSeconds += duration
		} else {
			a.Current = append(a.Current, rate)
			a.CurrentSeconds += duration
			a.CurrentBytes += v["disk.read_bytes"] + v["disk.write_bytes"]
		}
	}
	list := []diagnostics.Activity{}
	for _, a := range activities {
		list = append(list, *a)
	}
	return diagnostics.Rank(list, inc.Updated.Sub(inc.Start).Seconds()), rows.Err()
}
func (s *Store) Tick(ctx context.Context, now time.Time) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		rows, err := tx.Query("SELECT host_id,payload FROM rule_state")
		if err != nil {
			return err
		}
		type item struct{ id, payload string }
		items := []item{}
		for rows.Next() {
			var i item
			if err = rows.Scan(&i.id, &i.payload); err != nil {
				rows.Close()
				return err
			}
			items = append(items, i)
		}
		rows.Close()
		for _, i := range items {
			var st diagnostics.State
			if err = json.Unmarshal([]byte(i.payload), &st); err != nil {
				return err
			}
			changes := diagnostics.Tick(&st, now)
			for _, inc := range changes {
				if err = saveIncident(tx, inc); err != nil {
					return err
				}
			}
			if len(changes) > 0 {
				if _, err = tx.Exec("UPDATE rule_state SET payload=? WHERE host_id=?", JSON(st), i.id); err != nil {
					return err
				}
			}
		}
		// Continue retaining the five-minute tail after closed incidents.
		_, err = tx.Exec(`INSERT OR IGNORE INTO incident_evidence SELECT i.id,r.entity_id,r.resolution,r.bucket,r.generation,r.payload FROM incidents i JOIN metric_rollups r ON r.host_id=i.host_id WHERE i.state<>'open' AND i.updated>=? AND r.resolution=60 AND r.bucket>=CAST(strftime('%s',i.updated) AS INTEGER) AND r.bucket<=CAST(strftime('%s',i.updated) AS INTEGER)+300`, Stamp(now.Add(-10*time.Minute)))
		return err
	})
}

type Retention struct {
	RawHours    int `json:"raw_hours"`
	MinuteDays  int `json:"minute_days"`
	QuarterDays int `json:"quarter_days"`
	EventDays   int `json:"event_days"`
}

func (s *Store) Settings(ctx context.Context) (Retention, error) {
	var r Retention
	var p string
	err := s.DB.QueryRowContext(ctx, "SELECT payload FROM settings WHERE id=1").Scan(&p)
	if err == nil {
		err = json.Unmarshal([]byte(p), &r)
	}
	return r, err
}
func (s *Store) SetSettings(ctx context.Context, r Retention) error {
	if r.RawHours < 1 || r.RawHours > 168 || r.MinuteDays < 1 || r.MinuteDays > 90 || r.QuarterDays < 1 || r.QuarterDays > 365 || r.EventDays < 1 || r.EventDays > 365 {
		return errors.New("invalid_retention")
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec("UPDATE settings SET payload=? WHERE id=1", JSON(r))
		if e == nil {
			e = Audit(tx, "retention_updated", "settings")
		}
		return e
	})
}
func (s *Store) Cleanup(ctx context.Context, now time.Time) error {
	r, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		queries := []struct {
			q    string
			args []any
		}{
			{"DELETE FROM metric_samples WHERE rowid IN(SELECT rowid FROM metric_samples WHERE at<? LIMIT 10000)", []any{Stamp(now.Add(-time.Duration(r.RawHours) * time.Hour))}},
			{"DELETE FROM metric_rollups WHERE rowid IN(SELECT rowid FROM metric_rollups WHERE resolution=60 AND bucket<? LIMIT 10000)", []any{now.Add(-time.Duration(r.MinuteDays) * 24 * time.Hour).Unix()}},
			{"DELETE FROM metric_rollups WHERE rowid IN(SELECT rowid FROM metric_rollups WHERE resolution=900 AND bucket<? LIMIT 10000)", []any{now.Add(-time.Duration(r.QuarterDays) * 24 * time.Hour).Unix()}},
			{"DELETE FROM incidents WHERE state<>'open' AND updated<?", []any{Stamp(now.Add(-time.Duration(r.EventDays) * 24 * time.Hour))}},
			{"DELETE FROM events WHERE rowid IN(SELECT rowid FROM events WHERE at<? LIMIT 10000)", []any{Stamp(now.Add(-time.Duration(r.EventDays) * 24 * time.Hour))}},
			{"DELETE FROM ingest_batches WHERE received<?", []any{Stamp(now.Add(-72 * time.Hour))}},
			{"DELETE FROM sessions WHERE expires<?", []any{Stamp(now)}},
			{"DELETE FROM enrollment_tokens WHERE expires<?", []any{Stamp(now.Add(-time.Hour))}},
			{"DELETE FROM audit_events WHERE at<?", []any{Stamp(now.Add(-180 * 24 * time.Hour))}},
			{"DELETE FROM entity_versions WHERE rowid IN(SELECT rowid FROM entity_versions WHERE at<? LIMIT 10000)", []any{Stamp(now.Add(-366 * 24 * time.Hour))}},
			{"DELETE FROM container_runs WHERE end IS NOT NULL AND end<?", []any{Stamp(now.Add(-366 * 24 * time.Hour))}},
			{"DELETE FROM entities WHERE last_seen<? AND NOT EXISTS(SELECT 1 FROM incidents i WHERE i.host_id=entities.host_id AND i.entity_id=entities.id AND i.state='open')", []any{Stamp(now.Add(-366 * 24 * time.Hour))}},
		}
		for _, q := range queries {
			if _, e := tx.Exec(q.q, q.args...); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Store) Backup(ctx context.Context, destination string) error {
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		return errors.New("backup_destination_exists")
	}
	info, err := os.Stat(s.Path)
	if err != nil {
		return err
	}
	free, err := platform.FreeSpace(filepath.Dir(destination))
	if err != nil {
		return err
	}
	if free < uint64(info.Size())+512<<20 {
		return errors.New("insufficient_backup_space")
	}
	privateFile, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = privateFile.Close(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.DB.ExecContext(ctx, "VACUUM INTO ?", destination)
	return err
}
func (s *Store) Health(ctx context.Context) map[string]any {
	info, _ := os.Stat(s.Path)
	var size int64
	if info != nil {
		size = info.Size()
	}
	wal, _ := os.Stat(s.Path + "-wal")
	var walSize int64
	if wal != nil {
		walSize = wal.Size()
	}
	var pages, free int64
	_ = s.DB.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages)
	_ = s.DB.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&free)
	return map[string]any{"database_bytes": size, "wal_bytes": walSize, "pages": pages, "free_pages": free, "storage": "SQLite WAL on a local filesystem", "schema": 1}
}
func (s *Store) Checkpoint(ctx context.Context) error {
	_, e := s.DB.ExecContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)")
	return e
}
func ErrorCode(err error) string {
	if errors.Is(err, ErrConflict) {
		return "sequence_conflict"
	}
	if errors.Is(err, ErrOrder) {
		return "out_of_order"
	}
	return fmt.Sprintf("storage_error")
}
