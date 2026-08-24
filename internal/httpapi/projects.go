package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/reyansh7/Forge/internal/store"
)

// ProjectStore is the HTTP → persistence boundary for increment 0.2.
//
// What: three methods the handlers need. Not the whole of database/sql.
// Why: tests inject memProjects (no Docker). cmd/api injects *store.Postgres.
// Who calls it: createProject / listProjects / getProject.
// What it calls (production): PostgreSQL via store.Postgres.
//
// This is dependency inversion: HTTP depends on an interface, not a
// concrete driver. That is how /health tests used stubPing in 0.1.
//
// Deferred: Update/Delete, authz ("does this user own this project?").
type ProjectStore interface {
	CreateProject(ctx context.Context, in store.ProjectInput) (store.Project, error)
	GetProject(ctx context.Context, id string) (store.Project, error)
	ListProjects(ctx context.Context) ([]store.Project, error)
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
// There is no authentication in Phase 0 — loopback is the only gate.
func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	if s.Projects == nil {
		writeError(w, http.StatusInternalServerError, "projects store is not configured")
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

	p, err := s.Projects.CreateProject(ctx, in)
	if err != nil {
		s.logger().Error("create project failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create project")
		return
	}

	// Location is the HTTP convention for "here is the new resource".
	w.Header().Set("Location", "/projects/"+p.ID)
	writeJSON(w, http.StatusCreated, projectResponseFrom(p))
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	if s.Projects == nil {
		writeError(w, http.StatusInternalServerError, "projects store is not configured")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	list, err := s.Projects.ListProjects(ctx)
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

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	p, err := s.Projects.GetProject(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		s.logger().Error("get project failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to get project")
		return
	}

	writeJSON(w, http.StatusOK, projectResponseFrom(p))
}
