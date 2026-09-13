package queue

import (
	"context"
	"sync"
)

// Directed is an in-process per-node sink for scheduler tests.
//
// It is not a production backend. Keys are NodeKey(id) so a typo in
// tests fails the same way Redis would.
type Directed struct {
	mu sync.Mutex
	m  map[string][]Job
}

func NewDirected() *Directed {
	return &Directed{m: make(map[string][]Job)}
}

func (d *Directed) EnqueueOn(ctx context.Context, nodeID string, job Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := job.Marshal(); err != nil {
		return err
	}
	key, err := NodeKey(nodeID)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.m[key] = append(d.m[key], job)
	return nil
}

// JobsOn returns a copy of jobs queued for nodeID.
func (d *Directed) JobsOn(nodeID string) []Job {
	key, err := NodeKey(nodeID)
	if err != nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	src := d.m[key]
	out := make([]Job, len(src))
	copy(out, src)
	return out
}
