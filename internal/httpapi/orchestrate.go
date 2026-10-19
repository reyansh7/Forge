package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/reyansh7/Forge/internal/detect"
	"github.com/reyansh7/Forge/internal/store"
)

// ProjectDeployStore is the HTTP → project_deployments boundary.
//
// The saga row is not a transaction around Docker or Caddy. It records
// the plan and which existing deployment jobs were queued. Child
// deployment rows remain the runtime source of truth.
type ProjectDeployStore interface {
	CreateProjectDeployment(ctx context.Context, projectID, note string, plan []byte) (store.ProjectDeployment, error)
	AddProjectDeploymentService(ctx context.Context, in store.ProjectDeploymentService) (store.ProjectDeploymentService, error)
	SetProjectDeploymentStatus(ctx context.Context, id, status, message string) error
	GetProjectDeployment(ctx context.Context, id string) (store.ProjectDeployment, []store.ProjectDeploymentService, error)
	ListProjectDeployments(ctx context.Context, projectID string) ([]store.ProjectDeployment, error)
}

type deployPlanSnapshot struct {
	Services []detect.Service       `json:"services"`
	Links    []detect.SuggestedLink `json:"links"`
	Note     string                 `json:"note,omitempty"`
}

type projectDeployServiceResponse struct {
	Name          string   `json:"name"`
	ApplicationID string   `json:"application_id"`
	DeploymentID  string   `json:"deployment_id,omitempty"`
	Root          string   `json:"root"`
	Package       string   `json:"package,omitempty"`
	Port          int      `json:"port,omitempty"`
	Status        string   `json:"status"`
	Error         string   `json:"error,omitempty"`
	PublicURL     string   `json:"public_url,omitempty"`
	InternalURL   string   `json:"internal_url,omitempty"`
	DependsOn     []string `json:"depends_on,omitempty"`
	GeneratedEnv  []string `json:"generated_env,omitempty"`
}

type projectDeployResponse struct {
	ID           string                         `json:"id"`
	ProjectID    string                         `json:"project_id"`
	Status       string                         `json:"status"`
	ErrorMessage string                         `json:"error_message,omitempty"`
	Note         string                         `json:"note,omitempty"`
	Attention    string                         `json:"attention,omitempty"`
	Services     []projectDeployServiceResponse `json:"services"`
	Links        []detect.SuggestedLink         `json:"links"`
	CreatedAt    time.Time                      `json:"created_at"`
}

// rollupStatus is the project outcome. "live" is allowed only when
// every required service is live. A mix of live and failed is failed,
// not a successful deploy. In-progress work stays "deploying" so a
// poll does not declare failure while a later job can still be stopped.
func rollupStatus(states []string) string {
	if len(states) == 0 {
		return "failed"
	}
	inProgress := false
	failed := false
	for _, st := range states {
		switch st {
		case string(store.StatusLive):
		case string(store.StatusFailed), string(store.StatusStopped), "":
			failed = true
		default:
			inProgress = true
		}
	}
	if inProgress {
		return "deploying"
	}
	if failed {
		return "failed"
	}
	return "live"
}

func (s *Server) deployProject(w http.ResponseWriter, r *http.Request) {
	if s.Apps == nil || s.Deployments == nil || s.Jobs == nil || s.ProjectDeploys == nil {
		writeError(w, http.StatusInternalServerError, "project deploy is not configured")
		return
	}
	projectID, err := store.ParseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	var body struct {
		Services []string `json:"services"`
	}
	if !decodeOptionalJSON(r, w, &body) {
		return
	}
	project, ok := s.requireProject(w, r, projectID)
	if !ok {
		return
	}
	actor, ok := ActorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	found, err := s.discoverProjectServices(ctx, actor.UserID, project)
	if err != nil {
		s.logger().Error("project discover failed", "err", err)
		writeError(w, http.StatusBadGateway, "could not read the repository from GitHub")
		return
	}
	selected := selectServices(found, body.Services)
	if len(selected) == 0 {
		writeError(w, http.StatusBadRequest, "no deployable services in this repository")
		return
	}
	if len(selected) > s.nodeInflightQuota() {
		writeError(w, http.StatusConflict, "this node will not run that many deployments at once")
		return
	}
	inFlight, err := s.Deployments.CountInProgressByOwner(ctx, actor.UserID)
	if err != nil {
		s.logger().Error("count in-progress deploys failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create deployment")
		return
	}
	if inFlight > 0 {
		writeError(w, http.StatusConflict, "a deployment is already in progress")
		return
	}

	view, status, msg := s.enqueueProjectServices(ctx, r, project, actor, selected, store.CommitMeta{})
	if status != http.StatusAccepted {
		writeError(w, status, msg)
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}

// enqueueProjectServices queues one existing deployment per service.
//
// The commit is the same on every service. Generated link keys overwrite
// a user value of the same name so a pasted URL cannot drift. Other
// user variables are left alone. A dependency cycle is a 400: Forge
// does not pick an order.
func (s *Server) enqueueProjectServices(ctx context.Context, r *http.Request, project store.Project, actor Actor, selected []detect.Service, commit store.CommitMeta) (projectDeployResponse, int, string) {
	apps, err := s.Apps.ListApplicationsByProject(ctx, project.ID)
	if err != nil {
		s.logger().Error("list apps for project deploy failed", "err", err)
		return projectDeployResponse{}, http.StatusInternalServerError, "failed to create deployment"
	}
	byName := map[string]store.Application{}
	for _, svc := range selected {
		app, err := s.ensureServiceApp(ctx, project, apps, svc)
		if err != nil {
			s.logger().Error("ensure service app failed", "err", err)
			return projectDeployResponse{}, http.StatusInternalServerError, "failed to prepare " + svc.Name
		}
		byName[svc.Name] = app
		apps = append(apps, app)
		s.persistServiceVolumes(ctx, app.ID, svc)
	}

	links, note, err := detect.ResolveLinks(selected)
	if err != nil {
		return projectDeployResponse{}, http.StatusBadRequest, err.Error()
	}
	s.persistSuggestedLinks(ctx, links, byName)

	ordered, err := detect.OrderServices(selected)
	if err != nil {
		return projectDeployResponse{}, http.StatusBadRequest, err.Error()
	}
	plan, err := json.Marshal(deployPlanSnapshot{Services: ordered, Links: links, Note: note})
	if err != nil {
		return projectDeployResponse{}, http.StatusInternalServerError, "failed to create deployment"
	}
	rollout, err := s.ProjectDeploys.CreateProjectDeployment(ctx, project.ID, note, plan)
	if err != nil {
		s.logger().Error("create project deployment failed", "err", err)
		return projectDeployResponse{}, http.StatusInternalServerError, "failed to create deployment"
	}

	// Queue dependencies first. countQuota is false because the batch
	// was refused above when anything was already in progress, and the
	// node cap bounds how many jobs this one action may create. Each
	// job is still the existing single-service pipeline.
	var queued []store.ProjectDeploymentService
	stopped := ""
	for _, svc := range ordered {
		app := byName[svc.Name]
		row := store.ProjectDeploymentService{
			ProjectDeploymentID: rollout.ID,
			ApplicationID:       app.ID,
			ServiceName:         svc.Name,
			RootDirectory:       svc.Root,
			BuildPackage:        svc.Package,
			Status:              "queued",
		}
		if stopped != "" {
			row.Status = "failed"
			row.ErrorMessage = stopped
			saved, err := s.ProjectDeploys.AddProjectDeploymentService(ctx, row)
			if err != nil {
				s.logger().Error("record skipped service failed", "err", err)
			} else {
				queued = append(queued, saved)
			}
			continue
		}
		d, prep := s.createQueuedDeployment(ctx, app.ID, store.EnvProduction, actor.UserID, commit, false)
		if prep.message != "" {
			row.Status = "failed"
			row.ErrorMessage = prep.message
			stopped = prep.message
			_ = s.ProjectDeploys.SetProjectDeploymentStatus(ctx, rollout.ID, "failed", prep.message)
		} else {
			out := s.enqueueDeploymentJob(r, ctx, d)
			if out.status != http.StatusAccepted {
				row.Status = "failed"
				row.ErrorMessage = out.message
				stopped = out.message
				_ = s.ProjectDeploys.SetProjectDeploymentStatus(ctx, rollout.ID, "failed", out.message)
				if out.deployment.ID != "" {
					row.DeploymentID = out.deployment.ID
				} else {
					row.DeploymentID = d.ID
				}
			} else {
				row.DeploymentID = out.deployment.ID
			}
		}
		saved, err := s.ProjectDeploys.AddProjectDeploymentService(ctx, row)
		if err != nil {
			s.logger().Error("record project service failed", "err", err)
			return projectDeployResponse{}, http.StatusInternalServerError, "failed to record project deployment"
		}
		queued = append(queued, saved)
	}
	if stopped != "" {
		rollout.Status = "failed"
		if rollout.ErrorMessage == "" {
			rollout.ErrorMessage = stopped
		}
	}

	view := s.projectDeployJSON(ctx, rollout, queued)
	s.audit(r, "", "project.deploy", "project", project.ID, map[string]string{"project_deployment_id": rollout.ID, "status": view.Status})
	return view, http.StatusAccepted, ""
}

func selectServices(found []detect.Service, names []string) []detect.Service {
	want := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n != "" {
			want[n] = true
		}
	}
	var out []detect.Service
	seen := map[string]bool{}
	for _, svc := range found {
		if !svc.Deployable || svc.Name == "" || seen[svc.Name] {
			continue
		}
		if len(want) > 0 && !want[svc.Name] {
			continue
		}
		seen[svc.Name] = true
		out = append(out, svc)
	}
	return out
}

func (s *Server) ensureServiceApp(ctx context.Context, project store.Project, apps []store.Application, svc detect.Service) (store.Application, error) {
	for _, app := range apps {
		if app.Name != svc.Name {
			continue
		}
		health := app.HealthPath
		if svc.Health != "" {
			health = svc.Health
		}
		local := app.LocalHost
		exposure := exposureFromService(svc)
		if exposure == "private" {
			local = ""
		}
		if exposure == "public" && strings.TrimSpace(local) == "" {
			local = store.SuggestedLocalHost(svc.Name, app.ID)
		}
		if app.RootDirectory == svc.Root && app.BuildPackage == svc.Package && app.RepositoryURL == project.RepositoryURL && app.HealthPath == health && app.Exposure == exposure && app.LocalHost == local {
			return app, nil
		}
		return s.Apps.UpdateApplication(ctx, app.ID, store.ApplicationInput{
			Name:                 app.Name,
			RepositoryURL:        project.RepositoryURL,
			RootDirectory:        svc.Root,
			HealthPath:           health,
			LocalHost:            local,
			BuildPackage:         svc.Package,
			MemoryMB:             app.MemoryMB,
			CPUMillis:            app.CPUMillis,
			PidsLimit:            app.PidsLimit,
			HealthTimeoutSeconds: app.HealthTimeoutSeconds,
			Exposure:             exposure,
		})
	}
	health := "/"
	if svc.Health != "" {
		health = svc.Health
	}
	exposure := exposureFromService(svc)
	return s.Apps.CreateApplication(ctx, project.ID, store.ApplicationInput{
		Name:          svc.Name,
		RepositoryURL: project.RepositoryURL,
		RootDirectory: svc.Root,
		HealthPath:    health,
		BuildPackage:  svc.Package,
		Exposure:      exposure,
	})
}

func serviceRoutable(app store.Application) bool {
	switch strings.TrimSpace(app.Exposure) {
	case "public":
		return true
	case "private":
		return false
	}
	if strings.TrimSpace(app.LocalHost) != "" {
		return true
	}
	switch detect.ServiceRole(detect.Service{Name: app.Name}) {
	case "backend", "worker", "ml":
		return false
	default:
		return true
	}
}

func exposureFromService(svc detect.Service) string {
	if svc.Public == nil {
		return ""
	}
	if *svc.Public {
		return "public"
	}
	return "private"
}

func (s *Server) persistServiceVolumes(ctx context.Context, applicationID string, svc detect.Service) {
	if s.Volumes == nil {
		return
	}
	for _, vol := range svc.Volumes {
		if _, err := s.Volumes.CreateApplicationVolume(ctx, applicationID, vol.Name, vol.Path); err != nil && !errors.Is(err, store.ErrConflict) {
			s.logger().Error("persist service volume failed", "err", err, "service", svc.Name)
		}
	}
}

func (s *Server) persistSuggestedLinks(ctx context.Context, links []detect.SuggestedLink, byName map[string]store.Application) {
	if s.Links == nil {
		return
	}
	for _, link := range links {
		src, ok := byName[link.Source]
		if !ok {
			continue
		}
		tgt, ok := byName[link.Target]
		if !ok {
			continue
		}
		if _, err := s.Links.CreateApplicationLink(ctx, src.ID, tgt.ID, link.EnvKey); err != nil && !errors.Is(err, store.ErrConflict) {
			s.logger().Error("suggest service link failed", "err", err, "env_key", link.EnvKey)
		}
	}
}

func (s *Server) listProjectDeploys(w http.ResponseWriter, r *http.Request) {
	if s.ProjectDeploys == nil {
		writeError(w, http.StatusInternalServerError, "project deploy is not configured")
		return
	}
	projectID, err := store.ParseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	if _, ok := s.requireProject(w, r, projectID); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	list, err := s.ProjectDeploys.ListProjectDeployments(ctx, projectID)
	if err != nil {
		s.logger().Error("list project deployments failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list project deployments")
		return
	}
	out := make([]projectDeployResponse, 0, len(list))
	for _, item := range list {
		_, svcs, err := s.ProjectDeploys.GetProjectDeployment(ctx, item.ID)
		if err != nil {
			s.logger().Error("get project deployment failed", "err", err)
			writeError(w, http.StatusInternalServerError, "failed to list project deployments")
			return
		}
		out = append(out, s.projectDeployJSON(ctx, item, svcs))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getProjectDeploy(w http.ResponseWriter, r *http.Request) {
	view, ok := s.loadProjectDeploy(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) cancelProjectDeploy(w http.ResponseWriter, r *http.Request) {
	if s.ProjectDeploys == nil || s.Deployments == nil {
		writeError(w, http.StatusInternalServerError, "project deploy is not configured")
		return
	}
	projectID, err := store.ParseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	deployID, err := store.ParseUUID(r.PathValue("deployId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project deployment id")
		return
	}
	if _, ok := s.requireProject(w, r, projectID); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rollout, svcs, err := s.ProjectDeploys.GetProjectDeployment(ctx, deployID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "project deployment not found")
		return
	}
	if err != nil {
		s.logger().Error("get project deployment failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to cancel project deployment")
		return
	}
	if rollout.ProjectID != projectID {
		writeError(w, http.StatusNotFound, "project deployment not found")
		return
	}
	if err := s.ProjectDeploys.SetProjectDeploymentStatus(ctx, rollout.ID, "failed", "cancelled by operator"); err != nil {
		s.logger().Error("cancel project deployment failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to cancel project deployment")
		return
	}
	// Queued rows have not started. Marking them failed makes the
	// worker ignore the job. A job already building still reaches the
	// promotion gate, which refuses to switch traffic.
	for _, svc := range svcs {
		if svc.DeploymentID == "" {
			continue
		}
		d, err := s.Deployments.GetDeployment(ctx, svc.DeploymentID)
		if err != nil || d.Status != store.StatusQueued {
			continue
		}
		d.Status = store.StatusFailed
		d.FailedStage = string(store.StatusQueued)
		d.ErrorMessage = "cancelled by operator"
		_ = s.Deployments.UpdateDeployment(ctx, d)
	}
	rollout.Status = "failed"
	rollout.ErrorMessage = "cancelled by operator"
	s.audit(r, "", "project.deploy.cancel", "project", projectID, map[string]string{"project_deployment_id": rollout.ID})
	writeJSON(w, http.StatusOK, s.projectDeployJSON(ctx, rollout, svcs))
}

func (s *Server) loadProjectDeploy(w http.ResponseWriter, r *http.Request) (projectDeployResponse, bool) {
	if s.ProjectDeploys == nil {
		writeError(w, http.StatusInternalServerError, "project deploy is not configured")
		return projectDeployResponse{}, false
	}
	projectID, err := store.ParseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return projectDeployResponse{}, false
	}
	deployID, err := store.ParseUUID(r.PathValue("deployId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project deployment id")
		return projectDeployResponse{}, false
	}
	if _, ok := s.requireProject(w, r, projectID); !ok {
		return projectDeployResponse{}, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rollout, svcs, err := s.ProjectDeploys.GetProjectDeployment(ctx, deployID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "project deployment not found")
		return projectDeployResponse{}, false
	}
	if err != nil {
		s.logger().Error("get project deployment failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to read project deployment")
		return projectDeployResponse{}, false
	}
	if rollout.ProjectID != projectID {
		writeError(w, http.StatusNotFound, "project deployment not found")
		return projectDeployResponse{}, false
	}
	return s.projectDeployJSON(ctx, rollout, svcs), true
}

func (s *Server) projectDeployJSON(ctx context.Context, rollout store.ProjectDeployment, svcs []store.ProjectDeploymentService) projectDeployResponse {
	var snap deployPlanSnapshot
	_ = json.Unmarshal(rollout.Plan, &snap)
	ports := map[string]int{}
	deps := map[string][]string{}
	for _, svc := range snap.Services {
		ports[svc.Name] = svc.Port
		deps[svc.Name] = svc.DependsOn
	}
	envKeys := map[string][]string{}
	for _, link := range snap.Links {
		envKeys[link.Source] = append(envKeys[link.Source], link.EnvKey)
	}
	states := make([]string, 0, len(svcs))
	out := projectDeployResponse{
		ID:           rollout.ID,
		ProjectID:    rollout.ProjectID,
		ErrorMessage: rollout.ErrorMessage,
		Note:         firstNonEmpty(rollout.Note, snap.Note),
		Links:        snap.Links,
		Services:     make([]projectDeployServiceResponse, 0, len(svcs)),
		CreatedAt:    rollout.CreatedAt.UTC(),
	}
	if out.Links == nil {
		out.Links = []detect.SuggestedLink{}
	}
	anyLive := false
	for _, svc := range svcs {
		item := projectDeployServiceResponse{
			Name:          svc.ServiceName,
			ApplicationID: svc.ApplicationID,
			DeploymentID:  svc.DeploymentID,
			Root:          svc.RootDirectory,
			Package:       svc.BuildPackage,
			Port:          ports[svc.ServiceName],
			Status:        svc.Status,
			Error:         svc.ErrorMessage,
			DependsOn:     deps[svc.ServiceName],
			GeneratedEnv:  envKeys[svc.ServiceName],
		}
		if s.Apps != nil {
			if app, err := s.Apps.GetApplication(ctx, svc.ApplicationID); err == nil {
				listen := ports[svc.ServiceName]
				if listen <= 0 {
					listen = 8080
				}
				item.InternalURL = store.InternalServiceURL(store.BridgeAlias(app.Name, app.LocalHost, app.ID), listen)
				if serviceRoutable(app) {
					item.PublicURL = store.ServiceHostURL(s.proxyBase(), app)
				}
			}
		}
		if svc.DeploymentID != "" && s.Deployments != nil {
			if d, err := s.Deployments.GetDeployment(ctx, svc.DeploymentID); err == nil {
				item.Status = string(d.Status)
				if d.ErrorMessage != "" {
					item.Error = d.ErrorMessage
				}
			}
		}
		if svc.Compensation == "failed" && svc.CompensationError != "" {
			if item.Error != "" {
				item.Error += " compensation: " + svc.CompensationError
			} else {
				item.Error = "compensation: " + svc.CompensationError
			}
		}
		if item.Status == string(store.StatusLive) {
			anyLive = true
		}
		states = append(states, item.Status)
		out.Services = append(out.Services, item)
	}
	out.Status = rollupStatus(states)
	// An explicit failure (cancel, or a job that could not be queued)
	// stays failed even if a sibling row is still queued. Queued is
	// not success, and it must not clear the saga failure.
	if rollout.Status == "failed" {
		out.Status = "failed"
	}
	if out.Status == "failed" {
		out.Attention = "Required services are not all healthy. This project deployment did not succeed. A service that failed its checks still serves its previous healthy release."
		if anyLive {
			out.Attention += " A service that already passed health stays on the new release until you roll that application back. Forge does not reverse database migrations."
		}
	}
	if rollout.Status == "deploying" && out.Status != "deploying" && s.ProjectDeploys != nil {
		msg := rollout.ErrorMessage
		if out.Status == "failed" && msg == "" {
			msg = "a required service is not healthy"
		}
		_ = s.ProjectDeploys.SetProjectDeploymentStatus(ctx, rollout.ID, out.Status, msg)
	}
	if out.ErrorMessage == "" && out.Status == "failed" {
		out.ErrorMessage = "a required service is not healthy"
	}
	return out
}

// rollbackProjectDeploy queues the existing per-application rollback
// for every service in one project release. It does not build. Each
// child copies the image from the selected release. A service without
// an image refuses the whole request before any job is queued.
func (s *Server) rollbackProjectDeploy(w http.ResponseWriter, r *http.Request) {
	if s.ProjectDeploys == nil || s.Deployments == nil || s.Jobs == nil {
		writeError(w, http.StatusInternalServerError, "project deploy is not configured")
		return
	}
	projectID, err := store.ParseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	deployID, err := store.ParseUUID(r.PathValue("deployId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project deployment id")
		return
	}
	project, ok := s.requireProject(w, r, projectID)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	rollout, svcs, err := s.ProjectDeploys.GetProjectDeployment(ctx, deployID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && rollout.ProjectID != projectID) {
		writeError(w, http.StatusNotFound, "project deployment not found")
		return
	}
	if err != nil {
		s.logger().Error("get project deployment failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to roll back")
		return
	}
	if len(svcs) == 0 {
		writeError(w, http.StatusConflict, "that project deployment has no services to roll back")
		return
	}
	sources := make([]store.Deployment, 0, len(svcs))
	for _, svc := range svcs {
		if svc.DeploymentID == "" {
			writeError(w, http.StatusConflict, svc.ServiceName+" has no image to roll back to")
			return
		}
		d, err := s.Deployments.GetDeployment(ctx, svc.DeploymentID)
		if err != nil || strings.TrimSpace(d.ImageName) == "" {
			writeError(w, http.StatusConflict, svc.ServiceName+" has no image to roll back to")
			return
		}
		sources = append(sources, d)
	}
	inFlight, err := s.Deployments.CountInProgressByOwner(ctx, project.OwnerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to roll back")
		return
	}
	if inFlight > 0 {
		writeError(w, http.StatusConflict, "a deployment is already in progress")
		return
	}
	next, err := s.ProjectDeploys.CreateProjectDeployment(ctx, project.ID, "rollback of "+rollout.ID, rollout.Plan)
	if err != nil {
		s.logger().Error("create rollback project deployment failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to roll back")
		return
	}
	var queued []store.ProjectDeploymentService
	stopped := ""
	for i, svc := range svcs {
		row := store.ProjectDeploymentService{
			ProjectDeploymentID: next.ID,
			ApplicationID:       svc.ApplicationID,
			ServiceName:         svc.ServiceName,
			RootDirectory:       svc.RootDirectory,
			BuildPackage:        svc.BuildPackage,
			Status:              "queued",
		}
		if stopped != "" {
			row.Status = "failed"
			row.ErrorMessage = stopped
			saved, err := s.ProjectDeploys.AddProjectDeploymentService(ctx, row)
			if err == nil {
				queued = append(queued, saved)
			}
			continue
		}
		d, err := s.Deployments.CreateRollbackDeployment(ctx, sources[i].ID)
		if err != nil {
			row.Status = "failed"
			row.ErrorMessage = "failed to roll back " + svc.ServiceName
			stopped = row.ErrorMessage
			_ = s.ProjectDeploys.SetProjectDeploymentStatus(ctx, next.ID, "failed", stopped)
		} else {
			out := s.enqueueDeploymentJob(r, ctx, d)
			row.DeploymentID = d.ID
			if out.deployment.ID != "" {
				row.DeploymentID = out.deployment.ID
			}
			if out.status != http.StatusAccepted {
				row.Status = "failed"
				row.ErrorMessage = out.message
				stopped = out.message
				_ = s.ProjectDeploys.SetProjectDeploymentStatus(ctx, next.ID, "failed", out.message)
			}
		}
		saved, err := s.ProjectDeploys.AddProjectDeploymentService(ctx, row)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record project deployment")
			return
		}
		queued = append(queued, saved)
	}
	if stopped != "" {
		next.Status = "failed"
		next.ErrorMessage = stopped
	}
	view := s.projectDeployJSON(ctx, next, queued)
	s.audit(r, "", "project.rollback", "project", project.ID, map[string]string{"project_deployment_id": next.ID, "source": rollout.ID})
	writeJSON(w, http.StatusAccepted, view)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
