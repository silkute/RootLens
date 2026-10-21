package hub

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"rootlens/internal/agent"
	"rootlens/internal/storage"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLocalAgentBootstrapEnrollmentAndCompletion(t *testing.T) {
	ctx := context.Background()
	data, output := t.TempDir(), t.TempDir()
	if err := Initialize(data, "https://localhost:8443", []string{"localhost"}); err != nil {
		t.Fatal(err)
	}
	if err := LocalAgent(ctx, data, output, "Test NAS", true, false, false); err != nil {
		t.Fatal(err)
	}
	config, err := agent.LoadConfig(filepath.Join(output, "agent.json"))
	if err != nil {
		t.Fatal(err)
	}
	if config.DockerSocket != "/run/rootlens/docker.sock" || config.CredentialFile != "/state/agent.token" {
		t.Fatal("unsupported container configuration")
	}
	token, err := os.ReadFile(filepath.Join(output, "enrollment.token"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := storage.Open(filepath.Join(data, "rootlens.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var stored, expires string
	if err = s.DB.QueryRow("SELECT hash,expires FROM enrollment_tokens WHERE host_id=?", config.HostID).Scan(&stored, &expires); err != nil {
		t.Fatal(err)
	}
	if stored != Hash(strings.TrimSpace(string(token))) || time.Until(storage.ParseTime(expires)) < 9*time.Minute || time.Until(storage.ParseTime(expires)) > 10*time.Minute {
		t.Fatal("enrollment must be hashed and expire within ten minutes")
	}
	if err = LocalAgent(ctx, data, output, "Test NAS", true, false, false); err != nil {
		t.Fatal(err)
	}
	refreshed, _ := os.ReadFile(filepath.Join(output, "enrollment.token"))
	if string(refreshed) == string(token) {
		t.Fatal("pending retry did not refresh token")
	}
	var hosts int
	s.DB.QueryRow("SELECT COUNT(*) FROM hosts").Scan(&hosts)
	if hosts != 1 {
		t.Fatal("retry duplicated local host")
	}
	if err = LocalAgent(ctx, data, output, "Test NAS", false, true, false); err == nil {
		t.Fatal("unavailable telemetry reported ready")
	}
	credential := Token()
	if err = s.Write(ctx, func(tx *sql.Tx) error {
		if _, e := tx.Exec("INSERT INTO agent_credentials VALUES(?,?,?,0,?)", "local-test", config.HostID, Hash(credential), storage.Stamp(time.Now())); e != nil {
			return e
		}
		_, e := tx.Exec("UPDATE hosts SET last_seen=? WHERE id=?", storage.Stamp(time.Now()), config.HostID)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(output, "agent.json"))
	if err = LocalAgent(ctx, data, output, "Renamed", false, false, false); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(output, "agent.json"))
	if string(before) != string(after) {
		t.Fatal("retry altered an enrolled configuration")
	}
	for i := 0; i < 2; i++ {
		if err = LocalAgent(ctx, data, output, "", false, false, true); err != nil {
			t.Fatal(err)
		}
	}
	config, err = agent.LoadConfig(filepath.Join(output, "agent.json"))
	if err != nil || config.EnrollmentFile != "" {
		t.Fatal("consumed enrollment remains configured")
	}
	if _, err = os.Stat(filepath.Join(output, "enrollment.token")); !os.IsNotExist(err) {
		t.Fatal("consumed token remains on disk")
	}
	if runtime.GOOS != "windows" {
		for _, file := range []string{"agent.json", "ca.pem"} {
			info, err := os.Stat(filepath.Join(output, file))
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("agent output is not private")
			}
		}
	}
	s.DB.Exec("UPDATE hosts SET last_seen=?", storage.Stamp(time.Now().Add(-time.Minute)))
	if err = LocalAgent(ctx, data, output, "", false, true, false); err == nil {
		t.Fatal("stale telemetry accepted")
	}
}

func TestLocalAgentCannotReopenBootstrapOrOverwriteOtherHost(t *testing.T) {
	ctx := context.Background()
	t.Run("administrator exists", func(t *testing.T) {
		data, output := t.TempDir(), t.TempDir()
		if err := Initialize(data, "https://localhost:8443", []string{"localhost"}); err != nil {
			t.Fatal(err)
		}
		s, err := storage.Open(filepath.Join(data, "rootlens.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if _, err = s.DB.Exec("INSERT INTO users VALUES(1,'owner','test-hash')"); err != nil {
			t.Fatal(err)
		}
		if err = LocalAgent(ctx, data, output, "NAS", false, false, false); err == nil {
			t.Fatal("local CLI provisioned after administrator setup")
		}
		var count int
		s.DB.QueryRow("SELECT COUNT(*) FROM hosts").Scan(&count)
		if count != 0 {
			t.Fatal("failed bootstrap left a host")
		}
		if _, err = os.Stat(filepath.Join(output, "agent.json")); !os.IsNotExist(err) {
			t.Fatal("failed bootstrap wrote configuration")
		}
	})
	t.Run("mismatched host and missing secret", func(t *testing.T) {
		data, output := t.TempDir(), t.TempDir()
		Initialize(data, "https://localhost:8443", []string{"localhost"})
		foreign := []byte(`{"host_id":"other-host","hub":"https://localhost:8443"}`)
		os.WriteFile(filepath.Join(output, "agent.json"), foreign, 0600)
		if err := LocalAgent(ctx, data, output, "NAS", false, false, false); err == nil {
			t.Fatal("foreign configuration overwritten")
		}
		got, _ := os.ReadFile(filepath.Join(output, "agent.json"))
		if string(got) != string(foreign) {
			t.Fatal("foreign configuration changed")
		}
		os.Remove(filepath.Join(output, "agent.json"))
		os.WriteFile(filepath.Join(data, "setup.token"), []byte("wrong secret"), 0600)
		if err := LocalAgent(ctx, data, output, "NAS", false, false, false); err == nil {
			t.Fatal("bootstrap without matching secret accepted")
		}
	})
}
