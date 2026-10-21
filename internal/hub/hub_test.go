package hub

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"rootlens/internal/protocol"
	"rootlens/internal/storage"
	"strings"
	"testing"
	"time"
)

func testHub(t *testing.T) (*Hub, *httptest.Server) {
	t.Helper()
	s, e := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	h := New(s, "https://example.test")
	h.BootstrapHash = Hash("setup-secret")
	server := httptest.NewTLSServer(h.Handler())
	t.Cleanup(func() { server.Close(); s.Close() })
	return h, server
}
func request(t *testing.T, server *httptest.Server, method, path, origin, bearer string, body any, cookie *http.Cookie, csrf string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, server.URL+path, reader)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, e := server.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	return resp
}
func closeResponse(r *http.Response) { io.Copy(io.Discard, r.Body); r.Body.Close() }
func TestAuthenticationSessionAndCSRF(t *testing.T) {
	h, s := testHub(t)
	setup := map[string]string{"token": "setup-secret", "username": "owner", "password": "long-private-password"}
	resp := request(t, s, "POST", "/api/v1/setup", "https://evil.test", "", setup, nil, "")
	if resp.StatusCode != 403 {
		t.Fatal("cross-origin setup accepted")
	}
	closeResponse(resp)
	resp = request(t, s, "POST", "/api/v1/setup", h.Origin, "", setup, nil, "")
	if resp.StatusCode != 201 {
		t.Fatal("setup failed")
	}
	closeResponse(resp)
	resp = request(t, s, "POST", "/api/v1/session", h.Origin, "", map[string]string{"username": "owner", "password": "long-private-password"}, nil, "")
	if resp.StatusCode != 200 {
		t.Fatal("login failed")
	}
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	cookies := resp.Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe cookie")
	}
	resp = request(t, s, "POST", "/api/v1/hosts", h.Origin, "", map[string]string{"name": "NAS"}, cookies[0], "")
	if resp.StatusCode != 403 {
		t.Fatal("missing CSRF accepted")
	}
	closeResponse(resp)
	resp = request(t, s, "POST", "/api/v1/hosts", h.Origin, "", map[string]string{"name": "NAS"}, cookies[0], result["csrf"])
	if resp.StatusCode != 201 {
		t.Fatal("valid authenticated mutation failed")
	}
	closeResponse(resp)
	resp = request(t, s, "DELETE", "/api/v1/session", h.Origin, "", nil, cookies[0], result["csrf"])
	if resp.StatusCode != 204 {
		t.Fatal("logout failed")
	}
	closeResponse(resp)
	resp = request(t, s, "GET", "/api/v1/hosts", "", "", nil, cookies[0], "")
	if resp.StatusCode != 401 {
		t.Fatal("revoked session valid")
	}
	closeResponse(resp)
}
func TestEnrollmentSingleUseHostOwnershipAndRevocation(t *testing.T) {
	h, s := testHub(t)
	token, credential := Token(), Token()
	e := h.Store.Write(context.Background(), func(tx *sql.Tx) error {
		tx.Exec("INSERT INTO hosts(id,name) VALUES('a','A'),('b','B')")
		_, e := tx.Exec("INSERT INTO enrollment_tokens VALUES(?,?,?,0)", Hash(token), "a", storage.Stamp(time.Now().Add(time.Minute)))
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	input := map[string]string{"host_id": "a", "token": token, "credential": credential}
	resp := request(t, s, "POST", "/api/v1/agent/enroll", "", "", input, nil, "")
	if resp.StatusCode != 201 {
		t.Fatal("enrollment failed")
	}
	closeResponse(resp)
	resp = request(t, s, "POST", "/api/v1/agent/enroll", "", "", input, nil, "")
	if resp.StatusCode != 403 {
		t.Fatal("enrollment replay accepted")
	}
	closeResponse(resp)
	b := protocol.Batch{HostID: "b"}
	resp = request(t, s, "POST", "/api/v1/agent/batches", "", credential, b, nil, "")
	if resp.StatusCode != 403 {
		t.Fatal("cross-host ingestion accepted")
	}
	closeResponse(resp)
	resp = request(t, s, "GET", "/api/v1/hosts", "", credential, nil, nil, "")
	if resp.StatusCode != 401 {
		t.Fatal("agent became administrator")
	}
	closeResponse(resp)
	h.Store.DB.Exec("UPDATE agent_credentials SET revoked=1")
	resp = request(t, s, "POST", "/api/v1/agent/batches", "", credential, b, nil, "")
	if resp.StatusCode != 401 {
		t.Fatal("revoked credential accepted")
	}
	closeResponse(resp)
}
func TestDemoReadOnlyAndSecurityHeaders(t *testing.T) {
	h, s := testHub(t)
	h.Demo = true
	resp := request(t, s, "PATCH", "/api/v1/settings", h.Origin, "", map[string]int{"raw_hours": 1}, nil, "")
	if resp.StatusCode != 403 {
		t.Fatal("demo mutation accepted")
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("missing CSP")
	}
	closeResponse(resp)
}
func TestExpiredEnrollmentRejected(t *testing.T) {
	h, s := testHub(t)
	token := Token()
	h.Store.DB.Exec("INSERT INTO hosts(id,name) VALUES('h','Host')")
	h.Store.DB.Exec("INSERT INTO enrollment_tokens VALUES(?,?,?,0)", Hash(token), "h", storage.Stamp(time.Now().Add(-time.Minute)))
	resp := request(t, s, "POST", "/api/v1/agent/enroll", "", "", map[string]string{"host_id": "h", "token": token, "credential": Token()}, nil, "")
	if resp.StatusCode != 403 {
		t.Fatal("expired token accepted")
	}
	closeResponse(resp)
}
