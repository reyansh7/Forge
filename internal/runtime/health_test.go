package runtime

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProbeHTTPSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	port := mustPort(t, srv)
	if err := ProbeHTTP(context.Background(), Probe{Port: port, Path: "/ready", Timeout: 2 * time.Second}); err != nil {
		t.Fatal(err)
	}
}

func TestProbeHTTPStatus404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	err := ProbeHTTP(context.Background(), Probe{Port: mustPort(t, srv), Path: "/", Timeout: 2 * time.Second, ListenPort: 5000, PortSource: "source_listen"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "health path") {
		t.Fatalf("should suggest health path: %v", err)
	}
}

func TestProbeHTTPExitedContainer(t *testing.T) {
	err := ProbeHTTP(context.Background(), Probe{
		Port:    59999,
		Path:    "/",
		Timeout: 2 * time.Second,
		Inspect: func(context.Context) (ContainerState, error) {
			return ContainerState{Exists: true, Running: false, Status: "exited", ExitCode: 1, Name: "demo-a7f3"}, nil
		},
		Container: "demo-a7f3",
	})
	if err == nil || !strings.Contains(err.Error(), "not running") || !strings.Contains(err.Error(), "Exit code: 1") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "timed out") {
		t.Fatalf("must not look like a timeout: %v", err)
	}
}

func TestHealthGETPathStillWorks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if err := HealthGETPath(context.Background(), mustPort(t, srv), "/", 2*time.Second); err != nil {
		t.Fatal(err)
	}
}

func mustPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
