package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/reyansh7/Forge/internal/runtime"
	"github.com/reyansh7/Forge/internal/store"
)

// ApplicationStore is the HTTP → persistence boundary for applications.
//
// A project is a folder. An application owns the git remote, env vars,
// settings, and deployments. Tests inject an in-memory fake; cmd/api
// injects *store.Postgres.
type ApplicationStore interface {
	CreateApplication(ctx context.Context, projectID string, in store.ApplicationInput) (store.Application, error)
	GetApplication(ctx context.Context, id string) (store.Application, error)
	ListApplicationsByProject(ctx context.Context, projectID string) ([]store.Application, error)
	UpdateApplication(ctx context.Context, id string, in store.ApplicationInput) (store.Application, error)
	DeleteApplication(ctx context.Context, id string) error
	ListEnvVars(ctx context.Context, applicationID string) ([]store.EnvVar, error)
	PutEnvVar(ctx context.Context, applicationID, key, value string) error
	ReplaceEnvVars(ctx context.Context, applicationID string, vars []store.EnvVar) error
	DeleteEnvVar(ctx context.Context, applicationID, key string) error
}

// AppRuntime is the HTTP → Docker boundary for logs, stop, and live health.
//
// Build and run stay on the worker. The API may read `docker logs`,
// `docker stop`, and `docker inspect` because those are operator
// control-plane actions, not untrusted source execution. Tests inject a stub.
type AppRuntime interface {
	Logs(ctx context.Context, containerName string, tail int) (string, error)
	FollowLogs(ctx context.Context, containerName string, tail int, write func(line string) error) error
	Stop(ctx context.Context, containerName string) error
	Running(ctx context.Context, containerName string) (bool, error)
}

// RouteApplier updates Caddy after a stop so a dead port is not routed.
type RouteApplier interface {
	Apply(ctx context.Context, live []store.Deployment) error
}

type applicationResponse struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	Name          string    `json:"name"`
	RepositoryURL string    `json:"repository_url"`
	RootDirectory string    `json:"root_directory"`
	HealthPath    string    `json:"health_path"`
	LocalHost     string    `json:"local_host"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func applicationResponseFrom(a store.Application) applicationResponse {
	return applicationResponse{
		ID:            a.ID,
		ProjectID:     a.ProjectID,
		Name:          a.Name,
		RepositoryURL: a.RepositoryURL,
		RootDirectory: a.RootDirectory,
		HealthPath:    a.HealthPath,
		LocalHost:     a.LocalHost,
		CreatedAt:     a.CreatedAt.UTC(),
		UpdatedAt:     a.UpdatedAt.UTC(),
	}
}

type applicationBody struct {
	Name          string `json:"name"`
	RepositoryURL string `json:"repository_url"`
	RootDirectory string `json:"root_directory"`
	HealthPath    string `json:"health_path"`
	LocalHost     string `json:"local_host"`
}

type envVarResponse struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type envPutBody struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (s *Server) createApplication(w http.ResponseWriter, r *http.Request) {
	if s.Projects == nil || s.Apps == nil {
		writeError(w, http.StatusInternalServerError, "applications are not configured")
		return
	}
	projectID, err := store.ParseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	var req applicationBody
	if !decodeJSON(r, w, &req) {
		return
	}
	in, err := store.ValidateApplication(req.Name, req.RepositoryURL, req.RootDirectory, req.HealthPath, req.LocalHost)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if _, ok := s.requireProject(w, r, projectID); !ok {
		return
	}

	app, err := s.Apps.CreateApplication(ctx, projectID, in)
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		s.logger().Error("create application failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create application")
		return
	}
	w.Header().Set("Location", "/applications/"+app.ID)
	writeJSON(w, http.StatusCreated, applicationResponseFrom(app))
	s.audit(r, "", "application.create", "application", app.ID, map[string]string{"name": app.Name, "project_id": projectID})
}

func (s *Server) listApplications(w http.ResponseWriter, r *http.Request) {
	if s.Apps == nil {
		writeError(w, http.StatusInternalServerError, "applications are not configured")
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
	list, err := s.Apps.ListApplicationsByProject(ctx, projectID)
	if err != nil {
		s.logger().Error("list applications failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list applications")
		return
	}
	out := make([]applicationResponse, 0, len(list))
	for _, a := range list {
		out = append(out, applicationResponseFrom(a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getApplication(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, applicationResponseFrom(app))
}

func (s *Server) updateApplication(w http.ResponseWriter, r *http.Request) {
	if s.Apps == nil {
		writeError(w, http.StatusInternalServerError, "applications are not configured")
		return
	}
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	id := app.ID
	var req applicationBody
	if !decodeJSON(r, w, &req) {
		return
	}
	in, err := store.ValidateApplication(req.Name, req.RepositoryURL, req.RootDirectory, req.HealthPath, req.LocalHost)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	app, err = s.Apps.UpdateApplication(ctx, id, in)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		s.logger().Error("update application failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to update application")
		return
	}
	s.audit(r, "", "application.update", "application", app.ID, map[string]string{"name": app.Name})
	writeJSON(w, http.StatusOK, applicationResponseFrom(app))
}

func (s *Server) deleteApplication(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	// Stop first so ON DELETE CASCADE cannot leave a running container
	// whose deployment row is gone.
	if live, found, err := s.liveDeployment(ctx, app.ID); err != nil {
		s.logger().Error("list deployments before delete failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to delete application")
		return
	} else if found {
		s.haltLive(ctx, live, "stopped by operator")
	}

	if err := s.Apps.DeleteApplication(ctx, app.ID); err != nil {
		s.logger().Error("delete application failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to delete application")
		return
	}
	s.audit(r, "", "application.delete", "application", app.ID, map[string]string{"project_id": app.ProjectID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createApplicationDeployment(w http.ResponseWriter, r *http.Request) {
	if s.Apps == nil || s.Deployments == nil || s.Jobs == nil {
		writeError(w, http.StatusInternalServerError, "deployments are not configured")
		return
	}
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	var ignore struct{}
	if !decodeJSON(r, w, &ignore) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	s.enqueueDeployment(w, r, ctx, app.ID)
}

func (s *Server) listApplicationDeployments(w http.ResponseWriter, r *http.Request) {
	if s.Deployments == nil {
		writeError(w, http.StatusInternalServerError, "deployments are not configured")
		return
	}
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	list, err := s.Deployments.ListDeploymentsByApplication(ctx, app.ID)
	if err != nil {
		s.logger().Error("list application deployments failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list deployments")
		return
	}
	out := make([]deploymentResponse, 0, len(list))
	for _, d := range list {
		out = append(out, deploymentResponseFrom(d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getApplicationLogs(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	if s.Runtime == nil {
		writeError(w, http.StatusInternalServerError, "runtime is not configured")
		return
	}
	tail := 100
	if raw := r.URL.Query().Get("tail"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "tail must be a positive integer")
			return
		}
		tail = n
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	live, found, err := s.liveDeployment(ctx, app.ID)
	if err != nil {
		s.logger().Error("logs: list deployments failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to read logs")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "no live deployment")
		return
	}
	text, err := s.Runtime.Logs(ctx, runtime.ResolveContainerName(live.ID, live.ContainerName), tail)
	if err != nil {
		s.logger().Error("docker logs failed", "err", err)
		writeError(w, http.StatusBadGateway, "failed to read logs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"application_id": app.ID,
		"deployment_id":  live.ID,
		"tail":           tail,
		"text":           text,
	})
}

func (s *Server) getApplicationHealth(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	live, found, err := s.liveDeployment(ctx, app.ID)
	if err != nil {
		s.logger().Error("health: list deployments failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to check health")
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, map[string]any{
			"application_id": app.ID,
			"status":         "stopped",
		})
		return
	}

	// After go-live the dashboard polls this endpoint about every 1.5s.
	// An HTTP GET against the workload would show up in `docker logs` as
	// GET / from the Docker bridge (172.17.0.1) for as long as the page
	// is open. The worker already proved HTTP liveness once at deploy
	// time via runtime.HealthGETPath. Ongoing status is "is the
	// container still running" via docker inspect — that never hits the
	// app's HTTP server, so operator logs stay the app's own traffic.
	running := true
	if s.Runtime != nil {
		ok, runErr := s.Runtime.Running(ctx, runtime.ResolveContainerName(live.ID, live.ContainerName))
		if runErr != nil {
			s.logger().Error("health: docker inspect failed", "err", runErr)
			writeError(w, http.StatusBadGateway, "failed to check health")
			return
		}
		running = ok
	}
	status := "unhealthy"
	if running {
		status = "healthy"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"application_id": app.ID,
		"deployment_id":  live.ID,
		"status":         status,
		"public_url":     live.PublicURL,
	})
}

func (s *Server) stopApplication(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if s.Deployments == nil {
		writeError(w, http.StatusInternalServerError, "deployments are not configured")
		return
	}
	list, err := s.Deployments.ListDeploymentsByApplication(ctx, app.ID)
	if err != nil {
		s.logger().Error("stop: list deployments failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to stop application")
		return
	}
	// Stop must cancel BUILDING/QUEUED rows, not only LIVE. Killing the
	// worker mid-build used to leave InProgress forever, which blocked
	// the next Deploy.
	var last store.Deployment
	n := 0
	for _, d := range list {
		switch {
		case d.Status == store.StatusLive:
			s.haltLive(ctx, d, "stopped by operator")
			d.Status = store.StatusStopped
			d.ErrorMessage = store.SanitizeErrorMessage("stopped by operator")
			last = d
			n++
		case d.Status.InProgress():
			stage := string(d.Status)
			s.abandonInProgress(ctx, d, "stopped by operator")
			d.Status = store.StatusFailed
			d.FailedStage = stage
			d.ErrorMessage = store.SanitizeErrorMessage("stopped by operator")
			last = d
			n++
		}
	}
	if n == 0 {
		writeError(w, http.StatusConflict, "nothing to stop")
		return
	}
	s.audit(r, "", "application.stop", "application", app.ID, map[string]string{"deployment_id": last.ID})
	writeJSON(w, http.StatusOK, deploymentResponseFrom(last))
}

func (s *Server) listEnv(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	vars, err := s.Apps.ListEnvVars(ctx, app.ID)
	if err != nil {
		s.logger().Error("list env failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list env")
		return
	}
	out := make([]envVarResponse, 0, len(vars))
	for _, ev := range vars {
		out = append(out, envVarResponse{Key: ev.Key, Value: ev.Value})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) putEnv(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	var req envPutBody
	if !decodeJSON(r, w, &req) {
		return
	}
	if err := store.ValidateEnvKey(req.Key); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := store.ValidateEnvValue(req.Value); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.Apps.PutEnvVar(ctx, app.ID, req.Key, req.Value); err != nil {
		// The 32-var cap is a client error. Other failures stay generic
		// so a driver string cannot leak into the body.
		if err.Error() == "at most 32 environment variables per application" {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.logger().Error("put env failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to save env")
		return
	}
	writeJSON(w, http.StatusOK, envVarResponse{Key: req.Key, Value: req.Value})
	s.audit(r, "", "env.put", "application", app.ID, map[string]string{"key": req.Key})
}

func (s *Server) replaceEnv(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var req struct {
		Vars []envPutBody `json:"vars"`
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	vars := make([]store.EnvVar, 0, len(req.Vars))
	for _, ev := range req.Vars {
		vars = append(vars, store.EnvVar{Key: ev.Key, Value: ev.Value})
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.Apps.ReplaceEnvVars(ctx, app.ID, vars); err != nil {
		if err.Error() == "at most 32 environment variables per application" ||
			strings.Contains(err.Error(), "env key") ||
			strings.Contains(err.Error(), "env value") ||
			strings.Contains(err.Error(), "duplicate env") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.logger().Error("replace env failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to save env")
		return
	}
	out := make([]envVarResponse, 0, len(vars))
	for _, ev := range vars {
		out = append(out, envVarResponse{Key: ev.Key, Value: ev.Value})
	}
	s.audit(r, "", "env.replace", "application", app.ID, map[string]string{"count": strconv.Itoa(len(vars))})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteEnv(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	key := r.PathValue("key")
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.Apps.DeleteEnvVar(ctx, app.ID, key); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "env key not found")
		return
	} else if err != nil {
		s.logger().Error("delete env failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to delete env")
		return
	}
	s.audit(r, "", "env.delete", "application", app.ID, map[string]string{"key": key})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) loadApplication(w http.ResponseWriter, r *http.Request) (store.Application, bool) {
	if s.Apps == nil {
		writeError(w, http.StatusInternalServerError, "applications are not configured")
		return store.Application{}, false
	}
	id, err := store.ParseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return store.Application{}, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	app, err := s.Apps.GetApplication(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "application not found")
		return store.Application{}, false
	}
	if err != nil {
		s.logger().Error("get application failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to get application")
		return store.Application{}, false
	}
	// Same 404 as a missing app so a guessed application UUID does not
	// reveal "this id exists but you do not own the project".
	actor, ok := ActorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return store.Application{}, false
	}
	if s.Projects == nil {
		writeError(w, http.StatusInternalServerError, "projects store is not configured")
		return store.Application{}, false
	}
	proj, err := s.Projects.GetProject(ctx, app.ProjectID)
	if err != nil || proj.OwnerID == "" || proj.OwnerID != actor.UserID {
		writeError(w, http.StatusNotFound, "application not found")
		return store.Application{}, false
	}
	return app, true
}

func (s *Server) liveDeployment(ctx context.Context, applicationID string) (store.Deployment, bool, error) {
	if s.Deployments == nil {
		return store.Deployment{}, false, nil
	}
	list, err := s.Deployments.ListDeploymentsByApplication(ctx, applicationID)
	if err != nil {
		return store.Deployment{}, false, err
	}
	for _, d := range list {
		if d.Status == store.StatusLive {
			return d, true, nil
		}
	}
	return store.Deployment{}, false, nil
}

func (s *Server) abandonInProgress(ctx context.Context, d store.Deployment, notice string) {
	if s.Runtime != nil && (d.ContainerName != "" || d.ID != "") {
		_ = s.Runtime.Stop(ctx, runtime.ResolveContainerName(d.ID, d.ContainerName))
	}
	stage := string(d.Status)
	d.FailedStage = stage
	d.Status = store.StatusFailed
	d.ErrorMessage = store.SanitizeErrorMessage(notice)
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.Deployments.UpdateDeployment(persist, d); err != nil {
		s.logger().Error("persist abandoned deployment failed", "err", err)
	}
}

func (s *Server) haltLive(ctx context.Context, live store.Deployment, notice string) {
	if s.Runtime != nil && live.ID != "" {
		_ = s.Runtime.Stop(ctx, runtime.ResolveContainerName(live.ID, live.ContainerName))
	}
	live.Status = store.StatusStopped
	live.FailedStage = ""
	live.ErrorMessage = store.SanitizeErrorMessage(notice)
	// Persist even if docker stop consumed the request deadline.
	// Otherwise the row stays LIVE with no container (unhealthy + 409 on Deploy).
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if s.Deployments != nil {
		if err := s.Deployments.UpdateDeployment(persist, live); err != nil {
			s.logger().Error("persist stopped deployment failed", "err", err)
		}
	}
	if s.Router == nil || s.Deployments == nil {
		return
	}
	remaining, err := s.Deployments.ListLiveDeployments(persist)
	if err != nil {
		s.logger().Error("list live after stop failed", "err", err)
		return
	}
	if err := s.Router.Apply(persist, remaining); err != nil {
		s.logger().Error("caddy apply after stop failed", "err", err)
	}
}
