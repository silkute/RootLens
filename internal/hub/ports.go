package hub

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"rootlens/internal/agent"
	"strconv"
	"strings"
)

// NormalizeHTTPSOrigin matches browser origin serialization, including omission
// of HTTPS's default port. Origins never contain paths, credentials or queries.
func NormalizeHTTPSOrigin(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || strings.ToLower(u.Scheme) != "https" || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("invalid_https_origin")
	}
	host := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("invalid_https_port")
		}
		port = strconv.Itoa(n)
	}
	if port != "" && port != "443" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "https://" + host, nil
}

// SetPort changes saved local endpoints while the hub and agent are stopped.
// Certificates, bootstrap identity, accounts and agent credentials are untouched.
// Retry the same port after interruption to finish either configuration write.
func SetPort(data, output string, port int) (string, error) {
	if port < 1 || port > 65535 {
		return "", errors.New("port_must_be_between_1_and_65535")
	}
	path := filepath.Join(data, "hub.json")
	previous, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("initialize_hub_first")
	}
	var config Configuration
	if json.Unmarshal(previous, &config) != nil || config.BootstrapHash == "" {
		return "", errors.New("invalid_hub_configuration")
	}
	oldOrigin, err := NormalizeHTTPSOrigin(config.Origin)
	if err != nil {
		return "", err
	}
	u, _ := url.Parse(oldOrigin)
	u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(port))
	origin, err := NormalizeHTTPSOrigin(u.String())
	if err != nil {
		return "", err
	}
	var oldAgent []byte
	configPath := filepath.Join(output, "agent.json")
	var local agent.Config
	if output != "" {
		oldAgent, err = os.ReadFile(configPath)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if err == nil {
			if _, err = agent.LoadConfig(configPath); err != nil || json.Unmarshal(oldAgent, &local) != nil {
				return "", errors.New("invalid_agent_configuration")
			}
			agentOrigin, err := NormalizeHTTPSOrigin(local.Hub)
			if err != nil || (agentOrigin != oldOrigin && agentOrigin != origin) {
				return "", errors.New("local_agent_configuration_conflict")
			}
			local.Hub = origin
			if err = writeAgentConfig(configPath, local); err != nil {
				return "", err
			}
		}
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(previous, &fields)
	fields["origin"], _ = json.Marshal(origin)
	encoded, _ := json.MarshalIndent(fields, "", "  ")
	if err = privateWrite(path, append(encoded, '\n')); err != nil {
		if oldAgent != nil {
			if rollbackErr := privateWrite(configPath, oldAgent); rollbackErr != nil {
				return "", errors.New("port_change_incomplete_retry_same_port")
			}
		}
		return "", err
	}
	return origin, nil
}
