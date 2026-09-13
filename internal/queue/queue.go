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

// DirectedSink RPUSH's onto one node's list. Placement lives in
// internal/schedule; this type only names the key.
type DirectedSink interface {
	EnqueueOn(ctx context.Context, nodeID string, job Job) error
}
