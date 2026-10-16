package deploy

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/reyansh7/Forge/internal/queue"
	"github.com/reyansh7/Forge/internal/store"
)

type recoverMem struct {
	mu    sync.Mutex
	rows  []store.Deployment
	stops []string
	jobs  []queue.Job
}

func (m *recoverMem) ListRecoverableDeployments(context.Context, time.Duration) ([]store.Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]store.Deployment{}, m.rows...)
	return out, nil
}

func (m *recoverMem) RequeueRecoverable(_ context.Context, id, expected, jobID string, max int, _ time.Duration) (store.Deployment, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.rows {
		d := &m.rows[i]
		if d.ID != id || string(d.Status) != expected || d.RecoveryCount >= max {
			continue
		}
		d.Status = store.StatusQueued
		d.ActiveJobID = jobID
		d.RecoveryCount++
		d.HasLease = false
		return *d, true, nil
	}
	return store.Deployment{}, false, nil
}

func (m *recoverMem) FailRecoverable(_ context.Context, id, expected, _ string, _ int, _ time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.rows {
		d := &m.rows[i]
		if d.ID != id || string(d.Status) != expected {
			continue
		}
		if d.Status == store.StatusLive {
			return false, nil
		}
		d.Status = store.StatusFailed
		return true, nil
	}
	return false, nil
}

func TestSweepRequeuesQueuedAndBuilding(t *testing.T) {
	mem := &recoverMem{rows: []store.Deployment{
		{ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Status: store.StatusQueued, RecoveryCount: 0},
		{ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1", Status: store.StatusBuilding, RecoveryCount: 1, ContainerName: "app-old"},
		{ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2", Status: store.StatusLive, RecoveryCount: 0},
	}}
	s := Sweeper{
		Store: mem,
		Max:   2,
		Stop: func(_ context.Context, name string) error {
			mem.mu.Lock()
			mem.stops = append(mem.stops, name)
			mem.mu.Unlock()
			return nil
		},
		Push: func(_ context.Context, _ store.Deployment, job queue.Job) error {
			mem.mu.Lock()
			mem.jobs = append(mem.jobs, job)
			mem.mu.Unlock()
			return nil
		},
	}
	if err := s.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(mem.jobs) != 2 {
		t.Fatalf("jobs = %d", len(mem.jobs))
	}
	if mem.rows[2].Status != store.StatusLive {
		t.Fatalf("live status = %q", mem.rows[2].Status)
	}
	if len(mem.stops) != 1 {
		t.Fatalf("stops = %#v", mem.stops)
	}
}

func TestSweepFailsExpiredHealthAndExhaustedRetries(t *testing.T) {
	mem := &recoverMem{rows: []store.Deployment{
		{ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee3", Status: store.StatusHealthCheck, RecoveryCount: 0, ContainerName: "attempt"},
		{ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee4", Status: store.StatusDetecting, RecoveryCount: 2},
	}}
	s := Sweeper{
		Store: mem,
		Max:   2,
		Stop: func(_ context.Context, name string) error {
			mem.mu.Lock()
			mem.stops = append(mem.stops, name)
			mem.mu.Unlock()
			return nil
		},
		Push: func(context.Context, store.Deployment, queue.Job) error {
			return errors.New("must not push a failed attempt")
		},
	}
	if err := s.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if mem.rows[0].Status != store.StatusFailed || mem.rows[1].Status != store.StatusFailed {
		t.Fatalf("rows = %+v", mem.rows)
	}
	if len(mem.stops) != 2 {
		t.Fatalf("stops = %#v", mem.stops)
	}
}
