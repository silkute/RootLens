package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"rootlens/internal/protocol"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type pending struct {
	body []byte
	at   time.Time
}
type Buffer struct {
	mu      sync.Mutex
	items   []pending
	bytes   int
	dropped int
}

func (b *Buffer) Push(body []byte, at time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for len(b.items) > 0 && (b.bytes+len(body) > 32<<20 || at.Sub(b.items[0].at) > 10*time.Minute) {
		b.bytes -= len(b.items[0].body)
		b.items = b.items[1:]
		b.dropped++
	}
	if len(body) > 32<<20 {
		b.dropped++
		return
	}
	b.items = append(b.items, pending{body, at})
	b.bytes += len(body)
}
func (b *Buffer) peek() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.items) == 0 {
		return nil
	}
	return b.items[0].body
}
func (b *Buffer) ack(body []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.items) > 0 && bytes.Equal(b.items[0].body, body) {
		b.bytes -= len(body)
		b.items = b.items[1:]
	}
}
func (b *Buffer) takeDropped() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := b.dropped
	b.dropped = 0
	return n
}
func secret() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func LoadConfig(path string) (Config, error) {
	var c Config
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	c.Defaults()
	if c.Sampling != 5 && c.Sampling != 10 && c.Sampling != 15 {
		return c, errors.New("sampling_must_be_5_10_or_15")
	}
	u, e := url.Parse(c.Hub)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return c, errors.New("hub_requires_https")
	}
	if !protocol.ValidID(c.HostID) || c.CredentialFile == "" || len(c.Filesystems) > 32 {
		return c, errors.New("invalid_agent_configuration")
	}
	return c, nil
}
func Run(ctx context.Context, c Config) error {
	ca, e := os.ReadFile(c.CAFile)
	if e != nil {
		return errors.New("ca_file_unavailable")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return errors.New("invalid_ca_file")
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}, MaxIdleConnsPerHost: 2}, CheckRedirect: func(req *http.Request, via []*http.Request) error { return errors.New("redirect_not_allowed") }}
	credentialBytes, e := os.ReadFile(c.CredentialFile)
	if os.IsNotExist(e) {
		credentialBytes = []byte(secret())
		if e = os.MkdirAll(filepath.Dir(c.CredentialFile), 0700); e != nil {
			return e
		}
		file, e := os.OpenFile(c.CredentialFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = file.Write(credentialBytes)
		if e == nil {
			e = file.Sync()
		}
		closeErr := file.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	} else if e != nil {
		return e
	}
	credential := strings.TrimSpace(string(credentialBytes))
	if len(credential) != 43 {
		return errors.New("invalid_credential_file")
	}
	if c.EnrollmentFile != "" {
		tokenBytes, e := os.ReadFile(c.EnrollmentFile)
		if e != nil {
			return errors.New("enrollment_file_unavailable")
		}
		body, _ := json.Marshal(map[string]string{"host_id": c.HostID, "token": strings.TrimSpace(string(tokenBytes)), "credential": credential})
		req, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.Hub, "/")+"/api/v1/agent/enroll", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, e := client.Do(req)
		if e != nil {
			return errors.New("enrollment_transport_failed")
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode != 201 && resp.StatusCode != 403 {
			return fmt.Errorf("enrollment_status_%d", resp.StatusCode)
		}
		if resp.StatusCode == 403 {
			log.Print("Enrollment not repeated; checking persisted credential by uploading telemetry.")
		}
	}
	collector := NewCollector(c)
	defer func() {
		if collector.docker != nil && collector.docker.cancel != nil {
			collector.docker.cancel()
		}
	}()
	buffer := &Buffer{}
	var clockOffset atomic.Int64
	wake := make(chan struct{}, 1)
	go sender(ctx, client, c.Hub, credential, buffer, wake, &clockOffset)
	epoch := secret()
	seq := uint64(0)
	ticker := time.NewTicker(time.Duration(c.Sampling) * time.Second)
	defer ticker.Stop()
	collect := func() {
		now := time.Now()
		frame, entities, caps, events := collector.Collect(now)
		offset := float64(clockOffset.Load()) / float64(time.Second)
		clockStatus := "available"
		if math.Abs(offset) > 5 {
			clockStatus = "error"
		}
		caps["clock"] = protocol.Capability{Status: clockStatus, Reason: fmt.Sprintf("Estimated hub clock offset %.1fs. Rates use local monotonic intervals; synchronize clocks for timeline alignment.", offset), At: now.UTC()}
		seq++
		if dropped := buffer.takeDropped(); dropped > 0 {
			events = append(events, protocol.Event{ID: fmt.Sprintf("gap-%s-%d", epoch, seq), At: now.UTC(), Type: "telemetry_gap", Message: fmt.Sprintf("Agent buffer discarded %d batches after its duration or size limit.", dropped)})
		}
		batch := protocol.Batch{Protocol: 1, AgentVersion: protocol.Version, HostID: c.HostID, BootID: collector.Boot, Epoch: strings.ReplaceAll(epoch, "-", "_"), Sequence: strconv.FormatUint(seq, 10), SentAt: now.UTC(), Sampling: c.Sampling, Entities: entities, Frames: []protocol.Frame{frame}, Events: events, Capabilities: caps}
		batch.Processes = collector.ProcessSnapshot
		if e := batch.Validate(now); e != nil {
			log.Print("collector_contract_failed: ", e)
			return
		}
		body, e := json.Marshal(batch)
		if e != nil || len(body) > protocol.MaxBody {
			log.Print("collector_batch_limit")
			return
		}
		buffer.Push(body, now)
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	collect()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			collect()
		}
	}
}
func sender(ctx context.Context, client *http.Client, hub, credential string, buffer *Buffer, wake <-chan struct{}, clockOffset *atomic.Int64) {
	backoff := time.Second
	for ctx.Err() == nil {
		body := buffer.peek()
		if body == nil {
			select {
			case <-ctx.Done():
				return
			case <-wake:
				continue
			}
		}
		req, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(hub, "/")+"/api/v1/agent/batches", bytes.NewReader(body))
		if e != nil {
			return
		}
		req.Header.Set("Authorization", "Bearer "+credential)
		req.Header.Set("Content-Type", "application/json")
		sent := time.Now()
		resp, e := client.Do(req)
		if e == nil {
			var ack protocol.Ack
			if resp.StatusCode == 200 {
				e = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&ack)
			} else {
				io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			}
			resp.Body.Close()
			if resp.StatusCode == 200 && e == nil {
				var batch protocol.Batch
				if json.Unmarshal(body, &batch) == nil && ack.Sequence == batch.Sequence && ack.Protocol == 1 {
					if !ack.ServerTime.IsZero() {
						midpoint := sent.Add(time.Since(sent) / 2)
						clockOffset.Store(ack.ServerTime.Sub(midpoint).Nanoseconds())
					}
					buffer.ack(body)
					backoff = time.Second
					continue
				}
			}
			log.Printf("telemetry_upload_status_%d", resp.StatusCode)
		} else {
			log.Print("telemetry_transport_unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}
