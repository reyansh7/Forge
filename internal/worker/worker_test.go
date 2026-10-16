package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/reyansh7/Forge/internal/queue"
)

func silentLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestExampleHandlerRejectsUnknownType(t *testing.T) {
	h := ExampleHandler{Log: silentLog()}
	err := h.Handle(context.Background(), queue.Job{ID: "1", Type: "shell"})
	if !errors.Is(err, queue.ErrUnknownType) {
		t.Fatalf("err = %v", err)
	}
}

func TestExampleHandlerAcceptsExample(t *testing.T) {
	h := ExampleHandler{Log: silentLog()}
	if err := h.Handle(context.Background(), queue.Job{ID: "1", Type: queue.TypeExample}); err != nil {
		t.Fatal(err)
	}
}

type countingHandler struct {
	n atomic.Int32
}

func (c *countingHandler) Handle(context.Context, queue.Job) error {
	c.n.Add(1)
	return nil
}

func TestRunProcessesExampleJob(t *testing.T) {
	q := queue.NewMemory()
	h := &countingHandler{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, q, h, silentLog())
	}()

	if err := q.Enqueue(ctx, queue.Job{ID: "j1", Type: queue.TypeExample}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for h.n.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("handler was not called")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	q := queue.NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, q, ExampleHandler{Log: silentLog()}, silentLog())
	}()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestRunSkipsMalformedAndContinues(t *testing.T) {
	q := &oneShotQueue{err: queue.ErrMalformed}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := &countingHandler{}
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, q, h, silentLog())
	}()

	time.Sleep(50 * time.Millisecond)
	if h.n.Load() != 0 {
		t.Fatal("handler must not run for malformed jobs")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
}

// oneShotQueue returns err from Dequeue until ctx is done.
type oneShotQueue struct {
	err error
}

func (q *oneShotQueue) Enqueue(context.Context, queue.Job) error { return nil }

func (q *oneShotQueue) Dequeue(ctx context.Context) (queue.Job, error) {
	select {
	case <-ctx.Done():
		return queue.Job{}, ctx.Err()
	case <-time.After(20 * time.Millisecond):
		return queue.Job{}, q.err
	}
}

type overlapHandler struct {
	started chan struct{}
	release chan struct{}
}

func (h *overlapHandler) Handle(ctx context.Context, _ queue.Job) error {
	select {
	case h.started <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-h.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestRunConcurrentOverlapsIndependentJobs(t *testing.T) {
	q := queue.NewMemory()
	h := &overlapHandler{started: make(chan struct{}, 2), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- RunConcurrent(ctx, q, h, 2, silentLog())
	}()
	for _, id := range []string{"a", "b"} {
		if err := q.Enqueue(ctx, queue.Job{ID: id, Type: queue.TypeExample}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		select {
		case <-h.started:
		case <-time.After(2 * time.Second):
			t.Fatal("jobs did not overlap")
		}
	}
	close(h.release)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
}

type deferHandler struct {
	n atomic.Int32
}

func (h *deferHandler) Handle(context.Context, queue.Job) error {
	if h.n.Add(1) == 1 {
		return queue.ErrDefer
	}
	return nil
}

func TestRunConcurrentRequeuesDeferredJob(t *testing.T) {
	q := queue.NewMemory()
	h := &deferHandler{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- RunConcurrent(ctx, q, h, 1, silentLog())
	}()
	if err := q.Enqueue(ctx, queue.Job{ID: "j", Type: queue.TypeExample}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for h.n.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("deferred job ran %d times", h.n.Load())
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
}
