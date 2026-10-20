package httpapi

import (
	"sort"
	"strings"
	"time"

	"github.com/reyansh7/Forge/internal/observe"
	"github.com/reyansh7/Forge/internal/store"
)

// metricsService is one application in one environment, built only from
// rows and probes the metrics handler already loaded. Fields that Forge
// does not collect are the literal "unavailable" plus a reason, so the
// dashboard cannot render a zero that looks like a measurement.
type metricsService struct {
	ApplicationID      string `json:"application_id"`
	ApplicationName    string `json:"application_name"`
	ProjectID          string `json:"project_id"`
	Environment        string `json:"environment"`
	Health             string `json:"health"`
	HealthReason       string `json:"health_reason"`
	ReleaseID          string `json:"release_id,omitempty"`
	ReleaseStatus      string `json:"release_status,omitempty"`
	ReleaseSHA         string `json:"release_sha,omitempty"`
	Attempts           int    `json:"attempts"`
	Container          string `json:"container"`
	StatusAt           string `json:"status_at,omitempty"`
	FailedDeploymentID string `json:"failed_deployment_id,omitempty"`
	FailedStage        string `json:"failed_stage,omitempty"`
	FailedReason       string `json:"failed_reason,omitempty"`
	RestartCount       string `json:"restart_count"`
	RestartReason      string `json:"restart_count_reason"`
	Resources          string `json:"resources"`
	ResourcesReason    string `json:"resources_reason"`
	Uptime             string `json:"uptime"`
	UptimeReason       string `json:"uptime_reason"`
}

const (
	unavailableValue = "unavailable"
	restartReason    = "Docker inspect RestartCount is not read. The runtime check only reports whether the container is running."
	resourceReason   = "CPU and memory are not collected. A docker stats or cgroup read would be required, and this API does not call either."
	uptimeReason     = "Forge records when the deployment row last changed status, not when the container process started."
	queueDepthReason = "deploys_queued_total counts jobs this API process accepted since it started. The current Redis list length is not read."
)

// probeByDeployment is keyed by deployment id. A missing key means this
// refresh did not inspect that container (the 20-probe cap, or the row
// is not live). probe_error means inspect failed.
func buildServiceMetrics(apps []store.Application, deps []store.Deployment, probes map[string]string, deadNodes map[string]bool) []metricsService {
	type bucket struct {
		app     store.Application
		env     string
		rows    []store.Deployment
		haveApp bool
	}
	order := make([]string, 0)
	byKey := map[string]*bucket{}
	ensure := func(app store.Application, env string) *bucket {
		if env == "" {
			env = "production"
		}
		key := app.ID + "\x00" + env
		b, ok := byKey[key]
		if ok {
			return b
		}
		b = &bucket{app: app, env: env, haveApp: app.ID != ""}
		byKey[key] = b
		order = append(order, key)
		return b
	}
	names := map[string]store.Application{}
	for _, app := range apps {
		names[app.ID] = app
		// An application with no deployment still appears, so an empty
		// project is not confused with a hidden failure.
		ensure(app, "production")
	}
	for _, d := range deps {
		app := names[d.ApplicationID]
		if app.ID == "" {
			app = store.Application{ID: d.ApplicationID, ProjectID: d.ProjectID, Name: d.ApplicationID}
		}
		env := d.Environment
		if env == "" {
			env = "production"
		}
		b := ensure(app, env)
		b.rows = append(b.rows, d)
	}

	out := make([]metricsService, 0, len(order))
	for _, key := range order {
		b := byKey[key]
		if b.app.ID == "" {
			continue
		}
		// The placeholder production row exists so an app with no
		// deployments is visible. Once a real environment has rows,
		// drop the empty production placeholder.
		if len(b.rows) == 0 {
			hasOther := false
			for k, other := range byKey {
				if other.app.ID == b.app.ID && k != key && len(other.rows) > 0 {
					hasOther = true
					break
				}
			}
			if hasOther {
				continue
			}
		}
		out = append(out, assessBucket(b.app, b.env, b.rows, probes, deadNodes))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if healthRank(out[i].Health) != healthRank(out[j].Health) {
			return healthRank(out[i].Health) < healthRank(out[j].Health)
		}
		if out[i].ApplicationName != out[j].ApplicationName {
			return out[i].ApplicationName < out[j].ApplicationName
		}
		return out[i].Environment < out[j].Environment
	})
	return out
}

func assessBucket(app store.Application, env string, rows []store.Deployment, probes map[string]string, deadNodes map[string]bool) metricsService {
	var live *store.Deployment
	var latest store.Deployment
	var newerFailed *store.Deployment
	for i := range rows {
		d := &rows[i]
		if latest.ID == "" || d.CreatedAt.After(latest.CreatedAt) {
			latest = *d
		}
		if d.Status == store.StatusLive && (live == nil || d.CreatedAt.After(live.CreatedAt)) {
			copy := *d
			live = &copy
		}
	}
	if live != nil {
		for i := range rows {
			d := &rows[i]
			if d.Status != store.StatusFailed || !d.CreatedAt.After(live.CreatedAt) {
				continue
			}
			if newerFailed == nil || d.CreatedAt.After(newerFailed.CreatedAt) {
				copy := *d
				newerFailed = &copy
			}
		}
	}
	name := strings.TrimSpace(app.Name)
	if name == "" {
		name = app.ID
	}
	release := latest
	if live != nil {
		release = *live
	}
	probe := observe.ProbeNotProbed
	if live != nil {
		switch probes[live.ID] {
		case string(observe.ProbeRunning):
			probe = observe.ProbeRunning
		case string(observe.ProbeNotRunning):
			probe = observe.ProbeNotRunning
		case string(observe.ProbeError):
			probe = observe.ProbeError
		default:
			probe = observe.ProbeNotProbed
		}
	}
	workerStale := live != nil && live.NodeID != "" && deadNodes[live.NodeID]
	assessed := observe.AssessService(observe.ServiceInput{
		HasLive:            live != nil,
		LatestStatus:       string(latest.Status),
		LatestInProgress:   latest.Status.InProgress(),
		Probe:              probe,
		WorkerStale:        workerStale,
		NewerAttemptFailed: newerFailed != nil,
	})
	row := metricsService{
		ApplicationID:   app.ID,
		ApplicationName: name,
		ProjectID:       app.ProjectID,
		Environment:     env,
		Health:          assessed.Health,
		HealthReason:    assessed.Reason,
		Attempts:        len(rows),
		Container:       string(probe),
		RestartCount:    unavailableValue,
		RestartReason:   restartReason,
		Resources:       unavailableValue,
		ResourcesReason: resourceReason,
		Uptime:          unavailableValue,
		UptimeReason:    uptimeReason,
	}
	if app.ProjectID == "" {
		row.ProjectID = release.ProjectID
	}
	if release.ID != "" {
		row.ReleaseID = release.ID
		row.ReleaseStatus = string(release.Status)
		row.ReleaseSHA = release.CommitSHA
		if !release.UpdatedAt.IsZero() {
			row.StatusAt = release.UpdatedAt.UTC().Format(time.RFC3339)
		}
	}
	failed := newerFailed
	if failed == nil && latest.Status == store.StatusFailed {
		failed = &latest
	}
	if failed != nil {
		row.FailedDeploymentID = failed.ID
		row.FailedStage = string(failed.FailedStage)
		row.FailedReason = clipPublicReason(failed.ErrorMessage)
	}
	return row
}

func healthRank(health string) int {
	switch health {
	case observe.HealthUnhealthy:
		return 0
	case observe.HealthStale:
		return 1
	case observe.HealthDegraded:
		return 2
	case observe.HealthUnknown:
		return 3
	default:
		return 4
	}
}

func clipPublicReason(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// The deployment row already passed SanitizeErrorMessage when it
	// was saved. Clip again so a long builder tail cannot dominate the
	// metrics payload. This does not print the value anywhere else.
	const max = 240
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func countApplications(services []metricsService) (observed, unique int) {
	seen := map[string]struct{}{}
	for _, s := range services {
		observed++
		if s.ApplicationID != "" {
			seen[s.ApplicationID] = struct{}{}
		}
	}
	return observed, len(seen)
}
