package hub

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"rootlens/internal/agent"
	"rootlens/internal/protocol"
	"rootlens/internal/storage"
	"testing"
	"time"
)

func TestEnableDockerPreservesExistingAgent(t *testing.T) {
	output := t.TempDir()
	path := filepath.Join(output, "agent.json")
	before := agent.Config{Hub: "https://localhost:9443", HostID: "existing-host", CAFile: "/etc/rootlens/ca.pem", CredentialFile: "/state/agent.token", Sampling: 10, Proc: "/host/proc", Sys: "/host/sys", Cgroup: "/host/cgroup", Filesystems: []string{"/host/data"}, CollectProcesses: true}
	if err := writeAgentConfig(path, before); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := EnableDocker(output); err != nil {
			t.Fatal(err)
		}
	}
	encoded, _ := os.ReadFile(path)
	var after agent.Config
	json.Unmarshal(encoded, &after)
	if after.DockerSocket != "/run/rootlens/docker.sock" {
		t.Fatal("restricted proxy was not configured")
	}
	after.DockerSocket = ""
	if !reflect.DeepEqual(before, after) {
		t.Fatal("enabling Docker changed unrelated configuration")
	}
	before.DockerSocket = "/custom/docker.sock"
	writeAgentConfig(path, before)
	if err := EnableDocker(output); err == nil {
		t.Fatal("custom Docker endpoint silently replaced")
	}
	if err := EnableDocker(t.TempDir()); err == nil {
		t.Fatal("missing configuration accepted")
	}
}

func TestCheckDockerRequiresRecentAvailableCollector(t *testing.T) {
	ctx := context.Background()
	data, output := t.TempDir(), t.TempDir()
	if err := Initialize(data, "https://localhost:9443", []string{"localhost"}); err != nil {
		t.Fatal(err)
	}
	if err := LocalAgent(ctx, data, output, "NAS", false, false, false); err != nil {
		t.Fatal(err)
	}
	if err := CheckDocker(ctx, data, output); err == nil {
		t.Fatal("disabled Docker reported ready")
	}
	if err := EnableDocker(output); err != nil {
		t.Fatal(err)
	}
	s, err := storage.Open(filepath.Join(data, "rootlens.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = CheckDocker(ctx, data, output); err == nil {
		t.Fatal("missing telemetry reported ready")
	}
	caps := map[string]protocol.Capability{"docker": {Status: "available", At: time.Now()}}
	if _, err = s.DB.Exec("UPDATE hosts SET last_seen=?,capabilities=?", storage.Stamp(time.Now()), storage.JSON(caps)); err != nil {
		t.Fatal(err)
	}
	if err = CheckDocker(ctx, data, output); err != nil {
		t.Fatal("healthy empty Docker inventory must be allowed:", err)
	}
	s.DB.Exec("UPDATE hosts SET last_seen=?", storage.Stamp(time.Now().Add(-time.Minute)))
	if err = CheckDocker(ctx, data, output); err == nil {
		t.Fatal("stale telemetry reported ready")
	}
	caps["docker"] = protocol.Capability{Status: "error", At: time.Now()}
	s.DB.Exec("UPDATE hosts SET last_seen=?,capabilities=?", storage.Stamp(time.Now()), storage.JSON(caps))
	if err = CheckDocker(ctx, data, output); err == nil {
		t.Fatal("failed collector reported ready")
	}
}
