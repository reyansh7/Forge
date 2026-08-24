// Package httpapi is the control-plane HTTP surface.
//
// Routes here are Forge's own API (health, projects). Deployment routes come later.
// They are not the HTTP servers of apps users deploy — those will sit
// behind a reverse proxy in a later phase.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// StatusChecker reports whether a dependency is reachable.
//
// The interface is small on purpose: health only needs Ping. Tests inject
// a fake that returns an error; production injects *store.Postgres / RedisPinger.
// Implementations must not run user code.
type StatusChecker interface {
	Ping(ctx context.Context) error
}

// Server is the control-plane HTTP process's handler bundle.
//
// cmd/api fills these fields. Postgres and Redis satisfy StatusChecker
// (/health). The same *store.Postgres also satisfies ProjectStore
// (/projects). Tests swap fakes so Docker is not required.
type Server struct {
	Log      *slog.Logger
	Postgres StatusChecker
	Redis    StatusChecker
	// Projects is unused by /health. Project routes require it.
	Projects ProjectStore
}

func (s *Server) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Handler returns the mux. Only Forge control-plane routes belong here
// (not the HTTP servers of apps users will deploy later).
//
// Go 1.22 method-aware patterns ("GET /health") reject POST to the same
// path with 405 instead of treating every method as GET.
// GET /projects and GET /projects/{id} are different patterns; the mux
// picks the more specific one for /projects/<uuid>.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /projects", s.createProject)
	mux.HandleFunc("GET /projects", s.listProjects)
	mux.HandleFunc("GET /projects/{id}", s.getProject)
	return mux
}

// healthResponse is the JSON body. Field names are the public contract
// for curl, Compose healthchecks, and later a dashboard.
type healthResponse struct {
	Status   string `json:"status"`
	Postgres string `json:"postgres"`
	Redis    string `json:"redis"`
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	// Bound the pings so a hung database cannot stall this handler forever.
	// The request context is also cancelled if the client disconnects.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	resp := healthResponse{
		Status:   "ok",
		Postgres: "ok",
		Redis:    "ok",
	}

	if err := s.Postgres.Ping(ctx); err != nil {
		s.logger().Error("postgres ping failed", "err", err)
		resp.Postgres = "error"
		resp.Status = "degraded"
	}
	if err := s.Redis.Ping(ctx); err != nil {
		s.logger().Error("redis ping failed", "err", err)
		resp.Redis = "error"
		resp.Status = "degraded"
	}

	// 503 (not 500) means "this process is up, a dependency is not."
	// Load balancers and Compose can use that distinction.
	code := http.StatusOK
	if resp.Status != "ok" {
		code = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	// Encode errors are rare (broken conn). Ignoring them avoids a second
	// write after WriteHeader; the client already has a status code.
	_ = json.NewEncoder(w).Encode(resp)
}
