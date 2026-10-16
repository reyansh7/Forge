package deploy

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/reyansh7/Forge/internal/queue"
	"github.com/reyansh7/Forge/internal/runtime"
	"github.com/reyansh7/Forge/internal/store"
)

// RecoveryStore is the sweeper → PostgreSQL boundary.
//
// The sweeper decides eligibility from a list, then a conditional
// UPDATE so two API processes cannot both re-queue the same row.
type RecoveryStore interface {
	ListRecoverableDeployments(ctx context.Context, grace time.Duration) ([]store.Deployment, error)
	RequeueRecoverable(ctx context.Context, id, expectedStatus, jobID string, max int, grace time.Duration) (store.Deployment, bool, error)
	FailRecoverable(ctx context.Context, id, expectedStatus, message string, max int, grace time.Duration) (bool, error)
}

// Sweeper reclaims attempts whose Redis message or worker lease is gone.
//
// Redis stays a notification list. Postgres says which attempt is
// recoverable. A failed health check's previous LIVE row is a different
// deployment id and is not updated here. Containers are stopped only
// after the conditional update wins, and only for this attempt's name.
type Sweeper struct {
	Log   *slog.Logger
	Store RecoveryStore
	Grace time.Duration
	Max   int
	// Stop removes this attempt's container. Nil skips the call.
	// Errors are logged and do not undo the database transition.
	Stop func(ctx context.Context, containerName string) error
	// Push RPUSHes the new job. The deployment's node_id is a hint;
	// the caller may place a different ready node.
	Push func(ctx context.Context, d store.Deployment, job queue.Job) error
}

func (s Sweeper) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s Sweeper) grace() time.Duration {
	if s.Grace > 0 {
		return s.Grace
	}
	return store.RecoverGrace
}

func (s Sweeper) max() int {
	if s.Max > 0 {
		return s.Max
	}
	return store.MaxRecoveries
}

// Once runs a single recovery pass.
func (s Sweeper) Once(ctx context.Context) error {
	if s.Store == nil {
		return nil
	}
	list, err := s.Store.ListRecoverableDeployments(ctx, s.grace())
	if err != nil {
		return err
	}
	for _, d := range list {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.canRequeue(d) {
			if err := s.requeue(ctx, d); err != nil {
				s.log().Error("requeue recoverable deploy failed", "deployment_id", d.ID, "err", err)
			}
			continue
		}
		if err := s.fail(ctx, d); err != nil {
			s.log().Error("fail recoverable deploy failed", "deployment_id", d.ID, "err", err)
		}
	}
	return nil
}

func (s Sweeper) canRequeue(d store.Deployment) bool {
	if d.RecoveryCount >= s.max() {
		return false
	}
	switch d.Status {
	case store.StatusQueued, store.StatusDetecting, store.StatusBuilding:
		return true
	default:
		return false
	}
}

func (s Sweeper) requeue(ctx context.Context, d store.Deployment) error {
	jobID, err := queue.NewID()
	if err != nil {
		return err
	}
	next, ok, err := s.Store.RequeueRecoverable(ctx, d.ID, string(d.Status), jobID, s.max(), s.grace())
	if err != nil || !ok {
		return err
	}
	// The row is queued again. Stop only the attempt we abandoned.
	// A previous LIVE container has a different name.
	s.stopAttempt(ctx, d)
	if s.Push == nil {
		s.log().Info("requeued deployment without a queue push", "deployment_id", d.ID)
		return nil
	}
	payload, err := json.Marshal(deployPayload{DeploymentID: next.ID})
	if err != nil {
		return err
	}
	job := queue.Job{ID: jobID, Type: queue.TypeDeploy, Payload: payload}
	if err := s.Push(ctx, next, job); err != nil {
		// The row stays queued with the new job id. The next pass waits
		// out RecoverGrace and tries again until MaxRecoveries.
		return err
	}
	s.log().Info("requeued abandoned deployment", "deployment_id", d.ID, "was", d.Status, "recovery_count", next.RecoveryCount)
	return nil
}

func (s Sweeper) fail(ctx context.Context, d store.Deployment) error {
	msg := "worker lease expired; the previous live release was left in place"
	if d.Status == store.StatusQueued {
		msg = "queue delivery failed after automatic retries; deploy again to start a new attempt"
	}
	ok, err := s.Store.FailRecoverable(ctx, d.ID, string(d.Status), msg, s.max(), s.grace())
	if err != nil || !ok {
		return err
	}
	s.stopAttempt(ctx, d)
	s.log().Info("closed abandoned deployment", "deployment_id", d.ID, "was", d.Status)
	return nil
}

func (s Sweeper) stopAttempt(ctx context.Context, d store.Deployment) {
	if s.Stop == nil || d.Status == store.StatusQueued {
		return
	}
	name := runtime.ResolveContainerName(d.ID, d.ContainerName)
	if err := s.Stop(ctx, name); err != nil {
		s.log().Error("stop abandoned container failed", "deployment_id", d.ID, "container", name, "err", err)
	}
}

// RunSweep calls Once immediately, then on every interval, until ctx ends.
// Immediate first pass is what makes a worker restart recoverable without
// waiting out the full tick.
func RunSweep(ctx context.Context, s Sweeper, every time.Duration) {
	if every <= 0 {
		every = 20 * time.Second
	}
	if err := s.Once(ctx); err != nil {
		s.log().Error("deployment recovery sweep failed", "err", err)
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.Once(ctx); err != nil {
				s.log().Error("deployment recovery sweep failed", "err", err)
			}
		}
	}
}
