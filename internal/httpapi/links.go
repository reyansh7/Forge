package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/reyansh7/Forge/internal/store"
)

// LinkStore is the HTTP → application_links boundary.
// Placement of the URL string is the worker's job; HTTP only persists edges.
type LinkStore interface {
	CreateApplicationLink(ctx context.Context, sourceID, targetID, envKey string) (store.ApplicationLink, error)
	ListApplicationLinks(ctx context.Context, sourceID string) ([]store.ApplicationLink, error)
	DeleteApplicationLink(ctx context.Context, sourceID, linkID string) error
}

type createLinkRequest struct {
	TargetApplicationID string `json:"target_application_id"`
	EnvKey              string `json:"env_key"`
}

type linkResponse struct {
	ID                  string    `json:"id"`
	SourceApplicationID string    `json:"source_application_id"`
	TargetApplicationID string    `json:"target_application_id"`
	TargetName          string    `json:"target_name,omitempty"`
	EnvKey              string    `json:"env_key"`
	URL                 string    `json:"url,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

func (s *Server) proxyBase() string {
	if s != nil && strings.TrimSpace(s.ProxyPublicBase) != "" {
		return s.ProxyPublicBase
	}
	return "http://127.0.0.1:9080"
}

func (s *Server) linkResponse(ctx context.Context, l store.ApplicationLink) linkResponse {
	out := linkResponse{
		ID:                  l.ID,
		SourceApplicationID: l.SourceApplicationID,
		TargetApplicationID: l.TargetApplicationID,
		EnvKey:              l.EnvKey,
		CreatedAt:           l.CreatedAt.UTC(),
	}
	if s.Apps != nil {
		if tgt, err := s.Apps.GetApplication(ctx, l.TargetApplicationID); err == nil {
			out.TargetName = tgt.Name
			out.URL = store.ServiceHostURL(s.proxyBase(), tgt)
		}
	}
	return out
}

func (s *Server) createLink(w http.ResponseWriter, r *http.Request) {
	if s.Links == nil {
		writeError(w, http.StatusInternalServerError, "service links are not configured")
		return
	}
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	var req createLinkRequest
	if !decodeJSON(r, w, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	l, err := s.Links.CreateApplicationLink(ctx, app.ID, req.TargetApplicationID, req.EnvKey)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "target application not found")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "env key is already linked")
		return
	}
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "env key") || strings.Contains(msg, "itself") || strings.Contains(msg, "at most") || strings.Contains(msg, "reserved") {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		s.logger().Error("create application link failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create service link")
		return
	}
	s.audit(r, "", "application.link", "application", app.ID, map[string]string{
		"target_application_id": l.TargetApplicationID,
		"env_key":               l.EnvKey,
	})
	writeJSON(w, http.StatusCreated, s.linkResponse(ctx, l))
}

func (s *Server) listLinks(w http.ResponseWriter, r *http.Request) {
	if s.Links == nil {
		writeError(w, http.StatusInternalServerError, "service links are not configured")
		return
	}
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	links, err := s.Links.ListApplicationLinks(ctx, app.ID)
	if err != nil {
		s.logger().Error("list application links failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list service links")
		return
	}
	out := make([]linkResponse, 0, len(links))
	for _, l := range links {
		out = append(out, s.linkResponse(ctx, l))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteLink(w http.ResponseWriter, r *http.Request) {
	if s.Links == nil {
		writeError(w, http.StatusInternalServerError, "service links are not configured")
		return
	}
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	linkID, err := store.ParseUUID(r.PathValue("linkId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid link id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.Links.DeleteApplicationLink(ctx, app.ID, linkID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "service link not found")
			return
		}
		s.logger().Error("delete application link failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to delete service link")
		return
	}
	s.audit(r, "", "application.unlink", "application", app.ID, map[string]string{"link_id": linkID})
	w.WriteHeader(http.StatusNoContent)
}
