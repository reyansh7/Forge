package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/reyansh7/Forge/internal/detect"
	"github.com/reyansh7/Forge/internal/githubapi"
	"github.com/reyansh7/Forge/internal/store"
)

type memGitHub struct {
	mu         sync.Mutex
	account    *store.GitHubAccount
	bindings   map[string]store.ApplicationGitHub
	states     map[string]struct{ userID, returnTo string }
	deliveries map[string]store.DeliveryRow
}

func newMemGitHub() *memGitHub {
	return &memGitHub{
		bindings:   map[string]store.ApplicationGitHub{},
		states:     map[string]struct{ userID, returnTo string }{},
		deliveries: map[string]store.DeliveryRow{},
	}
}

func (m *memGitHub) UpsertGitHubAccount(_ context.Context, userID string, githubUserID int64, login, token string) (store.GitHubAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := store.GitHubAccount{UserID: userID, GitHubUserID: githubUserID, Login: login, AccessToken: token}
	m.account = &a
	return a, nil
}

func (m *memGitHub) GetGitHubAccount(_ context.Context, userID string) (store.GitHubAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.account == nil || m.account.UserID != userID {
		return store.GitHubAccount{}, store.ErrNotFound
	}
	return *m.account, nil
}

func (m *memGitHub) GetGitHubAccountByGitHubUserID(_ context.Context, githubUserID int64) (store.GitHubAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.account == nil || m.account.GitHubUserID != githubUserID {
		return store.GitHubAccount{}, store.ErrNotFound
	}
	return *m.account, nil
}

func (m *memGitHub) DeleteGitHubAccount(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.account == nil || m.account.UserID != userID {
		return store.ErrNotFound
	}
	m.account = nil
	return nil
}

func (m *memGitHub) CreateOAuthState(_ context.Context, userID, nonce, returnTo string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[nonce] = struct{ userID, returnTo string }{userID, returnTo}
	return nil
}

func (m *memGitHub) CreateLoginOAuthState(_ context.Context, nonce, returnTo string) error {
	return m.CreateOAuthState(context.Background(), "", nonce, returnTo)
}

func (m *memGitHub) ConsumeOAuthState(_ context.Context, nonce string) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.states[nonce]
	if !ok {
		return "", "", store.ErrNotFound
	}
	delete(m.states, nonce)
	return s.userID, s.returnTo, nil
}

func (m *memGitHub) UpsertApplicationGitHub(_ context.Context, in store.ApplicationGitHub) (store.ApplicationGitHub, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.bindings {
		if strings.EqualFold(b.RepoFullName, in.RepoFullName) && strings.EqualFold(b.Branch, in.Branch) && b.ApplicationID != in.ApplicationID {
			return store.ApplicationGitHub{}, store.ErrConflict
		}
	}
	m.bindings[in.ApplicationID] = in
	return in, nil
}

func (m *memGitHub) GetApplicationGitHub(_ context.Context, applicationID string) (store.ApplicationGitHub, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.bindings[applicationID]
	if !ok {
		return store.ApplicationGitHub{}, store.ErrNotFound
	}
	return b, nil
}

func (m *memGitHub) GetApplicationGitHubByRepoBranch(_ context.Context, repo, branch string) (store.ApplicationGitHub, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.bindings {
		if strings.EqualFold(b.RepoFullName, repo) && strings.EqualFold(b.Branch, branch) {
			return b, nil
		}
	}
	return store.ApplicationGitHub{}, store.ErrNotFound
}

func (m *memGitHub) ListApplicationGitHubByOwner(_ context.Context, ownerID string) ([]store.ApplicationGitHub, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.ApplicationGitHub, 0)
	for _, b := range m.bindings {
		if b.OwnerID == ownerID {
			out = append(out, b)
		}
	}
	return out, nil
}

func (m *memGitHub) DeleteApplicationGitHub(_ context.Context, applicationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.bindings[applicationID]; !ok {
		return store.ErrNotFound
	}
	delete(m.bindings, applicationID)
	return nil
}

func (m *memGitHub) DeleteApplicationGitHubByOwner(_ context.Context, ownerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, b := range m.bindings {
		if b.OwnerID == ownerID {
			delete(m.bindings, id)
		}
	}
	return nil
}

func (m *memGitHub) BeginWebhookDelivery(_ context.Context, deliveryID string) (store.WebhookLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, next := store.StepWebhookDelivery(time.Now().UTC(), m.deliveries[deliveryID])
	m.deliveries[deliveryID] = next
	return lease, nil
}

func (m *memGitHub) FinishWebhookDelivery(_ context.Context, deliveryID string, attempt int, state, deploymentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.deliveries[deliveryID]
	if !ok || row.Attempt != attempt || row.State != store.DeliveryPending {
		return store.ErrNotFound
	}
	if state != store.DeliveryQueued && state != store.DeliveryFailed {
		return store.ErrNotFound
	}
	row.State = state
	row.DeploymentID = deploymentID
	row.UpdatedAt = time.Now().UTC()
	m.deliveries[deliveryID] = row
	return nil
}

type stubGitHubAPI struct {
	token  string
	user   githubapi.User
	repos  []githubapi.Repo
	hookID int64
}

func (s *stubGitHubAPI) Exchange(context.Context, string, string) (string, error) {
	return s.token, nil
}
func (s *stubGitHubAPI) User(context.Context, string) (githubapi.User, error) { return s.user, nil }
func (s *stubGitHubAPI) ListRepos(context.Context, string) ([]githubapi.Repo, error) {
	return s.repos, nil
}
func (s *stubGitHubAPI) CreatePushWebhook(context.Context, string, string, string, string) (int64, error) {
	if s.hookID == 0 {
		return 7, nil
	}
	return s.hookID, nil
}
func (s *stubGitHubAPI) DeleteWebhook(context.Context, string, string, int64) error { return nil }

func githubAppFixture(t *testing.T) (*Server, *memGitHub, store.Application) {
	t.Helper()
	mem := newMemProjects()
	p, err := mem.CreateProject(context.Background(), testUserID, store.ProjectInput{
		Name: "demo", RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	apps := newMemApps()
	app, err := apps.CreateApplication(context.Background(), p.ID, store.ApplicationInput{
		Name: "api", RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	gh := newMemGitHub()
	srv := appServer(mem, apps, newMemDeployments())
	srv.GitHubAccounts = gh
	srv.GitHubClientID = "client"
	srv.GitHubClientSecret = "secret"
	srv.GitHub = &stubGitHubAPI{
		token:  "gho_test_token_never_in_json",
		user:   githubapi.User{ID: 1, Login: "octocat"},
		repos:  []githubapi.Repo{{FullName: "octo/hello", DefaultBranch: "main"}},
		hookID: 11,
	}
	srv.PublicBase = "http://127.0.0.1:3000"
	srv.WebhookBase = "http://127.0.0.1:8080"
	srv.GitHubAllowedUserIDs = []int64{1}
	return srv, gh, app
}

func TestGitHubStatusOmitsToken(t *testing.T) {
	srv, gh, _ := githubAppFixture(t)
	_, err := gh.UpsertGitHubAccount(context.Background(), testUserID, 1, "octocat", "gho_test_token_never_in_json")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/integrations/github/status", nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "gho_test_token_never_in_json") {
		t.Fatal("token leaked")
	}
	var body githubStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Connected || body.Login != "octocat" {
		t.Fatalf("%+v", body)
	}
}

func TestGitHubStartRedirects(t *testing.T) {
	srv, _, app := githubAppFixture(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/integrations/github/start?return_to=/applications/"+app.ID, nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "github.com/login/oauth/authorize") || !strings.Contains(loc, "client_id=client") {
		t.Fatalf("location %q", loc)
	}
}

func TestConnectRepoCreatesWebhookAndHidesSecret(t *testing.T) {
	srv, gh, app := githubAppFixture(t)
	srv.WebhookBase = "https://hooks.example.com"
	_, err := gh.UpsertGitHubAccount(context.Background(), testUserID, 1, "octocat", "gho_test_token_never_in_json")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	body := `{"repo":"octo/hello","branch":"main","environment":"production"}`
	req := httptest.NewRequest(http.MethodPut, "/applications/"+app.ID+"/github", bytes.NewReader([]byte(body)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "gho_") || strings.Contains(rec.Body.String(), "webhook_secret") {
		t.Fatal("secret leaked")
	}
	b, err := gh.GetApplicationGitHub(context.Background(), app.ID)
	if err != nil || b.RepoFullName != "octo/hello" || b.WebhookID != 11 {
		t.Fatalf("binding %+v err=%v", b, err)
	}
}

func TestConnectRepoSkipsLoopbackWebhook(t *testing.T) {
	srv, gh, app := githubAppFixture(t)
	_, err := gh.UpsertGitHubAccount(context.Background(), testUserID, 1, "octocat", "gho_test_token_never_in_json")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	body := `{"repo":"octo/hello","branch":"main","environment":"production"}`
	req := httptest.NewRequest(http.MethodPut, "/applications/"+app.ID+"/github", bytes.NewReader([]byte(body)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Press Deploy") {
		t.Fatalf("body %s", rec.Body.String())
	}
	b, err := gh.GetApplicationGitHub(context.Background(), app.ID)
	if err != nil || b.RepoFullName != "octo/hello" || b.WebhookID != 0 {
		t.Fatalf("binding %+v err=%v", b, err)
	}
}

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	srv, gh, app := githubAppFixture(t)
	_, _ = gh.UpsertApplicationGitHub(context.Background(), store.ApplicationGitHub{
		ApplicationID: app.ID,
		OwnerID:       testUserID,
		RepoFullName:  "octo/hello",
		Branch:        "main",
		Environment:   store.EnvProduction,
		WebhookSecret: "hook-secret",
	})
	body := []byte(`{"ref":"refs/heads/main","after":"abc1234","repository":{"full_name":"octo/hello"}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github/"+app.ID, bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "11111111-1111-1111-1111-111111111111")
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWebhookPushEnqueuesAndDedups(t *testing.T) {
	srv, gh, app := githubAppFixture(t)
	_, _ = gh.UpsertApplicationGitHub(context.Background(), store.ApplicationGitHub{
		ApplicationID: app.ID,
		OwnerID:       testUserID,
		RepoFullName:  "octo/hello",
		Branch:        "main",
		Environment:   store.EnvProduction,
		WebhookSecret: "hook-secret",
	})
	body := []byte(`{"ref":"refs/heads/main","after":"abc1234dead","repository":{"full_name":"octo/hello"},"head_commit":{"id":"abc1234dead","message":"ship it","author":{"name":"Ada"}}}`)
	sig := signBody("hook-secret", body)

	post := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/webhooks/github/"+app.ID, bytes.NewReader(body))
		req.Header.Set("X-GitHub-Event", "push")
		req.Header.Set("X-GitHub-Delivery", "22222222-2222-2222-2222-222222222222")
		req.Header.Set("X-Hub-Signature-256", sig)
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}
	first := post()
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d body=%s", first.Code, first.Body.String())
	}
	if !strings.Contains(first.Body.String(), "abc1234dead") || !strings.Contains(first.Body.String(), "Ada") {
		t.Fatalf("commit missing: %s", first.Body.String())
	}
	second := post()
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), "duplicate") {
		t.Fatalf("dup status = %d body=%s", second.Code, second.Body.String())
	}
}

func TestWebhookWrongBranchIgnored(t *testing.T) {
	srv, gh, app := githubAppFixture(t)
	_, _ = gh.UpsertApplicationGitHub(context.Background(), store.ApplicationGitHub{
		ApplicationID: app.ID,
		OwnerID:       testUserID,
		RepoFullName:  "octo/hello",
		Branch:        "main",
		Environment:   store.EnvProduction,
		WebhookSecret: "hook-secret",
	})
	body := []byte(`{"ref":"refs/heads/dev","after":"abc1234dead","repository":{"full_name":"octo/hello"}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github/"+app.ID, bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "33333333-3333-3333-3333-333333333333")
	req.Header.Set("X-Hub-Signature-256", signBody("hook-secret", body))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "branch") {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWebhookIsPublic(t *testing.T) {
	srv, gh, app := githubAppFixture(t)
	_, _ = gh.UpsertApplicationGitHub(context.Background(), store.ApplicationGitHub{
		ApplicationID: app.ID,
		OwnerID:       testUserID,
		RepoFullName:  "octo/hello",
		Branch:        "main",
		Environment:   store.EnvProduction,
		WebhookSecret: "hook-secret",
	})
	body := []byte(`{"zen":"ok"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github/"+app.ID, bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("X-GitHub-Delivery", "44444444-4444-4444-4444-444444444444")
	req.Header.Set("X-Hub-Signature-256", signBody("hook-secret", body))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ping without session: %d %s", rec.Code, rec.Body.String())
	}
}

func TestGitHubLoginStartIsPublic(t *testing.T) {
	srv, gh, _ := githubAppFixture(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/github", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "github.com/login/oauth/authorize") {
		t.Fatalf("location %q", loc)
	}
	var state string
	for _, c := range rec.Result().Cookies() {
		if c.Name == githubStateCookie {
			state = c.Value
		}
	}
	if len(state) < 16 {
		t.Fatal("missing state cookie")
	}
	if _, _, err := gh.ConsumeOAuthState(context.Background(), state); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubLoginCallbackCreatesSession(t *testing.T) {
	srv, gh, _ := githubAppFixture(t)
	if err := gh.CreateLoginOAuthState(context.Background(), "0123456789abcdef0123456789abcdef", "/"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/integrations/github/callback?code=abc&state=0123456789abcdef0123456789abcdef", nil)
	req.AddCookie(&http.Cookie{Name: githubStateCookie, Value: "0123456789abcdef0123456789abcdef"})
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || !strings.HasSuffix(rec.Header().Get("Location"), "/") {
		t.Fatalf("status = %d loc=%s", rec.Code, rec.Header().Get("Location"))
	}
	var session string
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			session = c.Value
		}
	}
	if session == "" {
		t.Fatal("no session cookie")
	}
	acc, err := gh.GetGitHubAccountByGitHubUserID(context.Background(), 1)
	if err != nil || acc.Login != "octocat" || acc.AccessToken == "" {
		t.Fatalf("account %+v err=%v", acc, err)
	}
	if acc.UserID == testUserID {
		t.Fatal("new github identity attached to the existing password operator")
	}
}

func TestGitHubLoginRejectsUnlistedAccount(t *testing.T) {
	srv, gh, _ := githubAppFixture(t)
	srv.GitHubAllowedUserIDs = nil
	if err := gh.CreateLoginOAuthState(context.Background(), "fedcba9876543210fedcba9876543210", "/"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/integrations/github/callback?code=abc&state=fedcba9876543210fedcba9876543210", nil)
	req.AddCookie(&http.Cookie{Name: githubStateCookie, Value: "fedcba9876543210fedcba9876543210"})
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "github=not_allowed") {
		t.Fatalf("status = %d loc=%s", rec.Code, rec.Header().Get("Location"))
	}
	if _, err := gh.GetGitHubAccountByGitHubUserID(context.Background(), 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unlisted account stored: %v", err)
	}
}

func TestGitHubLoginRejectsMissingStateCookie(t *testing.T) {
	srv, gh, _ := githubAppFixture(t)
	if err := gh.CreateLoginOAuthState(context.Background(), "0123456789abcdef0123456789abcdef", "/"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/integrations/github/callback?code=abc&state=0123456789abcdef0123456789abcdef", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "github=invalid_state") {
		t.Fatalf("status = %d loc=%s", rec.Code, rec.Header().Get("Location"))
	}
	if _, err := gh.GetGitHubAccountByGitHubUserID(context.Background(), 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("account stored without cookie: %v", err)
	}
}

func TestGitHubLoginReusesLinkedAccount(t *testing.T) {
	srv, gh, _ := githubAppFixture(t)
	if _, err := gh.UpsertGitHubAccount(context.Background(), testUserID, 1, "octocat", "old-token"); err != nil {
		t.Fatal(err)
	}
	if err := gh.CreateLoginOAuthState(context.Background(), "abcdef0123456789abcdef0123456789", "/"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/integrations/github/callback?code=abc&state=abcdef0123456789abcdef0123456789", nil)
	req.AddCookie(&http.Cookie{Name: githubStateCookie, Value: "abcdef0123456789abcdef0123456789"})
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d loc=%s", rec.Code, rec.Header().Get("Location"))
	}
	acc, err := gh.GetGitHubAccountByGitHubUserID(context.Background(), 1)
	if err != nil || acc.UserID != testUserID || acc.AccessToken != "gho_test_token_never_in_json" {
		t.Fatalf("account %+v err=%v", acc, err)
	}
}

func TestGitHubLoginDoesNotTakePasswordName(t *testing.T) {
	srv, gh, _ := githubAppFixture(t)
	auth := srv.Auth.(*memAuth)
	existing, err := auth.CreateUser(context.Background(), "octocat", "not-a-real-bcrypt-hash!!")
	if err != nil {
		t.Fatal(err)
	}
	if err := gh.CreateLoginOAuthState(context.Background(), "fedcba9876543210fedcba9876543210", "/"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/integrations/github/callback?code=abc&state=fedcba9876543210fedcba9876543210", nil)
	req.AddCookie(&http.Cookie{Name: githubStateCookie, Value: "fedcba9876543210fedcba9876543210"})
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d loc=%s body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	acc, err := gh.GetGitHubAccountByGitHubUserID(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if acc.UserID == existing.ID {
		t.Fatal("github login took the password account with the same name")
	}
}

func TestForgeUsernameFromGitHub(t *testing.T) {
	if got := forgeUsernameFromGitHub("OctoCat"); got != "octocat" {
		t.Fatalf("got %s", got)
	}
	if got := forgeUsernameFromGitHub("1ab"); got != "gh1ab" {
		t.Fatalf("got %s", got)
	}
}

func TestWebhookPushDeploysEveryServiceOnce(t *testing.T) {
	srv, gh, app := githubAppFixture(t)
	jobs := &recJobs{}
	srv.Jobs = jobs
	srv.ProjectDeploys = newMemProjectDeploys()
	srv.Discoverer = func(context.Context, store.Project) ([]detect.Service, error) {
		return []detect.Service{
			{Name: "ml-model", Root: "ml-model", Kind: "python", Port: 8000, Deployable: true, Public: boolPtr(false)},
			{Name: "backend", Root: "backend", Kind: "python", Framework: "fastapi", Port: 8080, Deployable: true, Public: boolPtr(true), DependsOn: []string{"ml-model"}},
			{Name: "frontend", Root: "frontend", Kind: "node", Framework: "next", Port: 3000, Deployable: true, Public: boolPtr(true), DependsOn: []string{"backend"}},
		}, nil
	}
	_, _ = gh.UpsertApplicationGitHub(context.Background(), store.ApplicationGitHub{
		ApplicationID: app.ID,
		OwnerID:       testUserID,
		RepoFullName:  "octo/hello",
		Branch:        "main",
		Environment:   store.EnvProduction,
		WebhookSecret: "hook-secret",
	})
	push := func(delivery, sha string) *httptest.ResponseRecorder {
		body := []byte(`{"ref":"refs/heads/main","after":"` + sha + `","repository":{"full_name":"octo/hello"},"head_commit":{"id":"` + sha + `","message":"ship","author":{"name":"Ada"}}}`)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/webhooks/github/"+app.ID, bytes.NewReader(body))
		req.Header.Set("X-GitHub-Event", "push")
		req.Header.Set("X-GitHub-Delivery", delivery)
		req.Header.Set("X-Hub-Signature-256", signBody("hook-secret", body))
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}
	first := push("55555555-5555-4555-8555-555555555555", "abc1234dead")
	if first.Code != http.StatusAccepted {
		t.Fatalf("first %d %s", first.Code, first.Body.String())
	}
	var view projectDeployResponse
	if err := json.Unmarshal(first.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Services) != 3 || len(jobs.jobs) != 3 {
		t.Fatalf("services %d jobs %d body %s", len(view.Services), len(jobs.jobs), first.Body.String())
	}
	if view.Services[0].Name != "ml-model" || view.Services[2].Name != "frontend" {
		t.Fatalf("order %+v", view.Services)
	}
	dup := push("55555555-5555-4555-8555-555555555555", "abc1234dead")
	if dup.Code != http.StatusOK || !strings.Contains(dup.Body.String(), "duplicate") || len(jobs.jobs) != 3 {
		t.Fatalf("dup %d jobs %d %s", dup.Code, len(jobs.jobs), dup.Body.String())
	}
	deps := srv.Deployments.(*memDeployments)
	deps.mu.Lock()
	for id, d := range deps.byID {
		if d.CommitSHA != "abc1234dead" {
			t.Errorf("commit %s", d.CommitSHA)
		}
		d.Status = store.StatusLive
		deps.byID[id] = d
	}
	deps.mu.Unlock()
	second := push("66666666-6666-4666-8666-666666666666", "def5678beef")
	if second.Code != http.StatusAccepted || len(jobs.jobs) != 6 {
		t.Fatalf("second %d jobs %d %s", second.Code, len(jobs.jobs), second.Body.String())
	}
	var again projectDeployResponse
	if err := json.Unmarshal(second.Body.Bytes(), &again); err != nil {
		t.Fatal(err)
	}
	if again.ID == view.ID || len(again.Services) != 3 {
		t.Fatalf("second release %+v", again)
	}
}

func TestWebhookEnqueueFailureRetriesSameDeployment(t *testing.T) {
	srv, gh, app := githubAppFixture(t)
	jobs := &stubJobs{err: errors.New("redis down")}
	srv.Jobs = jobs
	bindWebhook(t, gh, app.ID)
	body := pushBody("refs/heads/main", "abc1234dead", "octo/hello", false)
	delivery := "77777777-7777-4777-8777-777777777777"

	first := postWebhook(srv, app.ID, delivery, body)
	if first.Code != http.StatusServiceUnavailable {
		t.Fatalf("first status = %d body=%s", first.Code, first.Body.String())
	}
	if n := deploymentCount(t, srv); n != 1 {
		t.Fatalf("deployments after failure = %d", n)
	}

	jobs.err = nil
	second := postWebhook(srv, app.ID, delivery, body)
	if second.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d body=%s", second.Code, second.Body.String())
	}
	if n := deploymentCount(t, srv); n != 1 {
		t.Fatalf("retry created another deployment: %d", n)
	}
	if jobs.last.Type != "deploy" {
		t.Fatalf("job type %s", jobs.last.Type)
	}

	third := postWebhook(srv, app.ID, delivery, body)
	if third.Code != http.StatusOK || !strings.Contains(third.Body.String(), "duplicate") {
		t.Fatalf("third status = %d body=%s", third.Code, third.Body.String())
	}
	if n := deploymentCount(t, srv); n != 1 {
		t.Fatalf("duplicate created a deployment: %d", n)
	}
}

func TestWebhookIgnoredEventsDoNotConsumeDelivery(t *testing.T) {
	srv, gh, app := githubAppFixture(t)
	bindWebhook(t, gh, app.ID)
	delivery := "88888888-8888-4888-8888-888888888888"

	tag := postWebhook(srv, app.ID, delivery, pushBody("refs/tags/v1", "abc1234dead", "octo/hello", false))
	if tag.Code != http.StatusOK || !strings.Contains(tag.Body.String(), "branch") {
		t.Fatalf("tag status = %d body=%s", tag.Code, tag.Body.String())
	}
	deleted := postWebhook(srv, app.ID, delivery, pushBody("refs/heads/main", "0000000", "octo/hello", true))
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), "branch") {
		t.Fatalf("deleted status = %d body=%s", deleted.Code, deleted.Body.String())
	}
	other := postWebhook(srv, app.ID, delivery, pushBody("refs/heads/main", "abc1234dead", "octo/other", false))
	if other.Code != http.StatusOK || !strings.Contains(other.Body.String(), "repo") {
		t.Fatalf("repo status = %d body=%s", other.Code, other.Body.String())
	}
	ok := postWebhook(srv, app.ID, delivery, pushBody("refs/heads/main", "abc1234dead", "octo/hello", false))
	if ok.Code != http.StatusAccepted {
		t.Fatalf("push after ignores = %d body=%s", ok.Code, ok.Body.String())
	}
	if n := deploymentCount(t, srv); n != 1 {
		t.Fatalf("deployments = %d", n)
	}
}

func bindWebhook(t *testing.T, gh *memGitHub, appID string) {
	t.Helper()
	_, err := gh.UpsertApplicationGitHub(context.Background(), store.ApplicationGitHub{
		ApplicationID: appID,
		OwnerID:       testUserID,
		RepoFullName:  "octo/hello",
		Branch:        "main",
		Environment:   store.EnvProduction,
		WebhookSecret: "hook-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func pushBody(ref, sha, repo string, deleted bool) []byte {
	raw := map[string]any{
		"ref": ref, "after": sha, "deleted": deleted,
		"repository":  map[string]string{"full_name": repo},
		"head_commit": map[string]any{"id": sha, "message": "ship it", "author": map[string]string{"name": "Ada"}},
	}
	body, err := json.Marshal(raw)
	if err != nil {
		panic(err)
	}
	return body
}

func postWebhook(srv *Server, appID, delivery string, body []byte) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github/"+appID, bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", delivery)
	req.Header.Set("X-Hub-Signature-256", signBody("hook-secret", body))
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func deploymentCount(t *testing.T, srv *Server) int {
	t.Helper()
	deps := srv.Deployments.(*memDeployments)
	deps.mu.Lock()
	defer deps.mu.Unlock()
	return len(deps.byID)
}

func boolPtr(v bool) *bool { return &v }

func TestManualDeployStillWorks(t *testing.T) {
	srv, _, app := githubAppFixture(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/applications/"+app.ID+"/deployments", bytes.NewReader([]byte(`{}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}
