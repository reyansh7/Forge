package queue

import (
	"context"
	"testing"
	"time"
)

func TestMemoryEnqueueDequeue(t *testing.T) {
	q := NewMemory()
	ctx := context.Background()
	job := Job{ID: "1", Type: TypeExample}
	if err := q.Enqueue(ctx, job); err != nil {
		t.Fatal(err)
	}
	got, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "1" {
		t.Fatalf("id = %q", got.ID)
	}
}

func TestMemoryDequeueRespectsCancel(t *testing.T) {
	q := NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := q.Dequeue(ctx); err == nil {
		t.Fatal("expected context error")
	}
}

func TestMemoryEnqueueRejectsEmptyType(t *testing.T) {
	q := NewMemory()
	err := q.Enqueue(context.Background(), Job{ID: "1"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMemoryDequeueWaitsUntilEnqueue(t *testing.T) {
	q := NewMemory()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := q.Dequeue(ctx)
		done <- err
	}()

	time.Sleep(20 * time.Millisecond)
	if err := q.Enqueue(ctx, Job{ID: "2", Type: TypeExample}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("timeout")
	}
}
