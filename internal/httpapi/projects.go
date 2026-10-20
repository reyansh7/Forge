package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/reyansh7/Forge/internal/store"
)

// ProjectStore is the HTTP → persistence boundary for increment 0.2.
//
// What: three methods the handlers need. Not the whole of database/sql.
// Why: tests inject memProjects (no Docker). cmd/api injects *store.Postgres.
// Who calls it: createProject / listProjects / getProject / deleteProject.
// What it calls (production): PostgreSQL via store.Postgres.
//
// This is dependency inversion: HTTP depends on an interface, not a
// concrete driver. That is how /health tests used stubPing in 0.1.
//
// Deferred: renaming a project, team sharing (beyond owner_id).
type ProjectStore interface {
	CreateProject(ctx context.Context, ownerID string, in store.ProjectInput) (store.Project, error)
	GetProject(ctx context.Context, id string) (store.Project, error)
	ListProjects(ctx context.Context, ownerID string) ([]store.Project, error)
	CountProjectsByOwner(ctx context.Context, ownerID string) (int, error)
	DeleteProject(ctx context.Context, id, ownerID string) error
}

// createProjectRequest is the JSON the client sends. json tags are the
// public field names (`repository_url`, not RepositoryURL).
type createProjectRequest struct {
	Name          string `json:"name"`
	RepositoryURL string `json:"repository_url"`
}

// projectResponse is the JSON we send. UTC timestamps so clients are not
// surprised by the operator's local timezone.
type projectResponse struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	RepositoryURL string    `json:"repository_url"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func projectResponseFrom(p store.Project) projectResponse {
	return projectResponse{
		ID:            p.ID,
		Name:          p.Name,
		RepositoryURL: p.RepositoryURL,
		CreatedAt:     p.CreatedAt.UTC(),
		UpdatedAt:     p.UpdatedAt.UTC(),
	}
}

// createProject handles POST /projects.
//
// Flow: decode JSON → validate (400) → store insert → 201 + Location.
// 500 bodies are generic; the real driver error goes to slog only.
// There is no client-supplied owner id. The session actor is the owner.
func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	if s.Projects == nil {
		writeError(w, http.StatusInternalServerError, "projects store is not configured")
		return
	}
	actor, ok := ActorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req createProjectRequest
	if !decodeJSON(r, w, &req) {
		return
	}

	in, err := store.ValidateProjectInput(req.Name, req.RepositoryURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if max := s.projectQuota(); max > 0 {
		n, err := s.Projects.CountProjectsByOwner(ctx, actor.UserID)
		if err != nil {
			s.logger().Error("count projects failed", "err", err)
			writeError(w, http.StatusInternalServerError, "failed to create project")
			return
		}
		if n >= max {
			writeError(w, http.StatusConflict, "project quota exceeded")
			return
		}
	}

	p, err := s.Projects.CreateProject(ctx, actor.UserID, in)
	if err != nil {
		s.logger().Error("create project failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create project")
		return
	}

	s.audit(r, actor.UserID, "project.create", "project", p.ID, map[string]string{"name": p.Name})

	// Location is the HTTP convention for "here is the new resource".
	w.Header().Set("Location", "/projects/"+p.ID)
	writeJSON(w, http.StatusCreated, projectResponseFrom(p))
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	if s.Projects == nil {
		writeError(w, http.StatusInternalServerError, "projects store is not configured")
		return
	}
	actor, ok := ActorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	list, err := s.Projects.ListProjects(ctx, actor.UserID)
	if err != nil {
		s.logger().Error("list projects failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list projects")
		return
	}

	// Pre-size with make(..., 0, n) so JSON is [] when n==0, never null.
	out := make([]projectResponse, 0, len(list))
	for _, p := range list {
		out = append(out, projectResponseFrom(p))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	if s.Projects == nil {
		writeError(w, http.StatusInternalServerError, "projects store is not configured")
		return
	}

	// PathValue("id") is the {id} from "GET /projects/{id}" (Go 1.22 mux).
	id, err := store.ParseProjectID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	p, ok := s.requireProject(w, r, id)
	if !ok {
		return
	}

	writeJSON(w, http.StatusOK, projectResponseFrom(p))
}

// projectDeletePhrase is the exact confirmation the operator must send.
// The name is the stored project name, not a name supplied for the check.
func projectDeletePhrase(name string) string {
	return "delete this " + name
}

type deleteProjectRequest struct {
	Confirm string `json:"confirm"`
}

// deleteProject handles DELETE /projects/{id}.
//
// The body must be {"confirm":"delete this <name>"}. A missing or
// different phrase is 400 and leaves the row. The path id is not
// authorization: requireProject checks the session owner, and the
// DELETE statement checks owner_id again. Live containers are stopped
// and named volumes for those applications are removed before the row
// goes, using the same steps as deleting one application. Images are
// left on the Docker host.
func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	if s.Projects == nil {
		writeError(w, http.StatusInternalServerError, "projects store is not configured")
		return
	}
	id, err := store.ParseProjectID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	project, ok := s.requireProject(w, r, id)
	if !ok {
		return
	}
	var req deleteProjectRequest
	if !decodeJSON(r, w, &req) {
		return
	}
	if strings.TrimSpace(req.Confirm) != projectDeletePhrase(project.Name) {
		writeError(w, http.StatusBadRequest, "type "+projectDeletePhrase(project.Name)+" to confirm")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := s.stopProjectWorkloads(ctx, project.ID); err != nil {
		s.logger().Error("stop project workloads failed", "err", err, "project_id", project.ID)
		writeError(w, http.StatusInternalServerError, "failed to delete project")
		return
	}
	actor, _ := ActorFrom(r.Context())
	if err := s.Projects.DeleteProject(ctx, project.ID, actor.UserID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		s.logger().Error("delete project failed", "err", err, "project_id", project.ID)
		writeError(w, http.StatusInternalServerError, "failed to delete project")
		return
	}
	s.audit(r, actor.UserID, "project.delete", "project", project.ID, map[string]string{"name": project.Name})
	w.WriteHeader(http.StatusNoContent)
}

// stopProjectWorkloads stops running containers for every application
// in the project before the database cascade removes their rows.
// A list error aborts the delete so a running container is not left
// behind a missing row. Volume removal failures are logged and do not
// block the row delete; the volume row still cascades.
func (s *Server) stopProjectWorkloads(ctx context.Context, projectID string) error {
	if s.Apps == nil {
		return nil
	}
	apps, err := s.Apps.ListApplicationsByProject(ctx, projectID)
	if err != nil {
		return err
	}
	for _, app := range apps {
		liveRows, err := s.liveDeployments(ctx, app.ID)
		if err != nil {
			return err
		}
		for _, live := range liveRows {
			s.haltLive(ctx, live, "stopped by operator")
		}
		if s.Deployments != nil {
			list, err := s.Deployments.ListDeploymentsByApplication(ctx, app.ID)
			if err != nil {
				return err
			}
			for _, d := range list {
				if d.Status.InProgress() {
					s.abandonInProgress(ctx, d, "stopped by operator")
				}
			}
		}
		s.removeApplicationVolumes(ctx, app.ID)
	}
	return nil
}
