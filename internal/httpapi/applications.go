package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	ID                   string    `json:"id"`
	ProjectID            string    `json:"project_id"`
	Name                 string    `json:"name"`
	RepositoryURL        string    `json:"repository_url"`
	RootDirectory        string    `json:"root_directory"`
	HealthPath           string    `json:"health_path"`
	LocalHost            string    `json:"local_host"`
	BuildPackage         string    `json:"build_package,omitempty"`
	MemoryMB             int       `json:"memory_mb"`
	CPUMillis            int       `json:"cpu_millis"`
	PidsLimit            int       `json:"pids_limit"`
	HealthTimeoutSeconds int       `json:"health_timeout_seconds"`
	Exposure             string    `json:"exposure,omitempty"`
	PlatformURL          string    `json:"platform_url,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

func applicationResponseFrom(a store.Application) applicationResponse {
	return applicationResponse{
		ID:                   a.ID,
		ProjectID:            a.ProjectID,
		Name:                 a.Name,
		RepositoryURL:        a.RepositoryURL,
		RootDirectory:        a.RootDirectory,
		HealthPath:           a.HealthPath,
		LocalHost:            a.LocalHost,
		BuildPackage:         a.BuildPackage,
		MemoryMB:             a.MemoryMB,
		CPUMillis:            a.CPUMillis,
		PidsLimit:            a.PidsLimit,
		HealthTimeoutSeconds: a.HealthTimeoutSeconds,
		Exposure:             a.Exposure,
		CreatedAt:            a.CreatedAt.UTC(),
		UpdatedAt:            a.UpdatedAt.UTC(),
	}
}

func (s *Server) applicationJSON(a store.Application) applicationResponse {
	out := applicationResponseFrom(a)
	slug := a.LocalHost
	if slug == "" {
		slug = store.SuggestedLocalHost(a.Name, a.ID)
	}
	out.PlatformURL = store.PlatformURL(slug, s.AppsDomain)
	return out
}

// syncPlatformHost reserves {slug}.{apps domain} when the operator
// configured a zone. A conflict means another application already owns
// that hostname. Tests whose domain fake has no Ensure method skip this.
func (s *Server) syncPlatformHost(ctx context.Context, app store.Application) error {
	if s == nil || strings.TrimSpace(s.AppsDomain) == "" || s.Domains == nil {
		return nil
	}
	p, ok := s.Domains.(interface {
		EnsurePlatformHost(ctx context.Context, applicationID, slug, appsDomain string) (store.ApplicationDomain, error)
	})
	if !ok {
		return nil
	}
	slug := app.LocalHost
	if slug == "" {
		slug = store.SuggestedLocalHost(app.Name, app.ID)
	}
	if host, err := store.PlatformHostname(slug, s.AppsDomain); err == nil && strings.EqualFold(host, s.ControlHost) {
		return fmt.Errorf("%w: %s is reserved for the control plane", store.ErrConflict, host)
	}
	_, err := p.EnsurePlatformHost(ctx, app.ID, slug, s.AppsDomain)
	return err
}

type applicationBody struct {
	Name          string `json:"name"`
	RepositoryURL string `json:"repository_url"`
	RootDirectory string `json:"root_directory"`
	HealthPath    string `json:"health_path"`
	LocalHost     string `json:"local_host"`
	// BuildPackage is omitted by the settings form. Nil keeps the
	// stored package so a save does not clear ./cmd/api.
	BuildPackage *string `json:"build_package,omitempty"`
	// Zero keeps the historical container defaults and the 45s probe.
	MemoryMB             int `json:"memory_mb"`
	CPUMillis            int `json:"cpu_millis"`
	PidsLimit            int `json:"pids_limit"`
	HealthTimeoutSeconds int `json:"health_timeout_seconds"`
	// Exposure is omitted by the settings form. Nil keeps the stored
	// public/private flag from forge.yaml.
	Exposure *string `json:"exposure,omitempty"`
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
	if req.BuildPackage != nil {
		pkg, err := store.ValidateBuildPackage(*req.BuildPackage)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		in.BuildPackage = pkg
	}
	if err := store.ValidateResources(req.MemoryMB, req.CPUMillis, req.PidsLimit, req.HealthTimeoutSeconds); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.MemoryMB = req.MemoryMB
	in.CPUMillis = req.CPUMillis
	in.PidsLimit = req.PidsLimit
	in.HealthTimeoutSeconds = req.HealthTimeoutSeconds
	if req.Exposure != nil {
		if err := store.ValidateExposure(*req.Exposure); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		in.Exposure = *req.Exposure
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
	if err := s.syncPlatformHost(ctx, app); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		s.logger().Error("reserve platform hostname failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to reserve platform hostname")
		return
	}
	writeJSON(w, http.StatusCreated, s.applicationJSON(app))
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
		out = append(out, s.applicationJSON(a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getApplication(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.applicationJSON(app))
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
	in.BuildPackage = app.BuildPackage
	if req.BuildPackage != nil {
		pkg, err := store.ValidateBuildPackage(*req.BuildPackage)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		in.BuildPackage = pkg
	}
	if err := store.ValidateResources(req.MemoryMB, req.CPUMillis, req.PidsLimit, req.HealthTimeoutSeconds); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.MemoryMB = req.MemoryMB
	in.CPUMillis = req.CPUMillis
	in.PidsLimit = req.PidsLimit
	in.HealthTimeoutSeconds = req.HealthTimeoutSeconds
	in.Exposure = app.Exposure
	if req.Exposure != nil {
		if err := store.ValidateExposure(*req.Exposure); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		in.Exposure = *req.Exposure
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
	if err := s.syncPlatformHost(ctx, app); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		s.logger().Error("reserve platform hostname failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to reserve platform hostname")
		return
	}
	s.audit(r, "", "application.update", "application", app.ID, map[string]string{"name": app.Name})
	writeJSON(w, http.StatusOK, s.applicationJSON(app))
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
	if liveRows, err := s.liveDeployments(ctx, app.ID); err != nil {
		s.logger().Error("list deployments before delete failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to delete application")
		return
	} else {
		for _, live := range liveRows {
			s.haltLive(ctx, live, "stopped by operator")
		}
	}
	if s.Deployments != nil {
		list, err := s.Deployments.ListDeploymentsByApplication(ctx, app.ID)
		if err == nil {
			for _, d := range list {
				if d.Status.InProgress() {
					s.abandonInProgress(ctx, d, "stopped by operator")
				}
			}
		}
	}

	s.removeApplicationVolumes(ctx, app.ID)
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
	env, ok := readEnvironment(r, w, false)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	s.enqueueDeployment(w, r, ctx, app.ID, env)
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

	env, err := parseRequestEnvironment(r, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	live, found, err := s.liveDeployment(ctx, app.ID, env)
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
		"environment":    envOf(live),
		"tail":           tail,
		"text":           text,
	})
}

func (s *Server) getApplicationHealth(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	env, err := parseRequestEnvironment(r, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	live, found, err := s.liveDeployment(ctx, app.ID, env)
	if err != nil {
		s.logger().Error("health: list deployments failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to check health")
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, map[string]any{
			"application_id": app.ID,
			"environment":    env,
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
		"environment":    envOf(live),
		"status":         status,
		"public_url":     live.PublicURL,
	})
}

func (s *Server) stopApplication(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	env, ok := readEnvironment(r, w, true)
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
	// the next Deploy. Scope to one environment so stopping preview
	// cannot take production offline.
	var last store.Deployment
	n := 0
	for _, d := range list {
		if !store.SameEnvironment(d.Environment, env) {
			continue
		}
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
	s.audit(r, "", "application.stop", "application", app.ID, map[string]string{"deployment_id": last.ID, "environment": env})
	writeJSON(w, http.StatusOK, deploymentResponseFrom(last))
}

func (s *Server) restartApplication(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	if s.Apps == nil || s.Deployments == nil || s.Jobs == nil {
		writeError(w, http.StatusInternalServerError, "deployments are not configured")
		return
	}
	// Restart is a rolling deploy of one environment. The LIVE
	// container stays up until the new one is healthy. In-progress
	// rows are abandoned so a stuck build cannot block the next job.
	env, ok := readEnvironment(r, w, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	list, err := s.Deployments.ListDeploymentsByApplication(ctx, app.ID)
	if err != nil {
		s.logger().Error("restart: list deployments failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to restart application")
		return
	}
	found := false
	for _, d := range list {
		if !store.SameEnvironment(d.Environment, env) {
			continue
		}
		if d.Status == store.StatusLive {
			found = true
		}
		if d.Status.InProgress() {
			s.abandonInProgress(ctx, d, "restarted by operator")
			found = true
		}
	}
	if !found {
		writeError(w, http.StatusConflict, "nothing to restart")
		return
	}
	s.audit(r, "", "application.restart", "application", app.ID, map[string]string{"environment": env})
	s.enqueueDeployment(w, r, ctx, app.ID, env)
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

func (s *Server) liveDeployment(ctx context.Context, applicationID, environment string) (store.Deployment, bool, error) {
	if s.Deployments == nil {
		return store.Deployment{}, false, nil
	}
	list, err := s.Deployments.ListDeploymentsByApplication(ctx, applicationID)
	if err != nil {
		return store.Deployment{}, false, err
	}
	for _, d := range list {
		if d.Status == store.StatusLive && store.SameEnvironment(d.Environment, environment) {
			return d, true, nil
		}
	}
	return store.Deployment{}, false, nil
}

func (s *Server) liveDeployments(ctx context.Context, applicationID string) ([]store.Deployment, error) {
	if s.Deployments == nil {
		return nil, nil
	}
	list, err := s.Deployments.ListDeploymentsByApplication(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	out := make([]store.Deployment, 0)
	for _, d := range list {
		if d.Status == store.StatusLive {
			out = append(out, d)
		}
	}
	return out, nil
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
