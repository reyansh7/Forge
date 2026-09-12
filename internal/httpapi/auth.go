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

const (
	sessionCookieName = "forge_session"
	loginMaxAttempts  = 10
	loginWindow       = 10 * time.Minute
)

// IdentityStore is the HTTP → persistence boundary for Phase 3 auth.
//
// Tests inject memAuth. cmd/api injects *store.Postgres. Handlers never
// issue SQL. Password hashing stays in store so bcrypt is not copied.
type IdentityStore interface {
	UserCount(ctx context.Context) (int, error)
	CreateFirstUser(ctx context.Context, username, passwordHash string) (store.User, error)
	CreateUser(ctx context.Context, username, passwordHash string) (store.User, error)
	GetUserByUsername(ctx context.Context, username string) (store.User, error)
	LookupSession(ctx context.Context, tokenHash string) (store.User, error)
	CreateSession(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error
	DeleteSession(ctx context.Context, tokenHash string) error
	ClaimOrphanedProjects(ctx context.Context, ownerID string) (int64, error)
	RecordAudit(ctx context.Context, e store.AuditEvent) error
}

// Actor is the authenticated operator for this request.
//
// It is stored on context by withAuth. Handlers must not take a user id
// from JSON or a query string and treat it as this value.
type Actor struct {
	UserID   string
	Username string
}

type actorCtxKey struct{}

// ActorFrom returns the operator withAuth placed on the request.
func ActorFrom(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorCtxKey{}).(Actor)
	if !ok || a.UserID == "" {
		return Actor{}, false
	}
	return a, true
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicPath(r.Method, canonicalAPIPath(r.URL.Path)) {
			next.ServeHTTP(w, r)
			return
		}
		if s.Auth == nil {
			// Fail closed. A miswired API must not serve projects
			// without identity the way Phase 0 did.
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		raw := bearerOrCookie(r)
		if raw == "" {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		u, err := s.Auth.LookupSession(ctx, store.HashSessionToken(raw))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		actor := Actor{UserID: u.ID, Username: u.Username}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorCtxKey{}, actor)))
	})
}

// canonicalAPIPath maps the dashboard rewrite prefix and trailing
// slashes onto the mux paths. Next.js sometimes forwards
// /forge-api/auth/signup instead of /auth/signup; without this, withAuth
// treats a public route as protected and returns 401.
func canonicalAPIPath(path string) string {
	p := strings.TrimSpace(path)
	p = strings.TrimSuffix(p, "/")
	if rest, ok := strings.CutPrefix(p, "/forge-api"); ok {
		p = rest
	}
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

func isPublicPath(method, path string) bool {
	switch {
	case method == http.MethodGet && path == "/health":
		return true
	case method == http.MethodGet && path == "/auth/status":
		return true
	case method == http.MethodPost && path == "/auth/bootstrap":
		return true
	case method == http.MethodPost && path == "/auth/signup":
		return true
	case method == http.MethodPost && path == "/auth/login":
		return true
	case method == http.MethodPost && path == "/auth/logout":
		return true
	default:
		return false
	}
}

func bearerOrCookie(r *http.Request) string {
	// Cookie is what this browser last received from Set-Cookie.
	// Prefer it over Authorization so a leftover dashboard Bearer
	// from a previous operator cannot keep that operator signed in
	// after a new login replaced the cookie.
	if c, err := r.Cookie(sessionCookieName); err == nil {
		if v := strings.TrimSpace(c.Value); v != "" {
			return v
		}
	}
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func clientIP(r *http.Request) string {
	// RemoteAddr is host:port as seen by net/http. We ignore
	// X-Forwarded-For: this process is loopback-only and that header
	// would let a client pick someone else's rate-limit bucket.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) gate() *attemptGate {
	if s.loginGate == nil {
		s.loginGate = newAttemptGate(loginMaxAttempts, loginWindow)
	}
	return s.loginGate
}

type authCredentials struct {
	Username        string `json:"username"`
	Password        string `json:"password"`
	PasswordConfirm string `json:"password_confirm"`
}

type authUserResponse struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type authSessionResponse struct {
	Token string           `json:"token"`
	User  authUserResponse `json:"user"`
}

type authStatusResponse struct {
	BootstrapRequired bool `json:"bootstrap_required"`
}

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	if s.Auth == nil {
		writeJSON(w, http.StatusOK, authStatusResponse{BootstrapRequired: true})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	n, err := s.Auth.UserCount(ctx)
	if err != nil {
		s.logger().Error("user count failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to read auth status")
		return
	}
	writeJSON(w, http.StatusOK, authStatusResponse{BootstrapRequired: n == 0})
}

func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	if s.Auth == nil {
		writeError(w, http.StatusInternalServerError, "auth store is not configured")
		return
	}
	if !s.gate().allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	var req authCredentials
	if !decodeJSON(r, w, &req) {
		return
	}
	username, err := store.ValidateUsername(req.Username)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := store.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	u, err := s.Auth.CreateFirstUser(ctx, username, hash)
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "bootstrap already completed")
		return
	}
	if err != nil {
		s.logger().Error("bootstrap user failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create operator")
		return
	}
	if _, err := s.Auth.ClaimOrphanedProjects(ctx, u.ID); err != nil {
		s.logger().Error("claim orphaned projects failed", "err", err)
	}

	token, err := s.issueSession(ctx, w, r, u)
	if err != nil {
		s.logger().Error("bootstrap session failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	s.audit(r, u.ID, "bootstrap", "user", u.ID, map[string]string{"username": u.Username})
	writeJSON(w, http.StatusCreated, authSessionResponse{
		Token: token,
		User:  authUserResponse{ID: u.ID, Username: u.Username},
	})
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	// Sign-up creates an operator with their own owner_id. Projects
	// created after this session belong only to that user. This is
	// still local identity (Phase 3), not team roles (later).
	// Open registration is acceptable on loopback; do not keep it if
	// the API is later published without an invite gate.
	if s.Auth == nil {
		writeError(w, http.StatusInternalServerError, "auth store is not configured")
		return
	}
	if !s.gate().allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	var req authCredentials
	if !decodeJSON(r, w, &req) {
		return
	}
	if req.PasswordConfirm != req.Password {
		writeError(w, http.StatusBadRequest, "passwords do not match")
		return
	}
	username, err := store.ValidateUsername(req.Username)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := store.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	n, err := s.Auth.UserCount(ctx)
	if err != nil {
		s.logger().Error("signup count failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create account")
		return
	}

	var u store.User
	if n == 0 {
		u, err = s.Auth.CreateFirstUser(ctx, username, hash)
		if err == nil {
			if _, claimErr := s.Auth.ClaimOrphanedProjects(ctx, u.ID); claimErr != nil {
				s.logger().Error("claim orphaned projects failed", "err", claimErr)
			}
		}
	} else {
		u, err = s.Auth.CreateUser(ctx, username, hash)
	}
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "username is already taken")
		return
	}
	if err != nil {
		s.logger().Error("signup user failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create account")
		return
	}

	token, err := s.issueSession(ctx, w, r, u)
	if err != nil {
		s.logger().Error("signup session failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	s.audit(r, u.ID, "signup", "user", u.ID, map[string]string{"username": u.Username})
	writeJSON(w, http.StatusCreated, authSessionResponse{
		Token: token,
		User:  authUserResponse{ID: u.ID, Username: u.Username},
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.Auth == nil {
		writeError(w, http.StatusInternalServerError, "auth store is not configured")
		return
	}
	if !s.gate().allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	var req authCredentials
	if !decodeJSON(r, w, &req) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Same 401 for unknown user and bad password so this endpoint
	// cannot be used to enumerate usernames.
	fail := func() {
		s.audit(r, "", "login_failed", "session", "", map[string]string{})
		writeError(w, http.StatusUnauthorized, "invalid credentials")
	}

	u, err := s.Auth.GetUserByUsername(ctx, req.Username)
	if err != nil {
		fail()
		return
	}
	if err := store.CheckPassword(u.PasswordHash, req.Password); err != nil {
		fail()
		return
	}

	token, err := s.issueSession(ctx, w, r, u)
	if err != nil {
		s.logger().Error("login session failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	s.audit(r, u.ID, "login", "session", u.ID, map[string]string{"username": u.Username})
	writeJSON(w, http.StatusOK, authSessionResponse{
		Token: token,
		User:  authUserResponse{ID: u.ID, Username: u.Username},
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	raw := bearerOrCookie(r)
	clearSessionCookie(w)
	if raw != "" && s.Auth != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := s.Auth.DeleteSession(ctx, store.HashSessionToken(raw)); err != nil {
			s.logger().Error("logout failed", "err", err)
			writeError(w, http.StatusInternalServerError, "failed to log out")
			return
		}
		s.audit(r, "", "logout", "session", "", map[string]string{})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	actor, ok := ActorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, authUserResponse{ID: actor.UserID, Username: actor.Username})
}

func (s *Server) issueSession(ctx context.Context, w http.ResponseWriter, r *http.Request, u store.User) (string, error) {
	// One HttpOnly cookie name for the browser. A new login or signup
	// replaces it. The previous token presented on this request is
	// revoked so operator A's session cannot stay valid after operator
	// B signs in on the same browser.
	if prev := bearerOrCookie(r); prev != "" {
		if err := s.Auth.DeleteSession(ctx, store.HashSessionToken(prev)); err != nil {
			s.logger().Error("revoke previous session failed", "err", err)
		}
	}
	raw, hash, err := store.NewSessionToken()
	if err != nil {
		return "", err
	}
	if err := s.Auth.CreateSession(ctx, u.ID, hash, store.SessionExpiry(time.Now())); err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    raw,
		Path:     "/",
		MaxAge:   int(7 * 24 * time.Hour / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Secure is false: the control plane is HTTP on loopback.
		// Phase 5 TLS must flip this when the API is served over https.
		Secure: false,
	})
	return raw, nil
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) audit(r *http.Request, actorID, action, resourceType, resourceID string, meta map[string]string) {
	if s.Auth == nil {
		return
	}
	if actorID == "" {
		if a, ok := ActorFrom(r.Context()); ok {
			actorID = a.UserID
		}
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	err := s.Auth.RecordAudit(ctx, store.AuditEvent{
		ActorID:      actorID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		IP:           clientIP(r),
		Metadata:     meta,
	})
	if err != nil {
		s.logger().Error("audit failed", "err", err, "action", action)
	}
}
