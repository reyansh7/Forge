package queue

import "context"

// JobQueue is the HTTP/worker port over a transient job transport.
//
// Why an interface: handlers must not issue Redis RPUSH/BLPOP themselves.
// Tests inject Memory; production injects Redis. The worker depends on this
// same interface so it is not sprinkled with RESP.
type JobQueue interface {
	Enqueue(ctx context.Context, job Job) error
	Dequeue(ctx context.Context) (Job, error)
}
