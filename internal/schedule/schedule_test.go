package schedule

import (
	"context"
	"errors"
	"testing"

	"github.com/reyansh7/Forge/internal/store"
)

type fakeNodes struct {
	ready []store.Node
	err   error
}

func (f fakeNodes) ListReadyNodes(context.Context) ([]store.Node, error) {
	return f.ready, f.err
}

func TestPlacePicksLowestInflight(t *testing.T) {
	s := Scheduler{
		Nodes: fakeNodes{ready: []store.Node{
			{ID: "a", Name: "local", InProgress: 2},
			{ID: "b", Name: "lab2", InProgress: 0},
		}},
		MaxInflight: 4,
	}
	got, err := s.Place(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "b" {
		t.Fatalf("got %+v", got)
	}
}

func TestPlaceSkipsNodesAtCap(t *testing.T) {
	s := Scheduler{
		Nodes: fakeNodes{ready: []store.Node{
			{ID: "a", Name: "local", InProgress: 2},
		}},
		MaxInflight: 2,
	}
	if _, err := s.Place(context.Background()); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("err = %v", err)
	}
}

func TestPlaceNoReady(t *testing.T) {
	s := Scheduler{Nodes: fakeNodes{}}
	if _, err := s.Place(context.Background()); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("err = %v", err)
	}
}

func TestPlacePropagatesStoreError(t *testing.T) {
	want := errors.New("db down")
	s := Scheduler{Nodes: fakeNodes{err: want}}
	if _, err := s.Place(context.Background()); !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}
