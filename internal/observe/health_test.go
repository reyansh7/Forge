package observe

import (
	"strings"
	"testing"
)

func TestAssessService(t *testing.T) {
	cases := []struct {
		name   string
		in     ServiceInput
		health string
		// needle must appear, and the reason must not call a missing
		// probe a stopped container.
		needle    string
		forbidden string
	}{
		{
			name:      "failed attempt is unhealthy",
			in:        ServiceInput{LatestStatus: "failed"},
			health:    HealthUnhealthy,
			needle:    "failed attempt",
			forbidden: "not running",
		},
		{
			name:      "operator stop is not a failed probe",
			in:        ServiceInput{LatestStatus: "stopped"},
			health:    HealthUnknown,
			needle:    "not a failed health check",
			forbidden: "not running",
		},
		{
			name:      "in progress has nothing to probe",
			in:        ServiceInput{LatestStatus: "building", LatestInProgress: true},
			health:    HealthUnknown,
			needle:    "still in progress",
			forbidden: "not running",
		},
		{
			name:      "no release is unknown",
			in:        ServiceInput{},
			health:    HealthUnknown,
			needle:    "not proof",
			forbidden: "not running",
		},
		{
			name:      "skipped probe is not stopped",
			in:        ServiceInput{HasLive: true, Probe: ProbeNotProbed},
			health:    HealthUnknown,
			needle:    "did not probe",
			forbidden: "not running",
		},
		{
			name:      "inspect error is not stopped",
			in:        ServiceInput{HasLive: true, Probe: ProbeError},
			health:    HealthUnknown,
			needle:    "not confirmed stopped",
			forbidden: "not running",
		},
		{
			name:      "stale worker and skipped probe",
			in:        ServiceInput{HasLive: true, Probe: ProbeNotProbed, WorkerStale: true},
			health:    HealthStale,
			needle:    "not proof the container stopped",
			forbidden: "not running",
		},
		{
			name:      "stale worker and inspect error",
			in:        ServiceInput{HasLive: true, Probe: ProbeError, WorkerStale: true},
			health:    HealthStale,
			needle:    "not confirmed stopped",
			forbidden: "not running",
		},
		{
			name:   "inspect says not running",
			in:     ServiceInput{HasLive: true, Probe: ProbeNotRunning},
			health: HealthUnhealthy,
			needle: "not running",
		},
		{
			name:   "running is healthy",
			in:     ServiceInput{HasLive: true, Probe: ProbeRunning},
			health: HealthHealthy,
			needle: "running",
		},
		{
			name:   "running with a failed follow-up is degraded",
			in:     ServiceInput{HasLive: true, Probe: ProbeRunning, NewerAttemptFailed: true},
			health: HealthDegraded,
			needle: "was not switched",
		},
		{
			name:   "running on a stale worker is degraded",
			in:     ServiceInput{HasLive: true, Probe: ProbeRunning, WorkerStale: true},
			health: HealthDegraded,
			needle: "stopped heartbeating",
		},
		{
			name: "running, stale worker, and failed follow-up",
			in: ServiceInput{
				HasLive: true, Probe: ProbeRunning, WorkerStale: true, NewerAttemptFailed: true,
			},
			health: HealthDegraded,
			needle: "was not switched",
		},
		{
			name:      "unrecognized probe is unknown",
			in:        ServiceInput{HasLive: true, Probe: "something-else"},
			health:    HealthUnknown,
			needle:    "did not probe",
			forbidden: "not running",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AssessService(tc.in)
			if got.Health != tc.health {
				t.Fatalf("health = %q, want %q (%s)", got.Health, tc.health, got.Reason)
			}
			if got.Reason == "" || !strings.Contains(got.Reason, tc.needle) {
				t.Fatalf("reason %q does not contain %q", got.Reason, tc.needle)
			}
			if tc.forbidden != "" && strings.Contains(got.Reason, tc.forbidden) {
				t.Fatalf("reason %q contains forbidden %q", got.Reason, tc.forbidden)
			}
		})
	}
}
