package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/reyansh7/Forge/internal/queue"
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
// Clients cannot supply a command to execute. Type must be "example".
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

	jobType := req.Type
	if !queue.AllowedType(jobType) {
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

	if err := s.Jobs.Enqueue(ctx, job); err != nil {
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
