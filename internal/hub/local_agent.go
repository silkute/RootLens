package hub

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"rootlens/internal/agent"
	"rootlens/internal/storage"
	"strings"
	"time"
)

// LocalAgent provisions only the first, colocated agent before administrator
// setup. It is an offline CLI operation requiring access to the hub's private
// data directory, never an HTTP endpoint. Agents still enroll over verified TLS.
func LocalAgent(ctx context.Context, data, output, name string, docker, check, finish bool) error {
	encoded, err := os.ReadFile(filepath.Join(data, "hub.json"))
	if err != nil {
		return errors.New("initialize_hub_first")
	}
	var hubConfig Configuration
	if json.Unmarshal(encoded, &hubConfig) != nil || hubConfig.BootstrapHash == "" {
		return errors.New("invalid_hub_configuration")
	}
	id := Hash("local-agent:" + hubConfig.BootstrapHash)[:24]
	s, err := storage.Open(filepath.Join(data, "rootlens.db"))
	if err != nil {
		return err
	}
	defer s.Close()
	configPath := filepath.Join(output, "agent.json")
	var config agent.Config
	previous, err := os.ReadFile(configPath)
	if err == nil {
		if json.Unmarshal(previous, &config) != nil || config.HostID != id || config.Hub != hubConfig.Origin {
			return errors.New("local_agent_configuration_conflict")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	var enrolled int
	if err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_credentials WHERE host_id=? AND revoked=0", id).Scan(&enrolled); err != nil {
		return err
	}
	if check || finish {
		var seen sql.NullString
		if enrolled == 0 || config.HostID != id {
			return errors.New("local_agent_not_enrolled")
		}
		if err = s.DB.QueryRowContext(ctx, "SELECT last_seen FROM hosts WHERE id=?", id).Scan(&seen); err != nil {
			return err
		}
		if !seen.Valid || time.Since(storage.ParseTime(seen.String)) > 30*time.Second {
			return errors.New("waiting_for_local_agent_telemetry")
		}
		if finish {
			config.EnrollmentFile = ""
			if err = writeAgentConfig(configPath, config); err != nil {
				return err
			}
			if err = os.Remove(filepath.Join(output, "enrollment.token")); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		return nil
	}
	if enrolled > 0 {
		if config.HostID == id {
			return nil // A retry must never replace an enrolled credential/configuration.
		}
		return errors.New("local_agent_already_enrolled")
	}
	if len(strings.TrimSpace(name)) == 0 || len(name) > 100 {
		return errors.New("invalid_local_host_name")
	}
	bootstrap, err := os.ReadFile(filepath.Join(data, "setup.token"))
	if err != nil || subtle.ConstantTimeCompare([]byte(Hash(strings.TrimSpace(string(bootstrap)))), []byte(hubConfig.BootstrapHash)) != 1 {
		return errors.New("local_agent_requires_bootstrap_secret")
	}
	if err = os.MkdirAll(output, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(output)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("local_agent_output_must_be_directory")
	}
	ca, err := os.ReadFile(filepath.Join(data, "ca.pem"))
	if err != nil {
		return err
	}
	config = agent.Config{Hub: hubConfig.Origin, HostID: id, CAFile: "/etc/rootlens/ca.pem", CredentialFile: "/state/agent.token", EnrollmentFile: "/etc/rootlens/enrollment.token", Sampling: 5, Proc: "/host/proc", Sys: "/host/sys", Cgroup: "/host/cgroup"}
	if docker {
		config.DockerSocket = "/run/rootlens/docker.sock"
	}
	token := Token()
	return s.Write(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("local_agent_bootstrap_closed_use_settings")
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO hosts(id,name) VALUES(?,?) ON CONFLICT(id) DO NOTHING", id, name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM enrollment_tokens WHERE host_id=?", id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO enrollment_tokens(hash,host_id,expires) VALUES(?,?,?)", Hash(token), id, storage.Stamp(time.Now().Add(10*time.Minute))); err != nil {
			return err
		}
		for path, contents := range map[string][]byte{"ca.pem": ca, "enrollment.token": []byte(token + "\n")} {
			if err := privateWrite(filepath.Join(output, path), contents); err != nil {
				return err
			}
		}
		if err := writeAgentConfig(configPath, config); err != nil {
			return err
		}
		return storage.Audit(tx, "local_agent_bootstrap", id)
	})
}

func writeAgentConfig(path string, config agent.Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return privateWrite(path, append(data, '\n'))
}

func privateWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".rootlens-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
