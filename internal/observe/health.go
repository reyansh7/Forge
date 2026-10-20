package observe

// Service health is a label over records Forge already has.
//
// It is not a new probe. GET /metrics still asks Docker only whether a
// live container is running, and only for a bounded number of rows.
// AssessService turns that fact, plus the deployment status and the
// worker heartbeat, into one of five words the dashboard can show
// without guessing.
//
// healthy   — a live release was probed and the container is running.
// degraded  — that container is running, and something else is wrong
//             (a newer attempt failed, or the worker stopped heartbeating).
// unhealthy — the latest attempt failed and nothing is live, or Docker
//             reported the live container is not running.
// unknown   — Forge did not get a probe result. That is not proof the
//             container stopped.
// stale     — the worker missed heartbeats and this refresh also failed
//             to confirm the container.

const (
	HealthHealthy   = "healthy"
	HealthDegraded  = "degraded"
	HealthUnhealthy = "unhealthy"
	HealthUnknown   = "unknown"
	HealthStale     = "stale"
)

// ProbeResult is the outcome of one docker inspect, or the reason it
// did not happen. not_running means inspect succeeded and reported the
// container stopped. not_probed and probe_error mean Forge does not
// know, which AssessService must not collapse into not_running.
type ProbeResult string

const (
	ProbeRunning    ProbeResult = "running"
	ProbeNotRunning ProbeResult = "not_running"
	ProbeNotProbed  ProbeResult = "not_probed"
	ProbeError      ProbeResult = "probe_error"
)

// ServiceInput is the evidence for one application in one environment.
// LatestInProgress is the caller's store.Status.InProgress so this
// package does not keep a second copy of the state machine.
type ServiceInput struct {
	HasLive            bool
	LatestStatus       string
	LatestInProgress   bool
	Probe              ProbeResult
	WorkerStale        bool
	NewerAttemptFailed bool
}

// Assessment is the dashboard label and the sentence that justifies it.
type Assessment struct {
	Health string
	Reason string
}

// AssessService maps evidence to a health word. Missing or failed
// probes stay unknown or stale. They never become "stopped".
func AssessService(in ServiceInput) Assessment {
	if !in.HasLive {
		switch {
		case in.LatestStatus == "failed":
			return Assessment{
				Health: HealthUnhealthy,
				Reason: "The latest deployment failed, and there is no live release. This is a failed attempt, not a missing health probe.",
			}
		case in.LatestStatus == "stopped":
			return Assessment{
				Health: HealthUnknown,
				Reason: "The last release was stopped. Forge did not probe a container, so this is not a failed health check.",
			}
		case in.LatestInProgress:
			return Assessment{
				Health: HealthUnknown,
				Reason: "A deployment is still in progress. There is no live container to probe yet.",
			}
		default:
			return Assessment{
				Health: HealthUnknown,
				Reason: "This application has no live release. Missing probe data is not proof that a container stopped.",
			}
		}
	}

	switch in.Probe {
	case ProbeNotRunning:
		return Assessment{
			Health: HealthUnhealthy,
			Reason: "Docker inspect reported the container is not running.",
		}
	case ProbeError:
		if in.WorkerStale {
			return Assessment{
				Health: HealthStale,
				Reason: "The worker missed heartbeats, and Docker inspect failed. The container was not confirmed stopped.",
			}
		}
		return Assessment{
			Health: HealthUnknown,
			Reason: "Docker inspect failed. The container was not confirmed stopped.",
		}
	case ProbeRunning:
		switch {
		case in.WorkerStale && in.NewerAttemptFailed:
			return Assessment{
				Health: HealthDegraded,
				Reason: "The container is running. The worker has stopped heartbeating, and a newer deployment attempt failed. Traffic was not switched to that attempt.",
			}
		case in.WorkerStale:
			return Assessment{
				Health: HealthDegraded,
				Reason: "The container is running, but its worker has stopped heartbeating.",
			}
		case in.NewerAttemptFailed:
			return Assessment{
				Health: HealthDegraded,
				Reason: "This release is running, and a newer deployment attempt failed. Traffic was not switched.",
			}
		default:
			return Assessment{
				Health: HealthHealthy,
				Reason: "The container is running.",
			}
		}
	default:
		// not_probed and any unrecognized value. The cap of 20 live
		// containers, or a row that was never inspected, lands here.
		if in.WorkerStale {
			return Assessment{
				Health: HealthStale,
				Reason: "The worker missed heartbeats, and this refresh did not probe the container. Missing probe data is not proof the container stopped.",
			}
		}
		return Assessment{
			Health: HealthUnknown,
			Reason: "This refresh did not probe the container. Missing probe data is not proof the container stopped.",
		}
	}
}
