package store

import (
	"testing"
	"time"
)

func TestStepWebhookDelivery(t *testing.T) {
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)

	lease, row := StepWebhookDelivery(now, DeliveryRow{})
	if !lease.Proceed || lease.Attempt != 1 || row.State != DeliveryPending {
		t.Fatalf("insert lease=%+v row=%+v", lease, row)
	}

	again, _ := StepWebhookDelivery(now.Add(time.Second), row)
	if again.Proceed || !again.Busy {
		t.Fatalf("concurrent retry should wait, not deploy: %+v", again)
	}

	row.State = DeliveryFailed
	row.DeploymentID = "dep-1"
	row.UpdatedAt = now
	recover, next := StepWebhookDelivery(now.Add(time.Second), row)
	if !recover.Proceed || !recover.Recover || recover.DeploymentID != "dep-1" || recover.Attempt != 2 {
		t.Fatalf("failed delivery did not recover: %+v", recover)
	}
	if next.State != DeliveryPending {
		t.Fatalf("recover state %s", next.State)
	}

	stale := next
	stale.UpdatedAt = now.Add(-DeliveryStale - time.Second)
	stolen, _ := StepWebhookDelivery(now.Add(time.Minute), stale)
	if !stolen.Proceed || !stolen.Recover || stolen.Attempt != 3 {
		t.Fatalf("stale pending was not taken over: %+v", stolen)
	}

	done := DeliveryRow{Exists: true, State: DeliveryQueued, Attempt: 3, UpdatedAt: now}
	dup, _ := StepWebhookDelivery(now.Add(time.Hour), done)
	if dup.Proceed || dup.Busy || dup.Recover {
		t.Fatalf("finished delivery must not run again: %+v", dup)
	}
}
