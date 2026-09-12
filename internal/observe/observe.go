// Package observe is Phase 4 control-plane telemetry.
//
// Responsibility: in-process HTTP counters and a request-id helper.
// Deploy totals and container health are answered from PostgreSQL and
// Docker at request time so a second API process is not a second source
// of truth. Tracing and alerts are intentionally not here.
//
// Called by: HTTP middleware and GET /metrics.
// Must not: log secrets, require a sidecar, or expose metrics without
// a session.
package observe

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync/atomic"
	"time"
)

// Metrics is the API process's request counters.
//
// Atomically updated from every handler goroutine. Snapshot copies the
// counters; it does not reset them. Restarting the API zeros this view.
// Durable deploy history stays in PostgreSQL.
type Metrics struct {
	httpRequests  atomic.Int64
	httpErrors    atomic.Int64
	deploysQueued atomic.Int64
}

// Snapshot is a JSON-safe copy of process counters.
type Snapshot struct {
	HTTPRequests  int64 `json:"http_requests_total"`
	HTTPErrors    int64 `json:"http_errors_total"`
	DeploysQueued int64 `json:"deploys_queued_total"`
}

func (m *Metrics) IncHTTP(status int) {
	if m == nil {
		return
	}
	m.httpRequests.Add(1)
	if status >= 500 {
		m.httpErrors.Add(1)
	}
}

func (m *Metrics) IncDeploysQueued() {
	if m == nil {
		return
	}
	m.deploysQueued.Add(1)
}

func (m *Metrics) Snapshot() Snapshot {
	if m == nil {
		return Snapshot{}
	}
	return Snapshot{
		HTTPRequests:  m.httpRequests.Load(),
		HTTPErrors:    m.httpErrors.Load(),
		DeploysQueued: m.deploysQueued.Load(),
	}
}

// NewRequestID is 16 random bytes, hex. Clients may send X-Request-ID;
// we generate one when they do not so a log line can be correlated
// without a distributed tracer (tracing is a later increment).
func NewRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(buf[:])
}

// SafePath is the URL path only. Query strings are dropped so a
// leaked token in ?access_token= never appears in slog.
func SafePath(path string) string {
	p := strings.TrimSpace(path)
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	if p == "" {
		return "/"
	}
	return p
}
