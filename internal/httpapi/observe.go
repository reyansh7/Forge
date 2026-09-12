package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/reyansh7/Forge/internal/observe"
	"github.com/reyansh7/Forge/internal/runtime"
	"github.com/reyansh7/Forge/internal/store"
)

const (
	requestIDHeader    = "X-Request-ID"
	maxLogStream       = 10 * time.Minute
	logStreamKeepalive = 15 * time.Second
)

// withObserve records one structured access line per request.
//
// What it is: a wrapping ResponseWriter that captures status and
// duration, then slog.Info. Why: Phase 4 "what happened" for the
// control plane without a tracing backend. How: wrap after prefix
// strip, around withAuth, so 401s are visible. Query strings are
// dropped (observe.SafePath). Cookies, Authorization, and bodies are
// never logged. A missing Flusher is fine — SSE still writes.
func (s *Server) withObserve(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			id = observe.NewRequestID()
		}
		w.Header().Set(requestIDHeader, id)
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		status := sw.status
		s.Metrics.IncHTTP(status)
		s.logger().Info("http",
			"request_id", id,
			"method", r.Method,
			"path", observe.SafePath(r.URL.Path),
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type metricsResponse struct {
	observe.Snapshot
	DeploymentsTotal     int   `json:"deployments_total"`
	DeploymentsLive      int   `json:"deployments_live"`
	DeploymentsFailed    int   `json:"deployments_failed"`
	LastDeployDurationMS int64 `json:"last_deploy_duration_ms"`
	ContainersRunning    int   `json:"containers_running"`
	ContainersChecked    int   `json:"containers_checked"`
}

func (s *Server) getMetrics(w http.ResponseWriter, r *http.Request) {
	// Process counters plus this operator's deploy rows. Global LIVE
	// counts would leak that another account has a running app.
	actor, ok := ActorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	out := metricsResponse{Snapshot: s.Metrics.Snapshot()}
	if s.Projects == nil || s.Deployments == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	projects, err := s.Projects.ListProjects(ctx, actor.UserID)
	if err != nil {
		s.logger().Error("metrics list projects failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to read metrics")
		return
	}
	var last store.Deployment
	for _, p := range projects {
		deps, err := s.Deployments.ListDeploymentsByProject(ctx, p.ID)
		if err != nil {
			s.logger().Error("metrics list deployments failed", "err", err)
			writeError(w, http.StatusInternalServerError, "failed to read metrics")
			return
		}
		for _, d := range deps {
			out.DeploymentsTotal++
			switch d.Status {
			case store.StatusLive:
				out.DeploymentsLive++
				if s.Runtime != nil && out.ContainersChecked < 20 {
					out.ContainersChecked++
					ok, runErr := s.Runtime.Running(ctx, runtime.ContainerName(d.ID))
					if runErr == nil && ok {
						out.ContainersRunning++
					}
				}
			case store.StatusFailed:
				out.DeploymentsFailed++
			}
			if !d.Status.InProgress() && d.UpdatedAt.After(last.UpdatedAt) {
				last = d
			}
		}
	}
	if last.ID != "" {
		out.LastDeployDurationMS = last.DurationMS()
	}
	writeJSON(w, http.StatusOK, out)
}

type logLineEvent struct {
	Line         string `json:"line"`
	DeploymentID string `json:"deployment_id"`
}

func (s *Server) streamApplicationLogs(w http.ResponseWriter, r *http.Request) {
	// SSE live-tail of docker logs. Same owner check as GET /logs.
	// Snapshot remains; this is an addition. No websocket, no host agent.
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	if s.Runtime == nil {
		writeError(w, http.StatusInternalServerError, "runtime is not configured")
		return
	}
	tail := 100
	if raw := r.URL.Query().Get("tail"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "tail must be a positive integer")
			return
		}
		tail = n
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	live, found, err := s.liveDeployment(ctx, app.ID)
	cancel()
	if err != nil {
		s.logger().Error("log stream: list deployments failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to read logs")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "no live deployment")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	streamCtx, stop := context.WithTimeout(r.Context(), maxLogStream)
	defer stop()

	var mu sync.Mutex
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	writeEvent := func(v any) error {
		body, err := json.Marshal(v)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
			return err
		}
		flush()
		return nil
	}

	ticker := time.NewTicker(logStreamKeepalive)
	defer ticker.Stop()
	go func() {
		for {
			select {
			case <-streamCtx.Done():
				return
			case <-ticker.C:
				mu.Lock()
				_, _ = io.WriteString(w, ": keepalive\n\n")
				flush()
				mu.Unlock()
			}
		}
	}()

	err = s.Runtime.FollowLogs(streamCtx, runtime.ContainerName(live.ID), tail, func(line string) error {
		return writeEvent(logLineEvent{Line: line, DeploymentID: live.ID})
	})
	if err != nil && streamCtx.Err() == nil {
		s.logger().Error("log stream follow failed", "err", err, "deployment_id", live.ID)
		_ = writeEvent(map[string]string{"error": "log stream ended"})
	}
}
