package queue

import (
	"context"
	"testing"
	"time"
)

func TestRedisPingFailsWhenUnreachable(t *testing.T) {
	// Port 1 is not Redis. Enqueue/Ping must surface this so HTTP can 503.
	q, err := NewRedis("redis://127.0.0.1:1", "forge:jobs:unused")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := q.Ping(ctx); err == nil {
		t.Fatal("expected dial error")
	}
}
