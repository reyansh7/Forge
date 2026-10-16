package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/reyansh7/Forge/internal/queue"
	"github.com/reyansh7/Forge/internal/runtime"
	"github.com/reyansh7/Forge/internal/schedule"
	"github.com/reyansh7/Forge/internal/store"
)

// DeploymentStore is the HTTP → persistence boundary for deploys.
//
// HTTP creates queued rows on an application and reads status.
// The worker owns transitions except operator stop (API → Docker stop).
type DeploymentStore interface {
	CreateDeployment(ctx context.Context, applicationID string) (store.Deployment, error)
	CreateDeploymentIn(ctx context.Context, applicationID, environment string) (store.Deployment, error)
	CreateRollbackDeployment(ctx context.Context, sourceID string) (store.Deployment, error)
	GetDeployment(ctx context.Context, id string) (store.Deployment, error)
	ListDeploymentsByProject(ctx context.Context, projectID string) ([]store.Deployment, error)
	ListDeploymentsByApplication(ctx context.Context, applicationID string) ([]store.Deployment, error)
	ListLiveDeployments(ctx context.Context) ([]store.Deployment, error)
	UpdateDeployment(ctx context.Context, d store.Deployment) error
	CountInProgressByOwner(ctx context.Context, ownerID string) (int, error)
	SetDeploymentCommit(ctx context.Context, id string, meta store.CommitMeta) (store.Deployment, error)
	// ClaimQueuedJob stamps the job id that the worker will accept.
	// It fails unless the row is still queued, so re-queue cannot
	// restart a pipeline the worker has already begun.
	ClaimQueuedJob(ctx context.Context, id, jobID string) (store.Deployment, error)
}

type deploymentResponse struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	ApplicationID string    `json:"application_id"`
	Status        string    `json:"status"`
	FailedStage   string    `json:"failed_stage,omitempty"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	RuntimeKind   string    `json:"runtime_kind,omitempty"`
	RuntimeType   string    `json:"runtime_type,omitempty"`
	PortSource    string    `json:"port_source,omitempty"`
	HostPort      int       `json:"host_port,omitempty"`
	ListenPort    int       `json:"listen_port,omitempty"`
	ContainerID   string    `json:"container_id,omitempty"`
	ContainerName string    `json:"container_name,omitempty"`
	PublicURL     string    `json:"public_url,omitempty"`
	ImageName     string    `json:"image_name,omitempty"`
	BuildLog      string    `json:"build_log,omitempty"`
	RollbackOf    string    `json:"rollback_of,omitempty"`
	LocalHost     string    `json:"local_host,omitempty"`
	Environment   string    `json:"environment,omitempty"`
	NodeID        string    `json:"node_id,omitempty"`
	CommitSHA     string    `json:"commit_sha,omitempty"`
	CommitMessage string    `json:"commit_message,omitempty"`
	CommitAuthor  string    `json:"commit_author,omitempty"`
	DurationMS    int64     `json:"duration_ms"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func deploymentResponseFrom(d store.Deployment) deploymentResponse {
	return deploymentResponse{
		ID:            d.ID,
		ProjectID:     d.ProjectID,
		ApplicationID: d.ApplicationID,
		Status:        string(d.Status),
		FailedStage:   d.FailedStage,
		ErrorMessage:  d.ErrorMessage,
		RuntimeKind:   d.RuntimeKind,
		RuntimeType:   d.RuntimeType,
		PortSource:    d.PortSource,
		HostPort:      d.HostPort,
		ListenPort:    d.ListenPort,
		ContainerID:   d.ContainerID,
		ContainerName: runtime.ResolveContainerName(d.ID, d.ContainerName),
		PublicURL:     d.PublicURL,
		ImageName:     d.ImageName,
		BuildLog:      d.BuildLog,
		RollbackOf:    d.RollbackOf,
		LocalHost:     d.LocalHost,
		Environment:   envOf(d),
		NodeID:        d.NodeID,
		CommitSHA:     d.CommitSHA,
		CommitMessage: d.CommitMessage,
		CommitAuthor:  d.CommitAuthor,
		DurationMS:    d.DurationMS(),
		CreatedAt:     d.CreatedAt.UTC(),
		UpdatedAt:     d.UpdatedAt.UTC(),
	}
}

type createDeploymentResponse struct {
	deploymentResponse
	JobID  string `json:"job_id"`
	Queued string `json:"queue_status"`
}

// createDeployment handles POST /projects/{id}/deployments.
//
// 202 means "row created and job queued", not "the app is live".
// The handler does not clone, build, or start containers.
func (s *Server) createDeployment(w http.ResponseWriter, r *http.Request) {
	if s.Projects == nil || s.Apps == nil || s.Deployments == nil || s.Jobs == nil {
		writeError(w, http.StatusInternalServerError, "deployments are not configured")
		return
	}

	projectID, err := store.ParseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	// Body is ignored on purpose: clients cannot supply a command or
	// Dockerfile. Extra JSON is still size-capped.
	var ignore struct{}
	if !decodeJSON(r, w, &ignore) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	if _, ok := s.requireProject(w, r, projectID); !ok {
		return
	}

	// Phase 1: a project may have several apps. This convenience path
	// only works when exactly one exists. Otherwise the client must
	// POST /applications/{id}/deployments.
	apps, err := s.Apps.ListApplicationsByProject(ctx, projectID)
	if err != nil {
		s.logger().Error("list apps for deploy failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create deployment")
		return
	}
	if len(apps) == 0 {
		writeError(w, http.StatusNotFound, "no applications on this project")
		return
	}
	if len(apps) > 1 {
		writeError(w, http.StatusConflict, "project has multiple applications; deploy an application id")
		return
	}

	s.enqueueDeployment(w, r, ctx, apps[0].ID, store.EnvProduction)
}

func (s *Server) enqueueDeployment(w http.ResponseWriter, r *http.Request, ctx context.Context, applicationID, environment string) {
	actor, ok := ActorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	d, prep := s.createQueuedDeployment(ctx, applicationID, environment, actor.UserID, store.CommitMeta{}, true)
	if prep.message != "" {
		writeError(w, prep.status, prep.message)
		return
	}
	s.enqueueQueued(w, r, ctx, d)
}

// queuePrep is a failed createQueuedDeployment. ignored is for webhooks:
// GitHub retries 4xx, so "already in progress" must be 200, not 409.
type queuePrep struct {
	status  int
	message string
	ignored string
}

func (s *Server) createQueuedDeployment(ctx context.Context, applicationID, environment, ownerID string, commit store.CommitMeta, countQuota bool) (store.Deployment, queuePrep) {
	if ownerID == "" {
		return store.Deployment{}, queuePrep{status: http.StatusUnauthorized, message: "authentication required"}
	}
	if !s.deployAttempts().allow(ownerID) {
		return store.Deployment{}, queuePrep{status: http.StatusTooManyRequests, message: "too many deploy attempts", ignored: "rate_limited"}
	}
	if countQuota {
		if prep := s.ownerQuotaPrep(ctx, ownerID); prep.message != "" {
			return store.Deployment{}, prep
		}
	}

	list, err := s.Deployments.ListDeploymentsByApplication(ctx, applicationID)
	if err != nil {
		s.logger().Error("list deployments before enqueue failed", "err", err)
		return store.Deployment{}, queuePrep{status: http.StatusInternalServerError, message: "failed to create deployment"}
	}
	for _, existing := range list {
		if existing.Status.InProgress() && store.SameEnvironment(existing.Environment, environment) {
			return store.Deployment{}, queuePrep{status: http.StatusConflict, message: "deployment already in progress", ignored: "in_progress"}
		}
	}
	for _, existing := range list {
		if existing.Status != store.StatusLive || !store.SameEnvironment(existing.Environment, environment) {
			continue
		}
		// A LIVE row whose container is gone is stale (Docker restart,
		// crash). Clear it so Deploy can recover. A running LIVE
		// container stays up: the worker starts the new container
		// alongside it and cuts over after health (rolling deploy).
		if s.Runtime == nil {
			continue
		}
		ok, runErr := s.Runtime.Running(ctx, runtime.ResolveContainerName(existing.ID, existing.ContainerName))
		if runErr != nil {
			s.logger().Error("inspect live container failed", "err", runErr)
			return store.Deployment{}, queuePrep{status: http.StatusInternalServerError, message: "failed to create deployment"}
		}
		if !ok {
			s.haltLive(ctx, existing, "container is no longer running")
		}
	}

	d, err := s.Deployments.CreateDeploymentIn(ctx, applicationID, environment)
	if errors.Is(err, store.ErrNotFound) {
		return store.Deployment{}, queuePrep{status: http.StatusNotFound, message: "application not found"}
	}
	if err != nil {
		s.logger().Error("create deployment failed", "err", err)
		return store.Deployment{}, queuePrep{status: http.StatusInternalServerError, message: "failed to create deployment"}
	}
	commit = store.SanitizeCommitMeta(commit)
	if commit.SHA != "" || commit.Message != "" || commit.Author != "" {
		updated, err := s.Deployments.SetDeploymentCommit(ctx, d.ID, commit)
		if err != nil {
			s.logger().Error("set deployment commit failed", "err", err)
			return store.Deployment{}, queuePrep{status: http.StatusInternalServerError, message: "failed to create deployment"}
		}
		d = updated
	}
	return d, queuePrep{}
}

func (s *Server) ownerQuotaPrep(ctx context.Context, ownerID string) queuePrep {
	max := s.inflightQuota()
	if max <= 0 {
		return queuePrep{}
	}
	n, err := s.Deployments.CountInProgressByOwner(ctx, ownerID)
	if err != nil {
		s.logger().Error("count in-progress deploys failed", "err", err)
		return queuePrep{status: http.StatusInternalServerError, message: "failed to create deployment"}
	}
	if n >= max {
		return queuePrep{status: http.StatusConflict, message: "deploy concurrency quota exceeded", ignored: "quota"}
	}
	return queuePrep{}
}

type enqueueOutcome struct {
	deployment store.Deployment
	jobID      string
	status     int
	message    string
}

func (s *Server) enqueueDeploymentJob(r *http.Request, ctx context.Context, d store.Deployment) enqueueOutcome {
	jobID, err := queue.NewID()
	if err != nil {
		s.logger().Error("job id failed", "err", err)
		return enqueueOutcome{status: http.StatusInternalServerError, message: "failed to enqueue deployment"}
	}
	payload, err := json.Marshal(struct {
		DeploymentID string `json:"deployment_id"`
	}{DeploymentID: d.ID})
	if err != nil {
		return enqueueOutcome{status: http.StatusInternalServerError, message: "failed to enqueue deployment"}
	}
	job := queue.Job{ID: jobID, Type: queue.TypeDeploy, Payload: payload}

	// Persist the claim before RPUSH. A crash between the two leaves a
	// queued row the operator can re-queue. The worker ignores any job
	// whose id is not the one stored here.
	claimed, err := s.Deployments.ClaimQueuedJob(ctx, d.ID, jobID)
	if errors.Is(err, store.ErrNotFound) {
		return enqueueOutcome{status: http.StatusNotFound, message: "deployment not found"}
	}
	if errors.Is(err, store.ErrConflict) {
		return enqueueOutcome{status: http.StatusConflict, message: "only a queued deployment can be re-queued"}
	}
	if err != nil {
		s.logger().Error("claim deploy job failed", "err", err)
		return enqueueOutcome{status: http.StatusInternalServerError, message: "failed to enqueue deployment"}
	}
	d = claimed

	placed, err := s.placeDeployment(ctx, d)
	if err != nil {
		if errors.Is(err, schedule.ErrNoCapacity) {
			s.failQueuedDeploy(ctx, d, err.Error())
			return enqueueOutcome{status: http.StatusConflict, message: "no ready worker node with capacity"}
		}
		s.logger().Error("place deploy failed", "err", err)
		s.failQueuedDeploy(ctx, d, "failed to place deployment")
		return enqueueOutcome{status: http.StatusServiceUnavailable, message: "failed to place deployment"}
	}
	d = placed

	if err := s.pushDeployJob(ctx, d, job); err != nil {
		s.logger().Error("enqueue deploy failed", "err", err)
		d.ErrorMessage = "redis did not accept this job; re-queue the deployment"
		_ = s.Deployments.UpdateDeployment(ctx, d)
		return enqueueOutcome{status: http.StatusServiceUnavailable, message: "deployment stayed queued; re-queue it when Redis is back"}
	}

	action := "deployment.enqueue"
	if d.RollbackOf != "" {
		action = "deployment.rollback"
	}
	s.Metrics.IncDeploysQueued()
	s.audit(r, "", action, "deployment", d.ID, map[string]string{"application_id": d.ApplicationID})
	return enqueueOutcome{deployment: d, jobID: jobID, status: http.StatusAccepted}
}

func (s *Server) enqueueQueued(w http.ResponseWriter, r *http.Request, ctx context.Context, d store.Deployment) {
	out := s.enqueueDeploymentJob(r, ctx, d)
	if out.status != http.StatusAccepted {
		writeError(w, out.status, out.message)
		return
	}
	w.Header().Set("Location", "/deployments/"+out.deployment.ID)
	writeJSON(w, http.StatusAccepted, createDeploymentResponse{
		deploymentResponse: deploymentResponseFrom(out.deployment),
		JobID:              out.jobID,
		Queued:             "queued",
	})
}

// pushDeployJob RPUSHes onto the placed node's list, or the shared
// list when this process has no scheduler (unit tests).
func (s *Server) pushDeployJob(ctx context.Context, d store.Deployment, job queue.Job) error {
	if d.NodeID != "" && s.Sink != nil {
		return s.Sink.EnqueueOn(ctx, d.NodeID, job)
	}
	return s.Jobs.Enqueue(ctx, job)
}

// retryDeployment re-pushes a queued row whose Redis job was lost.
//
// It does not create a new attempt. A failed or in-progress row stays
// as it is: Deploy starts a new attempt, Stop abandons a dead worker's
// pipeline. The new job id invalidates any older list entry.
func (s *Server) retryDeployment(w http.ResponseWriter, r *http.Request) {
	if s.Deployments == nil || s.Jobs == nil {
		writeError(w, http.StatusInternalServerError, "deployments are not configured")
		return
	}
	id, err := store.ParseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid deployment id")
		return
	}
	var ignore struct{}
	if !decodeJSON(r, w, &ignore) {
		return
	}
	actor, ok := ActorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if !s.deployAttempts().allow(actor.UserID) {
		writeError(w, http.StatusTooManyRequests, "too many deploy attempts")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	d, err := s.Deployments.GetDeployment(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	}
	if err != nil {
		s.logger().Error("get deployment for re-queue failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to re-queue deployment")
		return
	}
	if !s.actorOwnsProject(w, r, d.ProjectID, "deployment not found") {
		return
	}
	if d.Status != store.StatusQueued {
		writeError(w, http.StatusConflict, "only a queued deployment can be re-queued; deploy again to start a new attempt")
		return
	}
	s.enqueueQueued(w, r, ctx, d)
}

func (s *Server) listDeployments(w http.ResponseWriter, r *http.Request) {
	if s.Deployments == nil {
		writeError(w, http.StatusInternalServerError, "deployments are not configured")
		return
	}
	projectID, err := store.ParseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if _, ok := s.requireProject(w, r, projectID); !ok {
		return
	}

	list, err := s.Deployments.ListDeploymentsByProject(ctx, projectID)
	if err != nil {
		s.logger().Error("list deployments failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list deployments")
		return
	}

	out := make([]deploymentResponse, 0, len(list))
	for _, d := range list {
		out = append(out, deploymentResponseFrom(d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getDeployment(w http.ResponseWriter, r *http.Request) {
	if s.Deployments == nil {
		writeError(w, http.StatusInternalServerError, "deployments are not configured")
		return
	}
	id, err := store.ParseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid deployment id")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	d, err := s.Deployments.GetDeployment(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	}
	if err != nil {
		s.logger().Error("get deployment failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to get deployment")
		return
	}
	if !s.actorOwnsProject(w, r, d.ProjectID, "deployment not found") {
		return
	}
	writeJSON(w, http.StatusOK, deploymentResponseFrom(d))
}

// rollbackDeployment handles POST /deployments/{id}/rollback.
//
// {id} is the source row, not the new one. The client cannot supply an
// image name or a command — we copy image_name from that row. Rolling
// rules match Deploy: a running LIVE container stays up until the
// rollback image is healthy.
func (s *Server) rollbackDeployment(w http.ResponseWriter, r *http.Request) {
	if s.Deployments == nil || s.Jobs == nil {
		writeError(w, http.StatusInternalServerError, "deployments are not configured")
		return
	}
	id, err := store.ParseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid deployment id")
		return
	}
	var ignore struct{}
	if !decodeJSON(r, w, &ignore) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	source, err := s.Deployments.GetDeployment(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	}
	if err != nil {
		s.logger().Error("get rollback source failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to roll back")
		return
	}
	if !s.actorOwnsProject(w, r, source.ProjectID, "deployment not found") {
		return
	}
	if strings.TrimSpace(source.ImageName) == "" {
		writeError(w, http.StatusConflict, "that deployment has no image to roll back to")
		return
	}

	s.enqueueRollback(w, r, ctx, source)
}

func (s *Server) enqueueRollback(w http.ResponseWriter, r *http.Request, ctx context.Context, source store.Deployment) {
	list, err := s.Deployments.ListDeploymentsByApplication(ctx, source.ApplicationID)
	if err != nil {
		s.logger().Error("list deployments before rollback failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to roll back")
		return
	}
	for _, existing := range list {
		if existing.Status.InProgress() && store.SameEnvironment(existing.Environment, source.Environment) {
			writeError(w, http.StatusConflict, "deployment already in progress")
			return
		}
	}
	for _, existing := range list {
		if existing.Status != store.StatusLive || !store.SameEnvironment(existing.Environment, source.Environment) {
			continue
		}
		if s.Runtime == nil {
			continue
		}
		ok, runErr := s.Runtime.Running(ctx, runtime.ResolveContainerName(existing.ID, existing.ContainerName))
		if runErr != nil {
			s.logger().Error("inspect live container failed", "err", runErr)
			writeError(w, http.StatusInternalServerError, "failed to roll back")
			return
		}
		if !ok {
			s.haltLive(ctx, existing, "container is no longer running")
		}
	}

	d, err := s.Deployments.CreateRollbackDeployment(ctx, source.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusConflict, "that deployment has no image to roll back to")
		return
	}
	if err != nil {
		s.logger().Error("create rollback failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to roll back")
		return
	}
	s.enqueueQueued(w, r, ctx, d)
}
