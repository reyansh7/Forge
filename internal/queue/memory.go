package queue

import "context"

// Memory is an in-process queue for unit tests (no Docker).
//
// It is not a production backend: jobs die with the process. A buffered
// channel gives FIFO and lets Dequeue wait without a busy loop.
type Memory struct {
	ch chan Job
}

// NewMemory returns a queue that can hold a small burst of jobs.
func NewMemory() *Memory {
	return &Memory{ch: make(chan Job, 32)}
}

func (m *Memory) Enqueue(ctx context.Context, job Job) error {
	if _, err := job.Marshal(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case m.ch <- job:
		return nil
	}
}

func (m *Memory) Dequeue(ctx context.Context) (Job, error) {
	select {
	case <-ctx.Done():
		return Job{}, ctx.Err()
	case job := <-m.ch:
		return job, nil
	}
}
