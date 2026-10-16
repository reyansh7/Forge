package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/reyansh7/Forge/internal/githubapi"
	"github.com/reyansh7/Forge/internal/store"
)

const githubStateCookie = "forge_github_state"

// GitHubStore is the HTTP → Postgres boundary for OAuth and bindings.
//
// Tokens and webhook secrets stay on this side. JSON responses only
// carry login, repo, branch, and environment.
type GitHubStore interface {
	UpsertGitHubAccount(ctx context.Context, userID string, githubUserID int64, login, token string) (store.GitHubAccount, error)
	GetGitHubAccount(ctx context.Context, userID string) (store.GitHubAccount, error)
	GetGitHubAccountByGitHubUserID(ctx context.Context, githubUserID int64) (store.GitHubAccount, error)
	DeleteGitHubAccount(ctx context.Context, userID string) error
	CreateOAuthState(ctx context.Context, userID, nonce, returnTo string) error
	CreateLoginOAuthState(ctx context.Context, nonce, returnTo string) error
	ConsumeOAuthState(ctx context.Context, nonce string) (userID, returnTo string, err error)
	UpsertApplicationGitHub(ctx context.Context, in store.ApplicationGitHub) (store.ApplicationGitHub, error)
	GetApplicationGitHub(ctx context.Context, applicationID string) (store.ApplicationGitHub, error)
	GetApplicationGitHubByRepoBranch(ctx context.Context, repo, branch string) (store.ApplicationGitHub, error)
	ListApplicationGitHubByOwner(ctx context.Context, ownerID string) ([]store.ApplicationGitHub, error)
	DeleteApplicationGitHub(ctx context.Context, applicationID string) error
	DeleteApplicationGitHubByOwner(ctx context.Context, ownerID string) error
	ClaimWebhookDelivery(ctx context.Context, deliveryID string) (bool, error)
}

type githubStatusResponse struct {
	Configured bool   `json:"configured"`
	Connected  bool   `json:"connected"`
	Login      string `json:"login,omitempty"`
}

type githubRepoResponse struct {
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
}

type applicationGitHubResponse struct {
	Connected   bool   `json:"connected"`
	Repo        string `json:"repo,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Environment string `json:"environment,omitempty"`
	// Webhook is false when the repo is saved but GitHub was not given a
	// hook, because FORGE_WEBHOOK_BASE is not a public URL.
	Webhook bool   `json:"webhook"`
	Warning string `json:"warning,omitempty"`
}

type connectGitHubRequest struct {
	Repo        string `json:"repo"`
	Branch      string `json:"branch"`
	Environment string `json:"environment"`
}

func (s *Server) githubConfigured() bool {
	return strings.TrimSpace(s.GitHubClientID) != "" && strings.TrimSpace(s.GitHubClientSecret) != "" && s.GitHub != nil && s.GitHubAccounts != nil
}

func (s *Server) githubRedirectURI() string {
	base := strings.TrimRight(strings.TrimSpace(s.PublicBase), "/")
	if base == "" {
		base = "http://127.0.0.1:3000"
	}
	return base + "/forge-api/integrations/github/callback"
}

func (s *Server) webhookPublicURL(applicationID string) string {
	base := strings.TrimRight(strings.TrimSpace(s.WebhookBase), "/")
	if base == "" {
		base = "http://127.0.0.1:8080"
	}
	return base + "/webhooks/github/" + applicationID
}

// webhookURLReachable is false for loopback and private addresses.
// GitHub rejects those with HTTP 422, and a laptop cannot receive the
// push event anyway. A public hostname is left for GitHub to validate.
func webhookURLReachable(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return true
	}
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}

func localWebhookWarning() string {
	return "Saved. Press Deploy to build this commit. A git push deploys on its own only after Forge has a public address."
}

// githubUserAllowed is false for every id when the operator has not
// set FORGE_GITHUB_ALLOWED_USER_IDS. That is the personal-PaaS default:
// reaching the login page is not authorization to deploy.
func (s *Server) githubUserAllowed(id int64) bool {
	if s == nil || id <= 0 {
		return false
	}
	for _, allowed := range s.GitHubAllowedUserIDs {
		if allowed == id {
			return true
		}
	}
	return false
}

func (s *Server) githubAPI() githubapi.API {
	if s.GitHub != nil {
		return s.GitHub
	}
	return &githubapi.Client{ClientID: s.GitHubClientID, ClientSecret: s.GitHubClientSecret}
}

func (s *Server) githubStatus(w http.ResponseWriter, r *http.Request) {
	actor, ok := ActorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	out := githubStatusResponse{Configured: s.githubConfigured()}
	if s.GitHubAccounts == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	acc, err := s.GitHubAccounts.GetGitHubAccount(ctx, actor.UserID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, out)
		return
	}
	if err != nil {
		s.logger().Error("get github account failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to load github status")
		return
	}
	out.Connected = true
	out.Login = acc.Login
	writeJSON(w, http.StatusOK, out)
}

// githubLogin starts Sign in with GitHub. It is public: there is no
// Forge session yet. The nonce is stored with a NULL user_id and also
// set as an HttpOnly cookie. The callback accepts the login only when
// both match, so a copied ?code=&state= URL in another browser fails.
func (s *Server) githubLogin(w http.ResponseWriter, r *http.Request) {
	public := s.publicOrigin()
	if !s.gate().allow(clientIP(r)) {
		http.Redirect(w, r, public+"/login?github=rate_limited", http.StatusFound)
		return
	}
	if !s.githubConfigured() || s.Auth == nil {
		http.Redirect(w, r, public+"/login?github=not_configured", http.StatusFound)
		return
	}
	nonce, err := store.NewOAuthNonce()
	if err != nil {
		http.Redirect(w, r, public+"/login?github=start_failed", http.StatusFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.GitHubAccounts.CreateLoginOAuthState(ctx, nonce, "/"); err != nil {
		s.logger().Error("create github login state failed", "err", err)
		http.Redirect(w, r, public+"/login?github=start_failed", http.StatusFound)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     githubStateCookie,
		Value:    nonce,
		Path:     "/",
		MaxAge:   int(10 * time.Minute / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.SecureCookies,
	})
	http.Redirect(w, r, s.githubAuthorizeURL(nonce), http.StatusFound)
}

func (s *Server) githubStart(w http.ResponseWriter, r *http.Request) {
	actor, ok := ActorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if !s.githubConfigured() {
		writeError(w, http.StatusServiceUnavailable, "github oauth is not configured")
		return
	}
	nonce, err := store.NewOAuthNonce()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start github oauth")
		return
	}
	returnTo := store.SanitizeReturnTo(r.URL.Query().Get("return_to"))
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.GitHubAccounts.CreateOAuthState(ctx, actor.UserID, nonce, returnTo); err != nil {
		s.logger().Error("create oauth state failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to start github oauth")
		return
	}
	http.Redirect(w, r, s.githubAuthorizeURL(nonce), http.StatusFound)
}

func (s *Server) githubAuthorizeURL(nonce string) string {
	client, ok := s.githubAPI().(*githubapi.Client)
	if ok {
		return client.AuthorizeURL(s.githubRedirectURI(), nonce)
	}
	if starter, ok := s.githubAPI().(interface {
		AuthorizeURL(string, string) string
	}); ok {
		return starter.AuthorizeURL(s.githubRedirectURI(), nonce)
	}
	return "https://github.com/login/oauth/authorize?" + url.Values{
		"client_id":    {s.GitHubClientID},
		"redirect_uri": {s.githubRedirectURI()},
		"scope":        {"repo admin:repo_hook read:user"},
		"state":        {nonce},
	}.Encode()
}

func (s *Server) publicOrigin() string {
	public := strings.TrimRight(strings.TrimSpace(s.PublicBase), "/")
	if public == "" {
		return "http://127.0.0.1:3000"
	}
	return public
}

func (s *Server) githubCallback(w http.ResponseWriter, r *http.Request) {
	public := s.publicOrigin()
	fail := func(reason string) {
		http.Redirect(w, r, public+"/?github="+url.QueryEscape(reason), http.StatusFound)
	}
	if !s.githubConfigured() {
		fail("not_configured")
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || state == "" {
		fail("invalid_callback")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	userID, returnTo, err := s.GitHubAccounts.ConsumeOAuthState(ctx, state)
	if err != nil {
		http.Redirect(w, r, public+"/login?github=invalid_state", http.StatusFound)
		return
	}
	// An empty user id is Sign in with GitHub. Identity comes from
	// GitHub's user id plus the state cookie, not from any session
	// that happens to be in this browser.
	if userID == "" {
		s.finishGitHubLogin(w, r, ctx, public, code, state)
		return
	}
	// Connect is a public path (GitHub's redirect). Still require the
	// forge_session cookie to match the operator who started OAuth so
	// a stolen ?code= URL in another browser cannot bind their GitHub
	// to this account.
	actor, ok := sessionActor(s, r)
	if !ok || actor.UserID != userID {
		fail("session_mismatch")
		return
	}
	token, err := s.githubAPI().Exchange(ctx, code, s.githubRedirectURI())
	if err != nil {
		s.logger().Error("github oauth exchange failed", "err", err)
		fail("exchange_failed")
		return
	}
	ghUser, err := s.githubAPI().User(ctx, token)
	if err != nil {
		s.logger().Error("github user failed", "err", err)
		fail("user_failed")
		return
	}
	// Connect and login share this allowlist. A webhook is not accepted
	// because the account is listed; the HMAC check stays separate.
	if !s.githubUserAllowed(ghUser.ID) {
		fail("not_allowed")
		return
	}
	if _, err := s.GitHubAccounts.UpsertGitHubAccount(ctx, userID, ghUser.ID, ghUser.Login, token); err != nil {
		if errors.Is(err, store.ErrConflict) {
			fail("account_taken")
			return
		}
		s.logger().Error("store github account failed", "err", err)
		fail("store_failed")
		return
	}
	s.audit(r, userID, "github.connect", "user", userID, map[string]string{"login": ghUser.Login})
	http.Redirect(w, r, public+returnTo, http.StatusFound)
}

// finishGitHubLogin turns a GitHub identity into a Forge session.
//
// Match is github_user_id, never the login string. A password account
// named "octocat" stays untouched when GitHub user octocat signs in
// for the first time. A later sign-in finds the row created here.
// The password hash is random and unused: this account enters through
// GitHub. The state cookie is cleared even on failure so it cannot be
// replayed.
func (s *Server) finishGitHubLogin(w http.ResponseWriter, r *http.Request, ctx context.Context, public, code, state string) {
	clearGitHubStateCookie(w)
	fail := func(reason string) {
		http.Redirect(w, r, public+"/login?github="+url.QueryEscape(reason), http.StatusFound)
	}
	if !githubStateCookieMatches(r, state) {
		fail("invalid_state")
		return
	}
	if s.Auth == nil {
		fail("not_configured")
		return
	}
	token, err := s.githubAPI().Exchange(ctx, code, s.githubRedirectURI())
	if err != nil {
		s.logger().Error("github login exchange failed", "err", err)
		fail("exchange_failed")
		return
	}
	ghUser, err := s.githubAPI().User(ctx, token)
	if err != nil || ghUser.ID <= 0 || strings.TrimSpace(ghUser.Login) == "" {
		s.logger().Error("github login user failed", "err", err)
		fail("user_failed")
		return
	}
	if !s.githubUserAllowed(ghUser.ID) {
		fail("not_allowed")
		return
	}
	acc, err := s.GitHubAccounts.GetGitHubAccountByGitHubUserID(ctx, ghUser.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.logger().Error("lookup github login failed", "err", err)
		fail("store_failed")
		return
	}
	var user store.User
	if err == nil {
		user, err = s.Auth.GetUser(ctx, acc.UserID)
		if err != nil {
			s.logger().Error("github login user missing", "err", err)
			fail("store_failed")
			return
		}
		if _, err := s.GitHubAccounts.UpsertGitHubAccount(ctx, user.ID, ghUser.ID, ghUser.Login, token); err != nil {
			s.logger().Error("refresh github token failed", "err", err)
			fail("store_failed")
			return
		}
	} else {
		user, err = s.createGitHubUser(ctx, ghUser, token)
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				fail("account_taken")
				return
			}
			s.logger().Error("create github user failed", "err", err)
			fail("store_failed")
			return
		}
	}
	if _, err := s.issueSession(ctx, w, r, user); err != nil {
		s.logger().Error("github login session failed", "err", err)
		fail("session_failed")
		return
	}
	s.audit(r, user.ID, "github.login", "user", user.ID, map[string]string{"login": ghUser.Login})
	http.Redirect(w, r, public+"/", http.StatusFound)
}

func (s *Server) createGitHubUser(ctx context.Context, ghUser githubapi.User, token string) (store.User, error) {
	hash, err := randomPasswordHash()
	if err != nil {
		return store.User{}, err
	}
	base := forgeUsernameFromGitHub(ghUser.Login)
	n, err := s.Auth.UserCount(ctx)
	if err != nil {
		return store.User{}, err
	}
	var user store.User
	for i := 0; i < 20; i++ {
		name := base
		if i > 0 {
			name = suffixUsername(base, i+1)
		}
		if n == 0 && i == 0 {
			user, err = s.Auth.CreateFirstUser(ctx, name, hash)
			if err == nil {
				if _, claimErr := s.Auth.ClaimOrphanedProjects(ctx, user.ID); claimErr != nil {
					s.logger().Error("claim orphaned projects failed", "err", claimErr)
				}
			}
		} else {
			user, err = s.Auth.CreateUser(ctx, name, hash)
		}
		if errors.Is(err, store.ErrConflict) {
			n = 1
			continue
		}
		if err != nil {
			return store.User{}, err
		}
		if _, err := s.GitHubAccounts.UpsertGitHubAccount(ctx, user.ID, ghUser.ID, ghUser.Login, token); err != nil {
			return store.User{}, err
		}
		return user, nil
	}
	return store.User{}, store.ErrConflict
}

func githubStateCookieMatches(r *http.Request, state string) bool {
	c, err := r.Cookie(githubStateCookie)
	if err != nil {
		return false
	}
	got := []byte(c.Value)
	want := []byte(state)
	if len(got) != len(want) || len(got) < 16 {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

func clearGitHubStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     githubStateCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func randomPasswordHash() (string, error) {
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return store.HashPassword(hex.EncodeToString(buf[:]))
}

// forgeUsernameFromGitHub maps a GitHub login onto Forge's username
// rules (letter start, 3–32, letters digits _ -). GitHub allows a
// leading digit and a single character; those cannot be stored as-is.
func forgeUsernameFromGitHub(login string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(login)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	s := strings.Trim(b.String(), "-_")
	if s == "" || !unicode.IsLetter(rune(s[0])) {
		s = "gh" + s
	}
	if utf8.RuneCountInString(s) < 3 {
		s += "user"
	}
	if utf8.RuneCountInString(s) > 32 {
		s = string([]rune(s)[:32])
		s = strings.TrimRight(s, "-_")
	}
	if _, err := store.ValidateUsername(s); err != nil {
		return "ghuser"
	}
	return s
}

func suffixUsername(base string, n int) string {
	suffix := "-" + itoa(n)
	room := 32 - utf8.RuneCountInString(suffix)
	runes := []rune(base)
	if len(runes) > room {
		runes = runes[:room]
	}
	return strings.TrimRight(string(runes), "-_") + suffix
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func (s *Server) githubDisconnect(w http.ResponseWriter, r *http.Request) {
	actor, ok := ActorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if s.GitHubAccounts == nil {
		writeError(w, http.StatusInternalServerError, "github is not configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	acc, err := s.GitHubAccounts.GetGitHubAccount(ctx, actor.UserID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "github is not connected")
		return
	}
	if err != nil {
		s.logger().Error("get github account failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to disconnect github")
		return
	}
	bindings, err := s.GitHubAccounts.ListApplicationGitHubByOwner(ctx, actor.UserID)
	if err != nil {
		s.logger().Error("list github bindings failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to disconnect github")
		return
	}
	if s.GitHub != nil {
		for _, b := range bindings {
			if err := s.GitHub.DeleteWebhook(ctx, acc.AccessToken, b.RepoFullName, b.WebhookID); err != nil {
				s.logger().Error("delete github webhook failed", "err", err, "repo", b.RepoFullName)
			}
		}
	}
	if err := s.GitHubAccounts.DeleteApplicationGitHubByOwner(ctx, actor.UserID); err != nil {
		s.logger().Error("delete github bindings failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to disconnect github")
		return
	}
	if err := s.GitHubAccounts.DeleteGitHubAccount(ctx, actor.UserID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.logger().Error("delete github account failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to disconnect github")
		return
	}
	s.audit(r, "", "github.disconnect", "user", actor.UserID, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) githubRepos(w http.ResponseWriter, r *http.Request) {
	actor, ok := ActorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if !s.githubConfigured() {
		writeError(w, http.StatusServiceUnavailable, "github oauth is not configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	acc, err := s.GitHubAccounts.GetGitHubAccount(ctx, actor.UserID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusConflict, "connect github first")
		return
	}
	if err != nil {
		s.logger().Error("get github account failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list repositories")
		return
	}
	repos, err := s.githubAPI().ListRepos(ctx, acc.AccessToken)
	if err != nil {
		s.logger().Error("list github repos failed", "err", err)
		writeError(w, http.StatusBadGateway, "failed to list repositories")
		return
	}
	out := make([]githubRepoResponse, 0, len(repos))
	for _, repo := range repos {
		out = append(out, githubRepoResponse{
			FullName:      repo.FullName,
			Private:       repo.Private,
			DefaultBranch: repo.DefaultBranch,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getApplicationGitHub(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	if s.GitHubAccounts == nil {
		writeJSON(w, http.StatusOK, applicationGitHubResponse{})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	b, err := s.GitHubAccounts.GetApplicationGitHub(ctx, app.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, applicationGitHubResponse{})
		return
	}
	if err != nil {
		s.logger().Error("get application github failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to load github connection")
		return
	}
	resp := applicationGitHubResponse{
		Connected:   true,
		Repo:        b.RepoFullName,
		Branch:      b.Branch,
		Environment: b.Environment,
		Webhook:     b.WebhookID > 0,
	}
	if !resp.Webhook {
		resp.Warning = localWebhookWarning()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) putApplicationGitHub(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	actor, _ := ActorFrom(r.Context())
	if !s.githubConfigured() {
		writeError(w, http.StatusServiceUnavailable, "github oauth is not configured")
		return
	}
	var req connectGitHubRequest
	if !decodeJSON(r, w, &req) {
		return
	}
	repo, err := store.ValidateRepoFullName(req.Repo)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	branch, err := store.ValidateBranch(req.Branch)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	env, err := store.ParseEnvironment(req.Environment)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	acc, err := s.GitHubAccounts.GetGitHubAccount(ctx, actor.UserID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusConflict, "connect github first")
		return
	}
	if err != nil {
		s.logger().Error("get github account failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to connect repository")
		return
	}
	if !s.repoVisible(ctx, acc.AccessToken, repo) {
		writeError(w, http.StatusNotFound, "repository not found")
		return
	}

	prev, _ := s.GitHubAccounts.GetApplicationGitHub(ctx, app.ID)
	if prev.WebhookID > 0 && s.GitHub != nil {
		_ = s.GitHub.DeleteWebhook(ctx, acc.AccessToken, prev.RepoFullName, prev.WebhookID)
	}

	secret, err := store.NewWebhookSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to connect repository")
		return
	}
	hookURL := s.webhookPublicURL(app.ID)
	var hookID int64
	if webhookURLReachable(hookURL) {
		hookID, err = s.githubAPI().CreatePushWebhook(ctx, acc.AccessToken, repo, hookURL, secret)
		if err != nil {
			s.logger().Error("create github webhook failed", "err", err)
			writeError(w, http.StatusBadGateway, "failed to create github webhook")
			return
		}
	}
	binding, err := s.GitHubAccounts.UpsertApplicationGitHub(ctx, store.ApplicationGitHub{
		ApplicationID: app.ID,
		OwnerID:       actor.UserID,
		RepoFullName:  repo,
		Branch:        branch,
		Environment:   env,
		WebhookID:     hookID,
		WebhookSecret: secret,
	})
	if errors.Is(err, store.ErrConflict) {
		if s.GitHub != nil {
			_ = s.GitHub.DeleteWebhook(ctx, acc.AccessToken, repo, hookID)
		}
		writeError(w, http.StatusConflict, "repository and branch are already connected")
		return
	}
	if err != nil {
		s.logger().Error("store github binding failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to connect repository")
		return
	}

	if _, err := s.Apps.UpdateApplication(ctx, app.ID, store.ApplicationInput{
		Name:          app.Name,
		RepositoryURL: store.GitHubCloneURL(repo),
		RootDirectory: app.RootDirectory,
		HealthPath:    app.HealthPath,
		LocalHost:     app.LocalHost,
		BuildPackage:  app.BuildPackage,
	}); err != nil {
		s.logger().Error("update application repo url failed", "err", err)
	}

	s.audit(r, "", "github.repo.connect", "application", app.ID, map[string]string{
		"repo": repo, "branch": branch, "environment": env,
	})
	resp := applicationGitHubResponse{
		Connected:   true,
		Repo:        binding.RepoFullName,
		Branch:      binding.Branch,
		Environment: binding.Environment,
		Webhook:     binding.WebhookID > 0,
	}
	if !resp.Webhook {
		resp.Warning = localWebhookWarning()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) deleteApplicationGitHub(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	actor, _ := ActorFrom(r.Context())
	if s.GitHubAccounts == nil {
		writeError(w, http.StatusInternalServerError, "github is not configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	b, err := s.GitHubAccounts.GetApplicationGitHub(ctx, app.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "repository is not connected")
		return
	}
	if err != nil {
		s.logger().Error("get application github failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to disconnect repository")
		return
	}
	if acc, accErr := s.GitHubAccounts.GetGitHubAccount(ctx, actor.UserID); accErr == nil && s.GitHub != nil {
		if err := s.GitHub.DeleteWebhook(ctx, acc.AccessToken, b.RepoFullName, b.WebhookID); err != nil {
			s.logger().Error("delete github webhook failed", "err", err)
		}
	}
	if err := s.GitHubAccounts.DeleteApplicationGitHub(ctx, app.ID); err != nil {
		s.logger().Error("delete github binding failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to disconnect repository")
		return
	}
	s.audit(r, "", "github.repo.disconnect", "application", app.ID, map[string]string{"repo": b.RepoFullName})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) repoVisible(ctx context.Context, token, fullName string) bool {
	repos, err := s.githubAPI().ListRepos(ctx, token)
	if err != nil {
		return false
	}
	want := strings.ToLower(fullName)
	for _, repo := range repos {
		if strings.EqualFold(repo.FullName, want) || strings.ToLower(repo.FullName) == want {
			return true
		}
	}
	return false
}
