package hub

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"rootlens/internal/agent"
	"rootlens/internal/protocol"
	"rootlens/internal/storage"
	"time"
)

// EnableDocker configures an existing local agent for the restricted proxy.
// The caller stops the agent and explicitly opts into deploying that proxy.
func EnableDocker(output string) error {
	path := filepath.Join(output, "agent.json")
	config, err := agent.LoadConfig(path)
	if err != nil {
		return err
	}
	const socket = "/run/rootlens/docker.sock"
	if config.DockerSocket == socket {
		return nil
	}
	if config.DockerSocket != "" {
		return errors.New("custom_docker_socket_requires_local_configuration")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &fields); err != nil {
		return err
	}
	fields["docker_socket"], _ = json.Marshal(socket)
	encoded, err = json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	return privateWrite(path, append(encoded, '\n'))
}

// CheckDocker verifies recent, committed collector health for the configured host.
// An empty inventory can be valid; availability is not inferred from a row count.
func CheckDocker(ctx context.Context, data, output string) error {
	config, err := agent.LoadConfig(filepath.Join(output, "agent.json"))
	if err != nil {
		return err
	}
	if config.DockerSocket != "/run/rootlens/docker.sock" {
		return errors.New("docker_monitoring_not_configured")
	}
	s, err := storage.Open(filepath.Join(data, "rootlens.db"))
	if err != nil {
		return err
	}
	defer s.Close()
	var seen sql.NullString
	var encoded string
	if err = s.DB.QueryRowContext(ctx, "SELECT last_seen,capabilities FROM hosts WHERE id=?", config.HostID).Scan(&seen, &encoded); err != nil {
		return err
	}
	var caps map[string]protocol.Capability
	if json.Unmarshal([]byte(encoded), &caps) != nil || !seen.Valid || time.Since(storage.ParseTime(seen.String)) > 30*time.Second || caps["docker"].Status != "available" {
		return errors.New("docker_collector_not_ready")
	}
	return nil
}
