package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/reyansh7/Forge/internal/store"
)

// requireProject loads a project and proves the authenticated actor owns it.
//
// A UUID in the path is client-supplied. It is not authorization.
// Wrong owner and missing row both return 404 so this API cannot be used
// to confirm that another operator's project id exists.
func (s *Server) requireProject(w http.ResponseWriter, r *http.Request, projectID string) (store.Project, bool) {
	return s.requireOwnedProject(w, r, projectID, "project not found")
}

func (s *Server) requireOwnedProject(w http.ResponseWriter, r *http.Request, projectID, notFound string) (store.Project, bool) {
	if notFound == "" {
		notFound = "project not found"
	}
	actor, ok := ActorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return store.Project{}, false
	}
	if s.Projects == nil {
		writeError(w, http.StatusInternalServerError, "projects store is not configured")
		return store.Project{}, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	p, err := s.Projects.GetProject(ctx, projectID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, notFound)
		return store.Project{}, false
	}
	if err != nil {
		s.logger().Error("get project for authz failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to authorize")
		return store.Project{}, false
	}
	if p.OwnerID == "" || p.OwnerID != actor.UserID {
		writeError(w, http.StatusNotFound, notFound)
		return store.Project{}, false
	}
	return p, true
}

func (s *Server) actorOwnsProject(w http.ResponseWriter, r *http.Request, projectID, notFound string) bool {
	_, ok := s.requireOwnedProject(w, r, projectID, notFound)
	return ok
}
