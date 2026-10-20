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
	// CollectedAt is when this response was assembled. It is not the
	// window of the process counters; those start at started_at.
	CollectedAt           time.Time        `json:"collected_at"`
	CounterScope          string           `json:"counter_scope"`
	DeploymentsTotal      int              `json:"deployments_total"`
	DeploymentsLive       int              `json:"deployments_live"`
	DeploymentsFailed     int              `json:"deployments_failed"`
	DeploymentsInProgress int              `json:"deployments_in_progress"`
	ApplicationsObserved  int              `json:"applications_observed"`
	ApplicationsUnique    int              `json:"applications_unique"`
	LastDeployDurationMS  int64            `json:"last_deploy_duration_ms"`
	ContainersRunning     int              `json:"containers_running"`
	ContainersChecked     int              `json:"containers_checked"`
	NodesReady            int              `json:"nodes_ready"`
	NodesDead             int              `json:"nodes_dead"`
	QueueDepth            string           `json:"queue_depth"`
	QueueDepthReason      string           `json:"queue_depth_reason"`
	Services              []metricsService `json:"services"`
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

	out := metricsResponse{
		Snapshot:         s.Metrics.Snapshot(),
		CollectedAt:      time.Now().UTC(),
		CounterScope:     "api_process",
		QueueDepth:       unavailableValue,
		QueueDepthReason: queueDepthReason,
		Services:         []metricsService{},
	}
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
	var apps []store.Application
	var deps []store.Deployment
	// probes records only the containers this request actually inspected.
	// A live row past the cap is absent, which buildServiceMetrics treats
	// as not_probed. An inspect error is probe_error, not not_running:
	// a Docker outage must not look like every container stopped.
	probes := map[string]string{}
	for _, p := range projects {
		if s.Apps != nil {
			listed, err := s.Apps.ListApplicationsByProject(ctx, p.ID)
			if err != nil {
				s.logger().Error("metrics list applications failed", "err", err)
				writeError(w, http.StatusInternalServerError, "failed to read metrics")
				return
			}
			apps = append(apps, listed...)
		}
		listed, err := s.Deployments.ListDeploymentsByProject(ctx, p.ID)
		if err != nil {
			s.logger().Error("metrics list deployments failed", "err", err)
			writeError(w, http.StatusInternalServerError, "failed to read metrics")
			return
		}
		deps = append(deps, listed...)
		for _, d := range listed {
			out.DeploymentsTotal++
			if d.Status.InProgress() {
				out.DeploymentsInProgress++
			}
			switch d.Status {
			case store.StatusLive:
				out.DeploymentsLive++
				if s.Runtime != nil && out.ContainersChecked < 20 {
					out.ContainersChecked++
					running, runErr := s.Runtime.Running(ctx, runtime.ResolveContainerName(d.ID, d.ContainerName))
					switch {
					case runErr != nil:
						probes[d.ID] = string(observe.ProbeError)
					case running:
						probes[d.ID] = string(observe.ProbeRunning)
						out.ContainersRunning++
					default:
						probes[d.ID] = string(observe.ProbeNotRunning)
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
	deadNodes := map[string]bool{}
	if s.Nodes != nil {
		nodes, err := s.Nodes.ListNodes(ctx)
		if err != nil {
			s.logger().Error("metrics list nodes failed", "err", err)
			writeError(w, http.StatusInternalServerError, "failed to read metrics")
			return
		}
		for _, n := range nodes {
			switch n.Status {
			case store.NodeReady:
				out.NodesReady++
			case store.NodeDead:
				out.NodesDead++
				deadNodes[n.ID] = true
			}
		}
	}
	out.Services = buildServiceMetrics(apps, deps, probes, deadNodes)
	out.ApplicationsObserved, out.ApplicationsUnique = countApplications(out.Services)
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

	env, err := parseRequestEnvironment(r, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	live, found, err := s.liveDeployment(ctx, app.ID, env)
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

	err = s.Runtime.FollowLogs(streamCtx, runtime.ResolveContainerName(live.ID, live.ContainerName), tail, func(line string) error {
		return writeEvent(logLineEvent{Line: line, DeploymentID: live.ID})
	})
	if err != nil && streamCtx.Err() == nil {
		s.logger().Error("log stream follow failed", "err", err, "deployment_id", live.ID)
		_ = writeEvent(map[string]string{"error": "log stream ended"})
	}
}
