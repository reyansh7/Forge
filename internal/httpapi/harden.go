package httpapi

// Phase 5 operator hardening helpers: quotas and GET /operator/status.
//
// Quotas are process config (FORGE_MAX_*), not client-supplied. The
// handler counts the authenticated actor's rows; a UUID in the body
// cannot raise someone else's cap. Rate limits for deploy sit on the
// same in-memory gate as login (one API process).

import (
	"context"
	"net/http"
	"time"

	"github.com/reyansh7/Forge/internal/config"
)

func (s *Server) projectQuota() int {
	if s.Limits.MaxProjectsPerOwner > 0 {
		return s.Limits.MaxProjectsPerOwner
	}
	return config.DefaultMaxProjects
}

func (s *Server) inflightQuota() int {
	if s.Limits.MaxInflightDeploys > 0 {
		return s.Limits.MaxInflightDeploys
	}
	return config.DefaultMaxInflightDeploys
}

func (s *Server) nodeInflightQuota() int {
	if s.Limits.MaxInflightPerNode > 0 {
		return s.Limits.MaxInflightPerNode
	}
	return config.DefaultMaxInflightPerNode
}

func (s *Server) deployAttempts() *attemptGate {
	if s.deployGate == nil {
		s.deployGate = newAttemptGate(10, 10*time.Minute)
	}
	return s.deployGate
}

type operatorStatusResponse struct {
	SchemaMigrations    []string `json:"schema_migrations"`
	TLSEnabled          bool     `json:"tls_enabled"`
	MaxProjectsPerOwner int      `json:"max_projects_per_owner"`
	MaxInflightDeploys  int      `json:"max_inflight_deploys"`
	MaxInflightPerNode  int      `json:"max_inflight_per_node"`
}

// operatorStatus is GET /operator/status.
//
// It is not public: a session is required so schema filenames and
// quota numbers are not an unauthenticated fingerprint. TLSEnabled
// mirrors SecureCookies (ListenAndServeTLS), not "Caddy has :443".
func (s *Server) operatorStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := ActorFrom(r.Context()); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	out := operatorStatusResponse{
		SchemaMigrations:    []string{},
		TLSEnabled:          s.SecureCookies,
		MaxProjectsPerOwner: s.projectQuota(),
		MaxInflightDeploys:  s.inflightQuota(),
		MaxInflightPerNode:  s.nodeInflightQuota(),
	}
	if s.Schemas != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		vers, err := s.Schemas.SchemaVersions(ctx)
		if err != nil {
			s.logger().Error("schema versions failed", "err", err)
			writeError(w, http.StatusInternalServerError, "failed to read operator status")
			return
		}
		out.SchemaMigrations = vers
	}
	writeJSON(w, http.StatusOK, out)
}
