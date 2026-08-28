package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/reyansh7/Forge/internal/queue"
)

// Handler runs one already-dequeued job. The worker never shells out or
// evaluates Payload as code — only an allowlisted Type is accepted.
type Handler interface {
	Handle(ctx context.Context, job queue.Job) error
}

// ExampleHandler is the increment 0.3 demonstration handler.
//
// It logs job id and type, then returns. That is enough to prove
// API → Redis → worker without executing user repositories.
type ExampleHandler struct {
	Log *slog.Logger
}

func (h ExampleHandler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func (h ExampleHandler) Handle(ctx context.Context, job queue.Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !queue.AllowedType(job.Type) {
		return fmt.Errorf("%w: %q", queue.ErrUnknownType, job.Type)
	}
	// Do not log Payload. Future jobs may accidentally carry secrets.
	h.log().Info("example job processed", "id", job.ID, "type", job.Type)
	return nil
}

// Run consumes q until ctx is cancelled.
//
// Dequeue is expected to block (BLPOP / channel), not spin. Redis errors
// pause one second and retry — that is reconnect, not a distributed
// retry/backoff product. Malformed and unknown jobs are logged and skipped;
// they are already off the LIST and will not be replayed.
func Run(ctx context.Context, q queue.JobQueue, h Handler, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	if h == nil {
		h = ExampleHandler{Log: log}
	}
	log.Info("worker started")
	defer log.Info("worker stopped")

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		job, err := q.Dequeue(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			if errors.Is(err, queue.ErrMalformed) {
				log.Error("skipping malformed job", "err", err)
				continue
			}
			log.Error("dequeue failed", "err", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
			continue
		}

		if err := h.Handle(ctx, job); err != nil {
			log.Error("job handler failed", "id", job.ID, "type", job.Type, "err", err)
			continue
		}
	}
}
