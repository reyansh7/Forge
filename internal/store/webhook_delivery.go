package store

import "time"

const (
	// DeliveryPending means one in-flight request owns this GitHub
	// delivery. A second request must not create another deployment.
	DeliveryPending = "pending"
	// DeliveryFailed means the push was accepted but no job was queued.
	// GitHub's retry of the same delivery id may continue.
	DeliveryFailed = "failed"
	// DeliveryQueued means this delivery has been decided. A repeat
	// must not start a second release.
	DeliveryQueued = "queued"

	// DeliveryStale is how long a pending row blocks other retries.
	// The webhook handler's own timeout is shorter, so a pending row
	// older than this belongs to a request that is no longer running.
	DeliveryStale = 20 * time.Second
)

// DeliveryRow is the stored state of one X-GitHub-Delivery.
// Exists is false when Begin has not inserted it yet.
type DeliveryRow struct {
	Exists       bool
	State        string
	Attempt      int
	DeploymentID string
	UpdatedAt    time.Time
}

// WebhookLease is what BeginWebhookDelivery gives the HTTP handler.
//
// Proceed is true only for the request that may create or re-queue.
// Busy means another request still holds a fresh pending row: the
// caller returns 503 so GitHub retries, and must not answer 200.
// Recover means a previous attempt failed or was abandoned. The
// caller re-queues DeploymentID when that row is still queued
// instead of inserting a second deployment.
type WebhookLease struct {
	Proceed      bool
	Busy         bool
	Recover      bool
	Attempt      int
	DeploymentID string
}

// StepWebhookDelivery is the delivery state machine, without a database.
//
// Deleting the row on failure is not safe: a concurrent retry can
// observe the row, tell GitHub the push was handled, and then lose
// the race when the first request deletes it. GitHub will not deliver
// that id again. The attempt number makes the same race harmless:
// only the request that moved the row to pending may mark it queued
// or failed.
func StepWebhookDelivery(now time.Time, row DeliveryRow) (WebhookLease, DeliveryRow) {
	if !row.Exists {
		next := DeliveryRow{
			Exists:    true,
			State:     DeliveryPending,
			Attempt:   1,
			UpdatedAt: now,
		}
		return WebhookLease{Proceed: true, Attempt: 1}, next
	}
	switch row.State {
	case DeliveryQueued:
		return WebhookLease{Attempt: row.Attempt}, row
	case DeliveryPending:
		if now.Sub(row.UpdatedAt) < DeliveryStale {
			return WebhookLease{Busy: true, Attempt: row.Attempt}, row
		}
		row.Attempt++
		row.State = DeliveryPending
		row.UpdatedAt = now
		return WebhookLease{
			Proceed: true, Recover: true, Attempt: row.Attempt, DeploymentID: row.DeploymentID,
		}, row
	case DeliveryFailed:
		id := row.DeploymentID
		row.Attempt++
		row.State = DeliveryPending
		row.UpdatedAt = now
		return WebhookLease{
			Proceed: true, Recover: true, Attempt: row.Attempt, DeploymentID: id,
		}, row
	default:
		return WebhookLease{Busy: true, Attempt: row.Attempt}, row
	}
}
