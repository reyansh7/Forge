package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/reyansh7/Forge/internal/queue"
	"github.com/reyansh7/Forge/internal/schedule"
)

// JobQueue is the HTTP → Redis boundary for increment 0.3.
// Tests inject queue.Memory (or a stub). cmd/api injects *queue.Redis.
type JobQueue interface {
	Enqueue(ctx context.Context, job queue.Job) error
}

type enqueueJobRequest struct {
	Type string `json:"type"`
}

type enqueueJobResponse struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

// enqueueJob handles POST /jobs.
//
// 202 Accepted means "queued", not "the worker finished". The handler
// only RPUSHes; it does not wait for ExampleHandler.
//
// POST /jobs requires a session (Phase 3). Type must still be "example".
// Payload is always {} so a JSON field named "command" cannot reach Redis.
func (s *Server) enqueueJob(w http.ResponseWriter, r *http.Request) {
	if s.Jobs == nil {
		writeError(w, http.StatusInternalServerError, "job queue is not configured")
		return
	}

	var req enqueueJobRequest
	if !decodeJSON(r, w, &req) {
		return
	}

	// POST /jobs is the 0.3 demo path only. Deploy jobs are created by
	// POST /projects/{id}/deployments so clients cannot pick a payload.
	if req.Type != queue.TypeExample {
		writeError(w, http.StatusBadRequest, "unsupported job type")
		return
	}

	id, err := queue.NewID()
	if err != nil {
		s.logger().Error("job id failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to enqueue job")
		return
	}

	job := queue.Job{
		ID:      id,
		Type:    queue.TypeExample,
		Payload: json.RawMessage(`{}`),
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if _, err := s.enqueuePlaced(ctx, job); err != nil {
		if errors.Is(err, schedule.ErrNoCapacity) {
			writeError(w, http.StatusConflict, "no ready worker node with capacity")
			return
		}
		s.logger().Error("enqueue job failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, "failed to enqueue job")
		return
	}

	writeJSON(w, http.StatusAccepted, enqueueJobResponse{
		ID:     job.ID,
		Type:   job.Type,
		Status: "queued",
	})
}
