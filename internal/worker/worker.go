package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
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
	if job.Type != queue.TypeExample {
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

		// Do not log Payload. Deploy JSON is an id; future types might
		// accidentally include an env value.
		started := time.Now()
		err = h.Handle(ctx, job)
		log.Info("job finished",
			"id", job.ID,
			"type", job.Type,
			"duration_ms", time.Since(started).Milliseconds(),
			"ok", err == nil,
		)
		if err != nil {
			log.Error("job handler failed", "id", job.ID, "type", job.Type, "err", err)
			continue
		}
	}
}

// RunConcurrent dequeues up to limit jobs at once.
//
// Redis BLPOP delivers each job to one waiter, so two goroutines do
// not run the same payload. A handler that returns queue.ErrDefer
// still has to Enqueue the job again: it is already off the list.
// limit is the node cap (FORGE_MAX_INFLIGHT_PER_NODE). Values below 1
// run one job at a time.
func RunConcurrent(ctx context.Context, q queue.JobQueue, h Handler, limit int, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	if h == nil {
		h = ExampleHandler{Log: log}
	}
	if limit < 1 {
		limit = 1
	}
	log.Info("worker started", "concurrency", limit)
	defer log.Info("worker stopped")

	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for {
		if err := ctx.Err(); err != nil {
			wg.Wait()
			return nil
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil
		case sem <- struct{}{}:
		}

		job, err := q.Dequeue(ctx)
		if err != nil {
			<-sem
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				wg.Wait()
				return nil
			}
			if errors.Is(err, queue.ErrMalformed) {
				log.Error("skipping malformed job", "err", err)
				continue
			}
			log.Error("dequeue failed", "err", err)
			select {
			case <-ctx.Done():
				wg.Wait()
				return nil
			case <-time.After(time.Second):
			}
			continue
		}

		wg.Add(1)
		go func(job queue.Job) {
			defer wg.Done()
			started := time.Now()
			err := h.Handle(ctx, job)
			<-sem
			if errors.Is(err, queue.ErrDefer) {
				// The slot is free so a dependency can run. Wait
				// briefly so a not-ready job does not spin.
				timer := time.NewTimer(300 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
				case <-timer.C:
				}
				enqCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				if enqErr := q.Enqueue(enqCtx, job); enqErr != nil {
					log.Error("requeue deferred job failed", "id", job.ID, "err", enqErr)
				}
				cancel()
				return
			}
			log.Info("job finished",
				"id", job.ID,
				"type", job.Type,
				"duration_ms", time.Since(started).Milliseconds(),
				"ok", err == nil,
			)
			if err != nil {
				log.Error("job handler failed", "id", job.ID, "type", job.Type, "err", err)
			}
		}(job)
	}
}
