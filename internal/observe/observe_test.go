package observe

import "testing"

func TestSafePathDropsQuery(t *testing.T) {
	got := SafePath("/applications/x/logs?tail=200&token=secret")
	if got != "/applications/x/logs" {
		t.Fatalf("SafePath = %q", got)
	}
}

func TestMetricsInc(t *testing.T) {
	var m Metrics
	m.IncHTTP(200)
	m.IncHTTP(503)
	m.IncDeploysQueued()
	s := m.Snapshot()
	if s.HTTPRequests != 2 || s.HTTPErrors != 1 || s.DeploysQueued != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestNewRequestIDNonEmpty(t *testing.T) {
	a := NewRequestID()
	b := NewRequestID()
	if a == "" || a == b {
		t.Fatalf("ids %q %q", a, b)
	}
}
