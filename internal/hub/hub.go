package hub

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"rootlens/internal/diagnostics"
	"rootlens/internal/platform"
	"rootlens/internal/protocol"
	"rootlens/internal/storage"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed static
var assets embed.FS
var compressedAssets sync.Map

type Hub struct {
	Store             *storage.Store
	Origin            string
	BootstrapHash     string
	Demo, DevLoopback bool
	limiter           *limiter
	ingest            chan struct{}
	processMu         sync.RWMutex
	processes         map[string]protocol.ProcessSnapshot
}

func New(s *storage.Store, origin string) *Hub {
	return &Hub{Store: s, Origin: origin, limiter: newLimiter(), ingest: make(chan struct{}, 8), processes: map[string]protocol.ProcessSnapshot{}}
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}
func storageFail(w http.ResponseWriter) {
	fail(w, 503, "storage_unavailable", "Storage is temporarily unavailable. No data was acknowledged.")
}
func decode(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		fail(w, 400, "invalid_request", "Request body is invalid or too large.")
		return false
	}
	if e := d.Decode(&struct{}{}); e != io.EOF {
		fail(w, 400, "invalid_request", "Expected one JSON object.")
		return false
	}
	return true
}
func (h *Hub) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/v1/me", h.me)
	if !h.Demo {
		m.HandleFunc("POST /api/v1/setup", h.setup)
		m.HandleFunc("POST /api/v1/session", h.login)
		m.HandleFunc("DELETE /api/v1/session", h.admin(h.logout))
		m.HandleFunc("POST /api/v1/agent/enroll", h.enroll)
		m.HandleFunc("POST /api/v1/agent/batches", h.batch)
	}
	m.HandleFunc("GET /api/v1/hosts", h.admin(h.hosts))
	m.HandleFunc("POST /api/v1/hosts", h.admin(h.createHost))
	m.HandleFunc("GET /api/v1/hosts/{id}", h.admin(h.host))
	m.HandleFunc("POST /api/v1/hosts/{id}/enrollment-tokens", h.admin(h.enrollmentToken))
	m.HandleFunc("GET /api/v1/hosts/{id}/credentials", h.admin(h.credentials))
	m.HandleFunc("DELETE /api/v1/hosts/{id}/credentials/{credential}", h.admin(h.revoke))
	m.HandleFunc("POST /api/v1/hosts/{id}/credentials/rotate", h.admin(h.rotate))
	m.HandleFunc("GET /api/v1/hosts/{id}/metrics", h.admin(h.metrics))
	m.HandleFunc("GET /api/v1/hosts/{id}/containers", h.admin(h.containers))
	m.HandleFunc("GET /api/v1/containers/{id}", h.admin(h.container))
	m.HandleFunc("GET /api/v1/hosts/{id}/processes", h.admin(h.processSummary))
	m.HandleFunc("GET /api/v1/incidents", h.admin(h.incidents))
	m.HandleFunc("GET /api/v1/incidents/{id}", h.admin(h.incident))
	m.HandleFunc("GET /api/v1/incidents/{id}/report", h.admin(h.report))
	m.HandleFunc("GET /api/v1/incidents/{id}/evidence", h.admin(h.evidence))
	m.HandleFunc("GET /api/v1/events", h.admin(h.events))
	m.HandleFunc("GET /api/v1/hosts/{id}/diagnostics", h.admin(h.diagnostics))
	m.HandleFunc("POST /api/v1/hosts/{id}/investigations", h.admin(h.investigate))
	m.HandleFunc("GET /api/v1/settings", h.admin(h.settings))
	m.HandleFunc("PATCH /api/v1/settings", h.admin(h.updateSettings))
	m.HandleFunc("GET /api/v1/health/storage", h.admin(h.health))
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); _, _ = w.Write([]byte("ok\n")) })
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "not_found", "Endpoint not found.") })
	static, _ := fs.Sub(assets, "static")
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "." || name == "" {
			name = "index.html"
		}
		file, e := static.Open(name)
		if e != nil {
			if strings.HasPrefix(name, "assets/") {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
			file, e = static.Open(name)
		}
		if e != nil {
			http.Error(w, "Frontend has not been built.", 503)
			return
		}
		defer file.Close()
		b, e := io.ReadAll(file)
		if e != nil {
			http.Error(w, "Asset unavailable", 500)
			return
		}
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public,max-age=31536000,immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if (strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".css") || strings.HasSuffix(name, ".html")) && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") && r.Header.Get("Range") == "" {
			cached, ok := compressedAssets.Load(name)
			if !ok {
				var buf bytes.Buffer
				zipper := gzip.NewWriter(&buf)
				_, _ = zipper.Write(b)
				_ = zipper.Close()
				cached, _ = compressedAssets.LoadOrStore(name, buf.Bytes())
			}
			encoded := cached.([]byte)
			contentType := "text/html; charset=utf-8"
			if strings.HasSuffix(name, ".js") {
				contentType = "text/javascript; charset=utf-8"
			} else if strings.HasSuffix(name, ".css") {
				contentType = "text/css; charset=utf-8"
			}
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Vary", "Accept-Encoding")
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
			if r.Method != "HEAD" {
				_, _ = w.Write(encoded)
			}
			return
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		m.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (h *Hub) hosts(w http.ResponseWriter, r *http.Request) {
	v, e := h.Store.Hosts(r.Context())
	if e != nil {
		storageFail(w)
		return
	}
	writeJSON(w, 200, v)
}
func (h *Hub) host(w http.ResponseWriter, r *http.Request) {
	v, e := h.Store.Hosts(r.Context())
	if e != nil {
		storageFail(w)
		return
	}
	for _, host := range v {
		if host.ID == r.PathValue("id") {
			writeJSON(w, 200, host)
			return
		}
	}
	fail(w, 404, "host_not_found", "Host not found.")
}
func (h *Hub) createHost(w http.ResponseWriter, r *http.Request) {
	var in protocol.CreateHostRequest
	if !decode(w, r, &in, 1024) {
		return
	}
	if len(strings.TrimSpace(in.Name)) < 1 || len(in.Name) > 100 {
		fail(w, 400, "invalid_name", "Provide a host name of 1–100 characters.")
		return
	}
	id := Hash(Token())[:24]
	e := h.Store.Write(r.Context(), func(tx *sql.Tx) error {
		var n int
		if e := tx.QueryRow("SELECT COUNT(*) FROM hosts").Scan(&n); e != nil {
			return e
		}
		if n >= 100 {
			return errors.New("host_limit")
		}
		if _, e := tx.Exec("INSERT INTO hosts(id,name) VALUES(?,?)", id, in.Name); e != nil {
			return e
		}
		return storage.Audit(tx, "host_created", id)
	})
	if e != nil {
		storageFail(w)
		return
	}
	writeJSON(w, 201, map[string]string{"id": id, "name": in.Name})
}
func (h *Hub) enrollmentToken(w http.ResponseWriter, r *http.Request) {
	token := Token()
	expires := time.Now().Add(10 * time.Minute)
	e := h.Store.Write(r.Context(), func(tx *sql.Tx) error {
		var n int
		if e := tx.QueryRow("SELECT COUNT(*) FROM agent_credentials WHERE host_id=? AND revoked=0", r.PathValue("id")).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return errors.New("already_enrolled")
		}
		if _, e := tx.Exec("DELETE FROM enrollment_tokens WHERE host_id=?", r.PathValue("id")); e != nil {
			return e
		}
		if _, e := tx.Exec("INSERT INTO enrollment_tokens(hash,host_id,expires) VALUES(?,?,?)", Hash(token), r.PathValue("id"), storage.Stamp(expires)); e != nil {
			return e
		}
		return storage.Audit(tx, "enrollment_created", r.PathValue("id"))
	})
	if e != nil {
		fail(w, 409, "enrollment_unavailable", "Revoke existing agent credentials before enrolling a replacement.")
		return
	}
	writeJSON(w, 201, map[string]any{"token": token, "host_id": r.PathValue("id"), "expires": expires})
}
func (h *Hub) enroll(w http.ResponseWriter, r *http.Request) {
	if !h.limiter.acquire(r.RemoteAddr) {
		fail(w, 429, "rate_limited", "Try again later.")
		return
	}
	defer func() { <-h.limiter.slots }()
	var in protocol.EnrollmentRequest
	if !decode(w, r, &in, 4096) {
		return
	}
	if len(in.Credential) != 43 || len(in.Token) != 43 {
		fail(w, 400, "invalid_credential", "Credentials must be 256-bit URL-safe tokens.")
		return
	}
	e := h.Store.Write(r.Context(), func(tx *sql.Tx) error {
		var host string
		var consumed int
		var expires string
		if e := tx.QueryRow("SELECT host_id,expires,consumed FROM enrollment_tokens WHERE hash=?", Hash(in.Token)).Scan(&host, &expires, &consumed); e != nil {
			return e
		}
		if host != in.HostID || consumed != 0 || !storage.ParseTime(expires).After(time.Now()) {
			return errors.New("invalid_token")
		}
		var n int
		if e := tx.QueryRow("SELECT COUNT(*) FROM agent_credentials WHERE host_id=? AND revoked=0", host).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return errors.New("already_enrolled")
		}
		if _, e := tx.Exec("INSERT INTO agent_credentials VALUES(?,?,?,0,?)", Hash(in.Credential)[:24], host, Hash(in.Credential), storage.Stamp(time.Now())); e != nil {
			return e
		}
		if _, e := tx.Exec("UPDATE enrollment_tokens SET consumed=1 WHERE hash=?", Hash(in.Token)); e != nil {
			return e
		}
		return storage.Audit(tx, "agent_enrolled", host)
	})
	if e != nil {
		fail(w, 403, "enrollment_failed", "Enrollment is expired, consumed, or invalid. Existing credentials can still be used to retry uploads.")
		return
	}
	writeJSON(w, 201, map[string]string{"host_id": in.HostID})
}
func (h *Hub) credentials(w http.ResponseWriter, r *http.Request) {
	rows, e := h.Store.DB.QueryContext(r.Context(), "SELECT id,created,revoked FROM agent_credentials WHERE host_id=? ORDER BY created DESC", r.PathValue("id"))
	if e != nil {
		storageFail(w)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, created string
		var revoked int
		if e = rows.Scan(&id, &created, &revoked); e != nil {
			storageFail(w)
			return
		}
		out = append(out, map[string]any{"id": id, "created": created, "revoked": revoked != 0})
	}
	writeJSON(w, 200, out)
}
func (h *Hub) revoke(w http.ResponseWriter, r *http.Request) {
	e := h.Store.Write(r.Context(), func(tx *sql.Tx) error {
		_, e := tx.Exec("UPDATE agent_credentials SET revoked=1 WHERE host_id=? AND id=?", r.PathValue("id"), r.PathValue("credential"))
		if e == nil {
			e = storage.Audit(tx, "agent_revoked", r.PathValue("id"))
		}
		return e
	})
	if e != nil {
		storageFail(w)
		return
	}
	w.WriteHeader(204)
}
func (h *Hub) rotate(w http.ResponseWriter, r *http.Request) {
	token := Token()
	e := h.Store.Write(r.Context(), func(tx *sql.Tx) error {
		if _, e := tx.Exec("UPDATE agent_credentials SET revoked=1 WHERE host_id=?", r.PathValue("id")); e != nil {
			return e
		}
		if _, e := tx.Exec("INSERT INTO agent_credentials VALUES(?,?,?,0,?)", Hash(token)[:24], r.PathValue("id"), Hash(token), storage.Stamp(time.Now())); e != nil {
			return e
		}
		return storage.Audit(tx, "agent_rotated", r.PathValue("id"))
	})
	if e != nil {
		storageFail(w)
		return
	}
	writeJSON(w, 201, map[string]string{"credential": token, "instruction": "Replace the agent credential file locally, then restart the agent. Old credentials are revoked immediately."})
}
func (h *Hub) batch(w http.ResponseWriter, r *http.Request) {
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	host, e := AgentHost(r.Context(), h.Store, bearer)
	if e != nil {
		fail(w, 401, "invalid_agent_credential", "Agent credential is invalid or revoked.")
		return
	}
	select {
	case h.ingest <- struct{}{}:
		defer func() { <-h.ingest }()
	default:
		w.Header().Set("Retry-After", "5")
		fail(w, 429, "ingestion_busy", "Retry this same batch later.")
		return
	}
	free, e := platform.FreeSpace(filepath.Dir(h.Store.Path))
	if e != nil || free < 512<<20 {
		storageFail(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, protocol.MaxBody)
	var reader io.Reader = r.Body
	if encoding := r.Header.Get("Content-Encoding"); encoding == "gzip" {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			fail(w, 400, "invalid_compression", "Invalid compressed body.")
			return
		}
		defer gz.Close()
		reader = gz
	} else if encoding != "" {
		fail(w, 415, "unsupported_encoding", "Use JSON or gzip JSON.")
		return
	}
	body, e := io.ReadAll(io.LimitReader(reader, protocol.MaxBody+1))
	if e != nil || len(body) > protocol.MaxBody {
		fail(w, 413, "batch_too_large", "Batch exceeds 2 MiB.")
		return
	}
	var b protocol.Batch
	if e = json.Unmarshal(body, &b); e != nil {
		fail(w, 400, "invalid_json", "Invalid telemetry JSON.")
		return
	}
	if b.HostID != host {
		fail(w, 403, "host_mismatch", "Credential is bound to another host.")
		return
	}
	if e = b.Validate(time.Now()); e != nil {
		fail(w, 400, e.Error(), "Telemetry did not satisfy the protocol contract.")
		return
	}
	duplicate, e := h.Store.Ingest(r.Context(), b, body)
	if e != nil {
		if errors.Is(e, storage.ErrConflict) || errors.Is(e, storage.ErrOrder) {
			fail(w, 409, storage.ErrorCode(e), "Sequence or source time conflicts with committed telemetry.")
		} else {
			storageFail(w)
		}
		return
	}
	if b.Processes != nil && time.Since(b.Processes.At) < 30*time.Second {
		h.processMu.Lock()
		current, ok := h.processes[host]
		if !ok || b.Processes.At.After(current.At) {
			h.processes[host] = *b.Processes
		}
		h.processMu.Unlock()
	}
	writeJSON(w, 200, protocol.Ack{Sequence: b.Sequence, ServerTime: time.Now().UTC(), Protocol: 1, Duplicate: duplicate})
}

func (h *Hub) processSummary(w http.ResponseWriter, r *http.Request) {
	h.processMu.RLock()
	snapshot, ok := h.processes[r.PathValue("id")]
	h.processMu.RUnlock()
	if !ok {
		writeJSON(w, 200, map[string]any{"status": "unavailable", "reason": "Enable collect_processes on the agent to receive current CPU and memory leaders. Summaries are held only in memory.", "processes": []protocol.Process{}})
		return
	}
	state := "available"
	if time.Since(snapshot.At) > 45*time.Second {
		state = "stale"
	}
	writeJSON(w, 200, map[string]any{"status": state, "at": snapshot.At, "visible": snapshot.Visible, "limited": snapshot.Limited, "processes": snapshot.Processes, "reason": "Names only; CPU is percent of one logical core. No command lines or process history are stored."})
}
func timeRange(r *http.Request) (time.Time, time.Time, error) {
	to := time.Now().UTC()
	from := to.Add(-time.Hour)
	var e error
	if v := r.URL.Query().Get("to"); v != "" {
		to, e = time.Parse(time.RFC3339Nano, v)
		if e != nil {
			return from, to, e
		}
	}
	if v := r.URL.Query().Get("from"); v != "" {
		from, e = time.Parse(time.RFC3339Nano, v)
		if e != nil {
			return from, to, e
		}
	}
	if !to.After(from) || to.Sub(from) > 366*24*time.Hour || to.After(time.Now().Add(5*time.Minute)) {
		return from, to, errors.New("invalid_range")
	}
	return from, to, nil
}
func (h *Hub) metrics(w http.ResponseWriter, r *http.Request) {
	from, to, e := timeRange(r)
	if e != nil {
		fail(w, 400, "invalid_range", "Choose a valid range of at most 366 days.")
		return
	}
	v, e := h.Store.Metrics(r.Context(), r.PathValue("id"), r.URL.Query().Get("entity"), from, to, 600)
	if e != nil {
		storageFail(w)
		return
	}
	writeJSON(w, 200, v)
}
func (h *Hub) containers(w http.ResponseWriter, r *http.Request) {
	entities, e := h.Store.Entities(r.Context(), r.PathValue("id"), "container")
	if e != nil {
		storageFail(w)
		return
	}
	out := []map[string]any{}
	for _, entity := range entities {
		var p, at string
		var interval float64
		values := map[string]float64{}
		_ = h.Store.DB.QueryRowContext(r.Context(), "SELECT payload,at,interval FROM metric_samples WHERE host_id=? AND entity_id=? ORDER BY at DESC LIMIT 1", r.PathValue("id"), entity.ID).Scan(&p, &at, &interval)
		json.Unmarshal([]byte(p), &values)
		for k, v := range values {
			if protocol.Registry[k].Kind == "delta" && interval > 0 {
				values[k] = v / interval
			}
		}
		out = append(out, map[string]any{"entity": entity, "values": values, "at": at})
	}
	writeJSON(w, 200, out)
}
func (h *Hub) container(w http.ResponseWriter, r *http.Request) {
	host := r.URL.Query().Get("host")
	entities, e := h.Store.Entities(r.Context(), host, "container")
	if e != nil {
		storageFail(w)
		return
	}
	for _, entity := range entities {
		if entity.ID == r.PathValue("id") {
			rows, e := h.Store.DB.QueryContext(r.Context(), "SELECT generation,start,end FROM container_runs WHERE host_id=? AND entity_id=? ORDER BY start DESC LIMIT 100", host, entity.ID)
			if e != nil {
				storageFail(w)
				return
			}
			defer rows.Close()
			runs := []map[string]any{}
			for rows.Next() {
				var gen, start string
				var end sql.NullString
				rows.Scan(&gen, &start, &end)
				runs = append(runs, map[string]any{"generation": gen, "start": start, "end": end.String})
			}
			writeJSON(w, 200, map[string]any{"entity": entity, "runs": runs})
			return
		}
	}
	fail(w, 404, "container_not_found", "Container not found.")
}
func (h *Hub) incidents(w http.ResponseWriter, r *http.Request) {
	v, e := h.Store.Incidents(r.Context(), r.URL.Query().Get("host"), r.URL.Query().Get("before"), 200)
	if e != nil {
		storageFail(w)
		return
	}
	writeJSON(w, 200, v)
}
func (h *Hub) incident(w http.ResponseWriter, r *http.Request) {
	v, e := h.Store.Incident(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, 404, "incident_not_found", "Incident not found.")
		return
	}
	writeJSON(w, 200, v)
}
func (h *Hub) report(w http.ResponseWriter, r *http.Request) {
	v, e := h.Store.Incident(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, 404, "incident_not_found", "Incident not found.")
		return
	}
	if r.URL.Query().Get("format") == "json" {
		writeJSON(w, 200, v)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="rootlens-incident.md"`)
	fmt.Fprintf(w, "# %s\n\nHost: %s · State: %s · Rule: %s v%d\n\nStarted: %s\n\n", v.Title, v.HostID, v.State, v.Rule, v.RuleVersion, v.Start.Format(time.RFC3339))
	for _, ev := range v.Evidence {
		fmt.Fprintf(w, "- **%s:** %s\n", ev.Level, ev.Text)
	}
	for _, c := range v.Contributors {
		fmt.Fprintf(w, "\n- %s: %s; %.1f%% of observed container block activity on %s.\n", c.Name, c.Label, c.Share, c.Device)
	}
	fmt.Fprintln(w, "\n## Investigation steps")
	for _, step := range v.Steps {
		fmt.Fprintf(w, "\n- %s", step)
	}
	fmt.Fprintln(w, "\n\n## Limitations")
	for _, text := range v.Limitations {
		fmt.Fprintf(w, "\n- %s", text)
	}
}
func (h *Hub) evidence(w http.ResponseWriter, r *http.Request) {
	rows, e := h.Store.DB.QueryContext(r.Context(), "SELECT entity_id,resolution,bucket,generation,payload FROM incident_evidence WHERE incident_id=? ORDER BY bucket LIMIT 20000", r.PathValue("id"))
	if e != nil {
		storageFail(w)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, gen, p string
		var resolution, bucket int64
		rows.Scan(&id, &resolution, &bucket, &gen, &p)
		out = append(out, map[string]any{"entity_id": id, "resolution_seconds": resolution, "at": time.Unix(bucket, 0), "generation": gen, "summary": json.RawMessage(p)})
	}
	writeJSON(w, 200, out)
}
func (h *Hub) events(w http.ResponseWriter, r *http.Request) {
	v, e := h.Store.Events(r.Context(), r.URL.Query().Get("host"), r.URL.Query().Get("before"))
	if e != nil {
		storageFail(w)
		return
	}
	writeJSON(w, 200, v)
}
func (h *Hub) diagnostics(w http.ResponseWriter, r *http.Request) {
	v, e := h.Store.Incidents(r.Context(), r.PathValue("id"), "", 200)
	if e != nil {
		storageFail(w)
		return
	}
	open := []protocol.Incident{}
	for _, i := range v {
		if i.State == "open" {
			open = append(open, i)
		}
	}
	writeJSON(w, 200, map[string]any{"findings": open, "rules": diagnostics.Rules, "message": "No finding is not proof that applications are healthy."})
}
func (h *Hub) investigate(w http.ResponseWriter, r *http.Request) {
	var input protocol.InvestigationRequest
	if !decode(w, r, &input, 1024) {
		return
	}
	if !input.To.After(input.From) || input.To.Sub(input.From) > 6*time.Hour || input.To.After(time.Now().Add(5*time.Minute)) {
		fail(w, 400, "invalid_range", "Choose at most six hours of retained native-resolution data.")
		return
	}
	ret, e := h.Store.Settings(r.Context())
	if e != nil {
		storageFail(w)
		return
	}
	if input.From.Before(time.Now().Add(-time.Duration(ret.RawHours) * time.Hour)) {
		fail(w, 422, "resolution_unavailable", "Native-resolution data has expired. Retained incident reports remain available.")
		return
	}
	out, e := h.Store.Investigate(r.Context(), r.PathValue("id"), input.From, input.To)
	if errors.Is(e, storage.ErrAnalysisLimit) {
		fail(w, 422, "analysis_limit", "Narrow the interval to stay within the bounded analysis budget.")
		return
	}
	if e != nil {
		storageFail(w)
		return
	}
	writeJSON(w, 200, map[string]any{"findings": out, "historical": true, "resolution": "native", "limitation": "Analysis is bounded by the selected interval and does not create notifications."})
}
func (h *Hub) settings(w http.ResponseWriter, r *http.Request) {
	v, e := h.Store.Settings(r.Context())
	if e != nil {
		storageFail(w)
		return
	}
	writeJSON(w, 200, v)
}
func (h *Hub) updateSettings(w http.ResponseWriter, r *http.Request) {
	var v storage.Retention
	if !decode(w, r, &v, 1024) {
		return
	}
	if e := h.Store.SetSettings(r.Context(), v); e != nil {
		fail(w, 400, "invalid_retention", "Retention values are outside the supported bounds.")
		return
	}
	writeJSON(w, 200, v)
}
func (h *Hub) health(w http.ResponseWriter, r *http.Request) {
	v := h.Store.Health(r.Context())
	free, e := platform.FreeSpace(filepath.Dir(h.Store.Path))
	v["free_bytes"] = free
	v["ingestion_available"] = e == nil && free >= 512<<20
	v["version"] = protocol.Version
	writeJSON(w, 200, v)
}
