package hub

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"rootlens/internal/agent"
	"rootlens/internal/storage"
	"strings"
	"testing"
)

func TestHTTPSOriginPorts(t *testing.T) {
	for input, expected := range map[string]string{
		"https://NAS.example:443/": "https://nas.example",
		"https://nas.example:9443": "https://nas.example:9443",
		"https://[::1]:443":        "https://[::1]",
		"https://[::1]:9443":       "https://[::1]:9443",
	} {
		got, err := NormalizeHTTPSOrigin(input)
		if err != nil || got != expected {
			t.Fatalf("origin %q: got %q, error %v", input, got, err)
		}
	}
	for _, input := range []string{"http://localhost:9443", "https://localhost:0", "https://localhost:65536", "https://owner:secret@localhost", "https://localhost/path", "https://localhost?token=secret"} {
		if _, err := NormalizeHTTPSOrigin(input); err == nil {
			t.Fatalf("invalid origin accepted: %q", input)
		}
	}
}

func TestPort443AcceptsBrowserOrigin(t *testing.T) {
	data := t.TempDir()
	if err := Initialize(data, "https://localhost:443", []string{"localhost"}); err != nil {
		t.Fatal(err)
	}
	var config Configuration
	encoded, _ := os.ReadFile(filepath.Join(data, "hub.json"))
	json.Unmarshal(encoded, &config)
	if config.Origin != "https://localhost" {
		t.Fatal("default HTTPS port must be omitted from the origin")
	}
	s, err := storage.Open(filepath.Join(data, "rootlens.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := New(s, config.Origin)
	h.BootstrapHash = config.BootstrapHash
	server := httptest.NewTLSServer(h.Handler())
	defer server.Close()
	secret, _ := os.ReadFile(filepath.Join(data, "setup.token"))
	resp := request(t, server, "POST", "/api/v1/setup", "https://localhost", "", map[string]string{"token": strings.TrimSpace(string(secret)), "username": "owner", "password": "long-private-password"}, nil, "")
	defer closeResponse(resp)
	if resp.StatusCode != 201 {
		t.Fatal("browser account setup failed on the default HTTPS port")
	}
}

func TestSetPortPreservesIdentityAndRecoversPendingWrite(t *testing.T) {
	data, output := t.TempDir(), t.TempDir()
	if err := Initialize(data, "https://localhost:8443", []string{"localhost"}); err != nil {
		t.Fatal(err)
	}
	original := agent.Config{Hub: "https://localhost:8443", HostID: "test-host", CAFile: "/etc/rootlens/ca.pem", CredentialFile: "/state/agent.token", Sampling: 10, DockerSocket: "/run/rootlens/docker.sock", Filesystems: []string{"/host/data"}}
	path := filepath.Join(output, "agent.json")
	if err := writeAgentConfig(path, original); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	for _, file := range []string{"ca.pem", "ca.key", "server.pem", "server.key", "setup.token"} {
		b, _ := os.ReadFile(filepath.Join(data, file))
		before[file] = string(b)
	}
	for _, port := range []int{9443, 443, 443, 8443} {
		origin, err := SetPort(data, output, port)
		if err != nil {
			t.Fatal(err)
		}
		var updated agent.Config
		encoded, _ := os.ReadFile(path)
		json.Unmarshal(encoded, &updated)
		if updated.Hub != origin {
			t.Fatal("agent endpoint did not follow the hub")
		}
		updated.Hub = original.Hub
		if !reflect.DeepEqual(updated, original) {
			t.Fatal("port change altered unrelated agent configuration")
		}
	}
	// An interrupted write can leave the agent at the requested port before the
	// hub configuration is committed. Retrying that same port must finish it.
	pending := original
	pending.Hub = "https://localhost:9443"
	writeAgentConfig(path, pending)
	if _, err := SetPort(data, output, 9443); err != nil {
		t.Fatal(err)
	}
	for file, expected := range before {
		b, _ := os.ReadFile(filepath.Join(data, file))
		if string(b) != expected {
			t.Fatalf("port change replaced %s", file)
		}
	}
	pending.Hub = "https://other-host:9443"
	writeAgentConfig(path, pending)
	if _, err := SetPort(data, output, 10443); err == nil {
		t.Fatal("unrelated agent was retargeted")
	}
	for _, invalid := range []int{-1, 0, 65536} {
		if _, err := SetPort(data, output, invalid); err == nil {
			t.Fatal("invalid port accepted")
		}
	}
}
