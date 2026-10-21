package hub

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"golang.org/x/crypto/argon2"
	"net"
	"net/http"
	"rootlens/internal/protocol"
	"rootlens/internal/storage"
	"strings"
	"sync"
	"time"
)

func Token() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func Hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func passwordHash(password string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return base64.RawStdEncoding.EncodeToString(salt) + "." + base64.RawStdEncoding.EncodeToString(key)
}
func passwordMatches(encoded, password string) bool {
	parts := strings.Split(encoded, ".")
	if len(parts) != 2 {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(parts[0])
	if e != nil || len(salt) != 16 {
		return false
	}
	want, e := base64.RawStdEncoding.DecodeString(parts[1])
	if e != nil || len(want) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return subtle.ConstantTimeCompare(want, got) == 1
}

type attempt struct {
	Count int
	Until time.Time
}
type limiter struct {
	mu    sync.Mutex
	ips   map[string]attempt
	slots chan struct{}
}

func newLimiter() *limiter { return &limiter{ips: map[string]attempt{}, slots: make(chan struct{}, 2)} }
func (l *limiter) acquire(addr string) bool {
	host, _, _ := net.SplitHostPort(addr)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for ip, a := range l.ips {
		if now.After(a.Until) {
			delete(l.ips, ip)
		}
	}
	a := l.ips[host]
	if a.Count >= 10 || len(l.ips) > 4096 {
		return false
	}
	select {
	case l.slots <- struct{}{}:
	default:
		return false
	}
	a.Count++
	if a.Until.IsZero() {
		a.Until = now.Add(time.Minute)
	}
	l.ips[host] = a
	return true
}
func (h *Hub) session(r *http.Request) (string, error) {
	cookie, e := r.Cookie("rootlens_session")
	if e != nil {
		return "", e
	}
	var csrf string
	e = h.Store.DB.QueryRowContext(r.Context(), "SELECT csrf FROM sessions WHERE hash=? AND expires>?", Hash(cookie.Value), storage.Stamp(time.Now())).Scan(&csrf)
	return csrf, e
}
func (h *Hub) originOK(r *http.Request) bool { return r.Header.Get("Origin") == h.Origin }
func (h *Hub) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Demo {
			if r.Method != "GET" {
				fail(w, 403, "demo_read_only", "The demonstration is read-only.")
				return
			}
			next(w, r)
			return
		}
		csrf, e := h.session(r)
		if e != nil {
			fail(w, 401, "unauthenticated", "Sign in to continue.")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if !h.originOK(r) || subtle.ConstantTimeCompare([]byte(csrf), []byte(r.Header.Get("X-CSRF-Token"))) != 1 {
				fail(w, 403, "csrf_failed", "Refresh this page before trying again.")
				return
			}
		}
		next(w, r)
	}
}
func (h *Hub) setup(w http.ResponseWriter, r *http.Request) {
	if !h.originOK(r) {
		fail(w, 403, "origin_mismatch", "Use the configured RootLens address.")
		return
	}
	if !h.limiter.acquire(r.RemoteAddr) {
		fail(w, 429, "rate_limited", "Wait before trying again.")
		return
	}
	defer func() { <-h.limiter.slots }()
	var input protocol.SetupRequest
	if !decode(w, r, &input, 4096) {
		return
	}
	if h.BootstrapHash == "" || subtle.ConstantTimeCompare([]byte(Hash(input.Token)), []byte(h.BootstrapHash)) != 1 {
		fail(w, 403, "invalid_setup_token", "The setup token is invalid.")
		return
	}
	if len(input.Username) < 1 || len(input.Username) > 64 || len(input.Password) < 12 || len(input.Password) > 128 {
		fail(w, 400, "invalid_account", "Use a username and a password of 12–128 characters.")
		return
	}
	hash := passwordHash(input.Password)
	e := h.Store.Write(r.Context(), func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow("SELECT COUNT(*) FROM users").Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return errors.New("already_initialized")
		}
		if _, err := tx.Exec("INSERT INTO users VALUES(1,?,?)", input.Username, hash); err != nil {
			return err
		}
		return storage.Audit(tx, "administrator_created", "local")
	})
	if e != nil {
		fail(w, 409, "already_initialized", "This instance has already been initialized.")
		return
	}
	writeJSON(w, 201, map[string]bool{"created": true})
}
func (h *Hub) login(w http.ResponseWriter, r *http.Request) {
	if !h.originOK(r) {
		fail(w, 403, "origin_mismatch", "Use the configured RootLens address.")
		return
	}
	if !h.limiter.acquire(r.RemoteAddr) {
		fail(w, 429, "rate_limited", "Wait before trying again.")
		return
	}
	defer func() { <-h.limiter.slots }()
	var input protocol.LoginRequest
	if !decode(w, r, &input, 4096) {
		return
	}
	if len(input.Password) > 128 {
		fail(w, 401, "invalid_credentials", "Invalid username or password.")
		return
	}
	var username, encoded string
	e := h.Store.DB.QueryRowContext(r.Context(), "SELECT username,password_hash FROM users WHERE id=1").Scan(&username, &encoded)
	if e != nil || !passwordMatches(encoded, input.Password) || input.Username != username {
		fail(w, 401, "invalid_credentials", "Invalid username or password.")
		return
	}
	token, csrf := Token(), Token()
	expiry := time.Now().Add(24 * time.Hour)
	e = h.Store.Write(r.Context(), func(tx *sql.Tx) error {
		if c, er := r.Cookie("rootlens_session"); er == nil {
			if _, er = tx.Exec("DELETE FROM sessions WHERE hash=?", Hash(c.Value)); er != nil {
				return er
			}
		}
		if _, er := tx.Exec("INSERT INTO sessions VALUES(?,?,?)", Hash(token), csrf, storage.Stamp(expiry)); er != nil {
			return er
		}
		return storage.Audit(tx, "session_created", "administrator")
	})
	if e != nil {
		storageFail(w)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "rootlens_session", Value: token, Path: "/", HttpOnly: true, Secure: !h.DevLoopback, SameSite: http.SameSiteStrictMode, Expires: expiry})
	writeJSON(w, 200, map[string]string{"csrf": csrf, "username": username})
}
func (h *Hub) logout(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie("rootlens_session")
	e := h.Store.Write(r.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec("DELETE FROM sessions WHERE hash=?", Hash(c.Value))
		return err
	})
	if e != nil {
		storageFail(w)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "rootlens_session", Path: "/", MaxAge: -1, Secure: !h.DevLoopback, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(204)
}
func (h *Hub) me(w http.ResponseWriter, r *http.Request) {
	if h.Demo {
		writeJSON(w, 200, map[string]any{"username": "Demo workspace", "demo": true, "csrf": ""})
		return
	}
	var count int
	_ = h.Store.DB.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM users").Scan(&count)
	csrf, e := h.session(r)
	if e != nil {
		writeJSON(w, 200, map[string]any{"authenticated": false, "setup_required": count == 0})
		return
	}
	var name string
	_ = h.Store.DB.QueryRowContext(r.Context(), "SELECT username FROM users WHERE id=1").Scan(&name)
	writeJSON(w, 200, map[string]any{"authenticated": true, "username": name, "csrf": csrf, "demo": false})
}
func AgentHost(ctx context.Context, s *storage.Store, bearer string) (string, error) {
	if len(bearer) != 43 {
		return "", errors.New("invalid_credential")
	}
	var id string
	e := s.DB.QueryRowContext(ctx, "SELECT host_id FROM agent_credentials WHERE hash=? AND revoked=0", Hash(bearer)).Scan(&id)
	return id, e
}
