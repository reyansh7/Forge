package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/reyansh7/Forge/internal/queue"
)

func TestDispatcherRoutesExampleAndRejectsUnknown(t *testing.T) {
	d := Dispatcher{Example: ExampleHandler{Log: silentLog()}}
	if err := d.Handle(context.Background(), queue.Job{ID: "1", Type: queue.TypeExample}); err != nil {
		t.Fatal(err)
	}
	if err := d.Handle(context.Background(), queue.Job{ID: "1", Type: "shell"}); !errors.Is(err, queue.ErrUnknownType) {
		t.Fatalf("err = %v", err)
	}
}
