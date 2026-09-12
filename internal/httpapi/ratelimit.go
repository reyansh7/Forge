package httpapi

import (
	"sync"
	"time"
)

// attemptGate is a process-local sliding window of events per key
// (usually a client IP).
//
// What: rate / abuse control for login and bootstrap. Those endpoints
// are unauthenticated, so a script on loopback can still hammer bcrypt.
// Why: Phase 3.c login/bootstrap, Phase 5 deploy attempts.
// How: keep timestamps; allow if fewer than `max`
// fall inside `window`.
//
// This is in-memory. Restart clears it. Two API processes do not share
// it. That is acceptable while Forge is one local process.
//
// We do not read X-Forwarded-For: that header is attacker-controlled
// unless a trusted proxy overwrites it, and this phase has no proxy
// in front of the API.
type attemptGate struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	max    int
	window time.Duration
}

func newAttemptGate(max int, window time.Duration) *attemptGate {
	if max < 1 {
		max = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	return &attemptGate{
		hits:   map[string][]time.Time{},
		max:    max,
		window: window,
	}
}

func (g *attemptGate) allow(key string) bool {
	if g == nil {
		return true
	}
	if key == "" {
		key = "unknown"
	}
	now := time.Now()
	cutoff := now.Add(-g.window)

	g.mu.Lock()
	defer g.mu.Unlock()

	prev := g.hits[key]
	kept := prev[:0]
	for _, ts := range prev {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	if len(kept) >= g.max {
		g.hits[key] = kept
		return false
	}
	g.hits[key] = append(kept, now)
	return true
}
