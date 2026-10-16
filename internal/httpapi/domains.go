package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/reyansh7/Forge/internal/store"
)

// DomainStore is the HTTP → application_domains boundary.
type DomainStore interface {
	CreateApplicationDomain(ctx context.Context, applicationID, hostname, environment string) (store.ApplicationDomain, error)
	ListApplicationDomains(ctx context.Context, applicationID string) ([]store.ApplicationDomain, error)
	GetApplicationDomain(ctx context.Context, applicationID, domainID string) (store.ApplicationDomain, error)
	ActivateApplicationDomain(ctx context.Context, applicationID, domainID string) (store.ApplicationDomain, error)
	DeleteApplicationDomain(ctx context.Context, applicationID, domainID string) error
}

type createDomainRequest struct {
	Hostname    string `json:"hostname"`
	Environment string `json:"environment"`
}

type domainResponse struct {
	ID            string `json:"id"`
	ApplicationID string `json:"application_id"`
	Hostname      string `json:"hostname"`
	Environment   string `json:"environment"`
	Status        string `json:"status"`
	Origin        string `json:"origin,omitempty"`
	TXTName       string `json:"txt_name,omitempty"`
	TXTValue      string `json:"txt_value,omitempty"`
}

func domainResponseFrom(d store.ApplicationDomain) domainResponse {
	resp := domainResponse{
		ID:            d.ID,
		ApplicationID: d.ApplicationID,
		Hostname:      d.Hostname,
		Environment:   d.Environment,
		Status:        d.Status,
		Origin:        d.Origin,
	}
	// Platform names are assigned by Forge. The TXT token exists in the
	// row because the column is required, and it is not an ownership proof
	// the operator needs to publish.
	if d.Origin == store.DomainOriginPlatform {
		return resp
	}
	resp.TXTName = store.DomainChallengeName(d.Hostname)
	resp.TXTValue = d.Token
	return resp
}

func (s *Server) lookupTXT(ctx context.Context, name string) ([]string, error) {
	if s != nil && s.LookupTXT != nil {
		return s.LookupTXT(ctx, name)
	}
	return net.DefaultResolver.LookupTXT(ctx, name)
}

func (s *Server) createDomain(w http.ResponseWriter, r *http.Request) {
	if s.Domains == nil {
		writeError(w, http.StatusInternalServerError, "domains are not configured")
		return
	}
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	var req createDomainRequest
	if !decodeJSON(r, w, &req) {
		return
	}
	if s.AppsDomain != "" && store.HostnameUnderZone(req.Hostname, s.AppsDomain) {
		writeError(w, http.StatusBadRequest, "names under the Forge apps domain are assigned from the application slug")
		return
	}
	if s.ControlHost != "" && strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(req.Hostname), "."), s.ControlHost) {
		writeError(w, http.StatusBadRequest, "that hostname is reserved for the Forge control plane")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	d, err := s.Domains.CreateApplicationDomain(ctx, app.ID, req.Hostname, req.Environment)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "domain is already claimed")
		return
	}
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "hostname") || strings.Contains(msg, "environment") || strings.Contains(msg, "at most") || strings.Contains(msg, "reserved") || strings.Contains(msg, "wildcard") {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		s.logger().Error("create application domain failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create domain")
		return
	}
	s.audit(r, "", "application.domain.create", "application", app.ID, map[string]string{
		"hostname":    d.Hostname,
		"environment": d.Environment,
	})
	writeJSON(w, http.StatusCreated, domainResponseFrom(d))
}

func (s *Server) listDomains(w http.ResponseWriter, r *http.Request) {
	if s.Domains == nil {
		writeError(w, http.StatusInternalServerError, "domains are not configured")
		return
	}
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	rows, err := s.Domains.ListApplicationDomains(ctx, app.ID)
	if err != nil {
		s.logger().Error("list application domains failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list domains")
		return
	}
	out := make([]domainResponse, 0, len(rows))
	for _, d := range rows {
		out = append(out, domainResponseFrom(d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) verifyDomain(w http.ResponseWriter, r *http.Request) {
	if s.Domains == nil {
		writeError(w, http.StatusInternalServerError, "domains are not configured")
		return
	}
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	domainID, err := store.ParseUUID(r.PathValue("domainId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid domain id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	d, err := s.Domains.GetApplicationDomain(ctx, app.ID, domainID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "domain not found")
		return
	}
	if err != nil {
		s.logger().Error("get application domain failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to verify domain")
		return
	}
	name := store.DomainChallengeName(d.Hostname)
	if name == "" {
		writeError(w, http.StatusBadRequest, "hostname is not a valid DNS name")
		return
	}
	// Lookup only the Forge TXT name we constructed. Do not dial A/AAAA
	// of the claimed host (that would be SSRF from an HTTP handler).
	txts, err := s.lookupTXT(ctx, name)
	if err != nil || !txtContainsToken(txts, d.Token) {
		writeError(w, http.StatusBadRequest, "DNS TXT record not found; add "+name+" with the token shown")
		return
	}
	got, err := s.Domains.ActivateApplicationDomain(ctx, app.ID, domainID)
	if err != nil {
		s.logger().Error("activate application domain failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to verify domain")
		return
	}
	s.reloadLiveProxy(ctx)
	s.audit(r, "", "application.domain.verify", "application", app.ID, map[string]string{"hostname": got.Hostname})
	writeJSON(w, http.StatusOK, domainResponseFrom(got))
}

func txtContainsToken(txts []string, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	for _, t := range txts {
		if strings.TrimSpace(t) == token {
			return true
		}
	}
	return false
}

func (s *Server) deleteDomain(w http.ResponseWriter, r *http.Request) {
	if s.Domains == nil {
		writeError(w, http.StatusInternalServerError, "domains are not configured")
		return
	}
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	domainID, err := store.ParseUUID(r.PathValue("domainId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid domain id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	d, err := s.Domains.GetApplicationDomain(ctx, app.ID, domainID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "domain not found")
		return
	}
	if err != nil {
		s.logger().Error("get application domain failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to delete domain")
		return
	}
	if d.Origin == store.DomainOriginPlatform {
		writeError(w, http.StatusConflict, "platform hostnames follow the application slug")
		return
	}
	if err := s.Domains.DeleteApplicationDomain(ctx, app.ID, domainID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "domain not found")
			return
		}
		s.logger().Error("delete application domain failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to delete domain")
		return
	}
	s.reloadLiveProxy(ctx)
	s.audit(r, "", "application.domain.delete", "application", app.ID, map[string]string{"hostname": d.Hostname})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) reloadLiveProxy(ctx context.Context) {
	if s == nil || s.Router == nil || s.Deployments == nil {
		return
	}
	live, err := s.Deployments.ListLiveDeployments(ctx)
	if err != nil {
		s.logger().Error("list live before domain reload failed", "err", err)
		return
	}
	if err := s.Router.Apply(ctx, live); err != nil {
		s.logger().Error("caddy apply after domain change failed", "err", err)
	}
}
