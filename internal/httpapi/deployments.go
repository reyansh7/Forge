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
	"github.com/reyansh7/Forge/internal/store"
)

// DeploymentStore is the HTTP → persistence boundary for deploys.
//
// HTTP creates queued rows on an application and reads status.
// The worker owns transitions except operator stop (API → Docker stop).
type DeploymentStore interface {
	CreateDeployment(ctx context.Context, applicationID string) (store.Deployment, error)
	CreateRollbackDeployment(ctx context.Context, sourceID string) (store.Deployment, error)
	GetDeployment(ctx context.Context, id string) (store.Deployment, error)
	ListDeploymentsByProject(ctx context.Context, projectID string) ([]store.Deployment, error)
	ListDeploymentsByApplication(ctx context.Context, applicationID string) ([]store.Deployment, error)
	ListLiveDeployments(ctx context.Context) ([]store.Deployment, error)
	UpdateDeployment(ctx context.Context, d store.Deployment) error
}

type deploymentResponse struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	ApplicationID string    `json:"application_id"`
	Status        string    `json:"status"`
	FailedStage   string    `json:"failed_stage,omitempty"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	RuntimeKind   string    `json:"runtime_kind,omitempty"`
	PublicURL     string    `json:"public_url,omitempty"`
	ImageName     string    `json:"image_name,omitempty"`
	BuildLog      string    `json:"build_log,omitempty"`
	RollbackOf    string    `json:"rollback_of,omitempty"`
	LocalHost     string    `json:"local_host,omitempty"`
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
		PublicURL:     d.PublicURL,
		ImageName:     d.ImageName,
		BuildLog:      d.BuildLog,
		RollbackOf:    d.RollbackOf,
		LocalHost:     d.LocalHost,
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

	s.enqueueDeployment(w, r, ctx, apps[0].ID)
}

func (s *Server) enqueueDeployment(w http.ResponseWriter, r *http.Request, ctx context.Context, applicationID string) {
	list, err := s.Deployments.ListDeploymentsByApplication(ctx, applicationID)
	if err != nil {
		s.logger().Error("list deployments before enqueue failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create deployment")
		return
	}
	for _, existing := range list {
		if existing.Status.InProgress() {
			writeError(w, http.StatusConflict, "deployment already in progress")
			return
		}
	}
	for _, existing := range list {
		if existing.Status != store.StatusLive {
			continue
		}
		// A LIVE row whose container is gone is stale (Docker restart,
		// crash). Clear it so Deploy can recover. A container that is
		// actually running must be Stopped first — do not stack another.
		running := true
		if s.Runtime != nil {
			ok, runErr := s.Runtime.Running(ctx, runtime.ContainerName(existing.ID))
			if runErr != nil {
				s.logger().Error("inspect live container failed", "err", runErr)
				writeError(w, http.StatusInternalServerError, "failed to create deployment")
				return
			}
			running = ok
		}
		if running {
			writeError(w, http.StatusConflict, "application is already live; stop it before deploying again")
			return
		}
		s.haltLive(ctx, existing, "container is no longer running")
	}

	d, err := s.Deployments.CreateDeployment(ctx, applicationID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}
	if err != nil {
		s.logger().Error("create deployment failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create deployment")
		return
	}

	s.enqueueQueued(w, r, ctx, d)
}

func (s *Server) enqueueQueued(w http.ResponseWriter, r *http.Request, ctx context.Context, d store.Deployment) {
	jobID, err := queue.NewID()
	if err != nil {
		s.logger().Error("job id failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to enqueue deployment")
		return
	}

	payload, err := json.Marshal(struct {
		DeploymentID string `json:"deployment_id"`
	}{DeploymentID: d.ID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue deployment")
		return
	}

	job := queue.Job{
		ID:      jobID,
		Type:    queue.TypeDeploy,
		Payload: payload,
	}

	if err := s.Jobs.Enqueue(ctx, job); err != nil {
		s.logger().Error("enqueue deploy failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, "failed to enqueue deployment")
		return
	}

	w.Header().Set("Location", "/deployments/"+d.ID)
	action := "deployment.enqueue"
	if d.RollbackOf != "" {
		action = "deployment.rollback"
	}
	s.audit(r, "", action, "deployment", d.ID, map[string]string{"application_id": d.ApplicationID})
	writeJSON(w, http.StatusAccepted, createDeploymentResponse{
		deploymentResponse: deploymentResponseFrom(d),
		JobID:              jobID,
		Queued:             "queued",
	})
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
// image name or a command — we copy image_name from that row. Same
// one-LIVE rules as Deploy: stop first if a container is still running.
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
		if existing.Status.InProgress() {
			writeError(w, http.StatusConflict, "deployment already in progress")
			return
		}
	}
	for _, existing := range list {
		if existing.Status != store.StatusLive {
			continue
		}
		running := true
		if s.Runtime != nil {
			ok, runErr := s.Runtime.Running(ctx, runtime.ContainerName(existing.ID))
			if runErr != nil {
				s.logger().Error("inspect live container failed", "err", runErr)
				writeError(w, http.StatusInternalServerError, "failed to roll back")
				return
			}
			running = ok
		}
		if running {
			writeError(w, http.StatusConflict, "application is already live; stop it before rolling back")
			return
		}
		s.haltLive(ctx, existing, "container is no longer running")
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
