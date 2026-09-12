package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/reyansh7/Forge/internal/store"
)

func TestUnauthenticatedMutatingCallFails(t *testing.T) {
	// Roadmap 3.a: a client without a session cannot create projects.
	srv := projectServer(newMemProjects())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects", bytes.NewReader(
		[]byte(`{"name":"x","repository_url":"forge://hello"}`),
	))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHealthRemainsPublic(t *testing.T) {
	srv := projectServer(newMemProjects())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestAuthStatusWhenEmpty(t *testing.T) {
	srv := &Server{
		Postgres: stubPing{},
		Redis:    stubPing{},
		Auth:     newMemAuth(),
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/status", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body authStatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.BootstrapRequired {
		t.Fatal("expected bootstrap_required")
	}
}

func TestBootstrapThenMe(t *testing.T) {
	auth := newMemAuth()
	srv := &Server{
		Postgres: stubPing{},
		Redis:    stubPing{},
		Auth:     auth,
	}
	body := []byte(`{"username":"reyansh","password":"long-enough-password"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/bootstrap", bytes.NewReader(body))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var sess authSessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&sess); err != nil {
		t.Fatal(err)
	}
	if sess.Token == "" || sess.User.Username != "reyansh" {
		t.Fatalf("%+v", sess)
	}
	if rec.Header().Get("Set-Cookie") == "" {
		t.Fatal("expected session cookie")
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req2.Header.Set("Authorization", "Bearer "+sess.Token)
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("me status = %d body=%s", rec2.Code, rec2.Body.String())
	}

	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/auth/bootstrap", bytes.NewReader(body))
	srv.Handler().ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusConflict {
		t.Fatalf("second bootstrap = %d", rec3.Code)
	}
}

func TestSignupRejectsMismatchedPasswords(t *testing.T) {
	srv := &Server{Postgres: stubPing{}, Redis: stubPing{}, Auth: newMemAuth()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(
		[]byte(`{"username":"newbie","password":"long-enough-password","password_confirm":"different-password"}`),
	))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSignupThroughDashboardPrefixIsPublic(t *testing.T) {
	// The browser posts /forge-api/auth/signup. That must not require a
	// session even if the rewrite leaves the prefix on the request.
	srv := &Server{Postgres: stubPing{}, Redis: stubPing{}, Auth: newMemAuth()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/forge-api/auth/signup", bytes.NewReader(
		[]byte(`{"username":"newbie","password":"long-enough-password","password_confirm":"long-enough-password"}`),
	))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSignupCreatesAccountAndSession(t *testing.T) {
	auth := newMemAuth()
	srv := &Server{Postgres: stubPing{}, Redis: stubPing{}, Auth: auth}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(
		[]byte(`{"username":"newbie","password":"long-enough-password","password_confirm":"long-enough-password"}`),
	))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var sess authSessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&sess); err != nil {
		t.Fatal(err)
	}
	if sess.User.Username != "newbie" || sess.Token == "" {
		t.Fatalf("%+v", sess)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(
		[]byte(`{"username":"newbie","password":"long-enough-password","password_confirm":"long-enough-password"}`),
	))
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("duplicate = %d", rec2.Code)
	}
}

func TestLoginRejectsBadPassword(t *testing.T) {
	auth := newMemAuth()
	hash, err := store.HashPassword("long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.CreateFirstUser(nil, "operator", hash); err != nil {
		t.Fatal(err)
	}
	srv := &Server{Postgres: stubPing{}, Redis: stubPing{}, Auth: auth}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(
		[]byte(`{"username":"operator","password":"wrong-password-x"}`),
	))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(
		[]byte(`{"username":"nobody","password":"wrong-password-x"}`),
	))
	srv.Handler().ServeHTTP(rec2, req2)
	if rec.Body.String() != rec2.Body.String() {
		t.Fatalf("unknown user and bad password must look the same\n%s\n%s", rec.Body.String(), rec2.Body.String())
	}
}

func TestOtherOperatorCannotReadProject(t *testing.T) {
	mem := newMemProjects()
	srv := projectServer(mem)
	created, err := mem.CreateProject(nil, testUserID, store.ProjectInput{
		Name:          "secret",
		RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := serveOther(srv, httptest.NewRequest(http.MethodGet, "/projects/"+created.ID, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	recList := serveOther(srv, httptest.NewRequest(http.MethodGet, "/projects", nil))
	if recList.Code != http.StatusOK {
		t.Fatalf("list status = %d", recList.Code)
	}
	var list []projectResponse
	if err := json.NewDecoder(recList.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("other operator listed %#v", list)
	}
}

func TestEachOperatorOnlySeesOwnCreatedProjects(t *testing.T) {
	// Invariant: a project created while logged in as A is stored on A's
	// account (owner_id = A's user id). B's GET /projects must not
	// include it, and A's list must not include a project B created.
	mem := newMemProjects()
	srv := projectServer(mem)

	recA := httptest.NewRecorder()
	reqA := httptest.NewRequest(http.MethodPost, "/projects", bytes.NewReader(
		[]byte(`{"name":"alpha","repository_url":"forge://hello"}`),
	))
	testHandler(srv).ServeHTTP(recA, reqA)
	if recA.Code != http.StatusCreated {
		t.Fatalf("A create = %d body=%s", recA.Code, recA.Body.String())
	}
	var aProject projectResponse
	if err := json.NewDecoder(recA.Body).Decode(&aProject); err != nil {
		t.Fatal(err)
	}

	recB := serveOther(srv, httptest.NewRequest(http.MethodPost, "/projects", bytes.NewReader(
		[]byte(`{"name":"beta","repository_url":"forge://hello"}`),
	)))
	if recB.Code != http.StatusCreated {
		t.Fatalf("B create = %d body=%s", recB.Code, recB.Body.String())
	}
	var bProject projectResponse
	if err := json.NewDecoder(recB.Body).Decode(&bProject); err != nil {
		t.Fatal(err)
	}
	if aProject.ID == bProject.ID {
		t.Fatal("operators must not share a project id")
	}

	recAList := httptest.NewRecorder()
	reqAList := httptest.NewRequest(http.MethodGet, "/projects", nil)
	testHandler(srv).ServeHTTP(recAList, reqAList)
	var aList []projectResponse
	if err := json.NewDecoder(recAList.Body).Decode(&aList); err != nil {
		t.Fatal(err)
	}
	if !projectIDsEqual(aList, []string{aProject.ID}) {
		t.Fatalf("A list = %#v, want only %s", aList, aProject.ID)
	}

	recBList := serveOther(srv, httptest.NewRequest(http.MethodGet, "/projects", nil))
	var bList []projectResponse
	if err := json.NewDecoder(recBList.Body).Decode(&bList); err != nil {
		t.Fatal(err)
	}
	if !projectIDsEqual(bList, []string{bProject.ID}) {
		t.Fatalf("B list = %#v, want only %s", bList, bProject.ID)
	}

	stored, err := mem.GetProject(nil, aProject.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.OwnerID != testUserID {
		t.Fatalf("A project owner = %q, want session user", stored.OwnerID)
	}
	storedB, err := mem.GetProject(nil, bProject.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedB.OwnerID != testOtherUserID {
		t.Fatalf("B project owner = %q, want other session user", storedB.OwnerID)
	}
}

func projectIDsEqual(got []projectResponse, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, p := range got {
		seen[p.ID] = true
	}
	for _, id := range want {
		if !seen[id] {
			return false
		}
	}
	return true
}

func TestLoginRevokesPreviousSession(t *testing.T) {
	// Safety: signing in again on the same browser must kill the
	// previous cookie/token so a later request cannot still act as
	// the last operator.
	auth := newMemAuth()
	srv := &Server{Postgres: stubPing{}, Redis: stubPing{}, Auth: auth}
	signupBody := []byte(`{"username":"newbie","password":"long-enough-password","password_confirm":"long-enough-password"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(signupBody))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup = %d body=%s", rec.Code, rec.Body.String())
	}
	var first authSessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(
		[]byte(`{"username":"newbie","password":"long-enough-password"}`),
	))
	req2.AddCookie(&http.Cookie{Name: sessionCookieName, Value: first.Token})
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("login = %d body=%s", rec2.Code, rec2.Body.String())
	}
	var second authSessionResponse
	if err := json.NewDecoder(rec2.Body).Decode(&second); err != nil {
		t.Fatal(err)
	}
	if second.Token == "" || second.Token == first.Token {
		t.Fatalf("expected a new session, got %+v", second)
	}

	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req3.Header.Set("Authorization", "Bearer "+first.Token)
	srv.Handler().ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusUnauthorized {
		t.Fatalf("old token status = %d, want 401", rec3.Code)
	}

	rec4 := httptest.NewRecorder()
	req4 := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req4.Header.Set("Authorization", "Bearer "+second.Token)
	srv.Handler().ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusOK {
		t.Fatalf("new token status = %d body=%s", rec4.Code, rec4.Body.String())
	}
}

func TestSessionCookieWinsOverStaleBearer(t *testing.T) {
	// The dashboard used to send a leftover Bearer after a new cookie
	// was set. Cookie must win so the browser is the operator who
	// just signed in, not the previous one.
	srv := withAuth(&Server{Postgres: stubPing{}, Redis: stubPing{}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+testSessionToken)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: testOtherToken})
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var me authUserResponse
	if err := json.NewDecoder(rec.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	if me.Username != "other" {
		t.Fatalf("me = %+v, want cookie user other", me)
	}
}

func TestLoginRateLimit(t *testing.T) {
	auth := newMemAuth()
	srv := &Server{
		Postgres:  stubPing{},
		Redis:     stubPing{},
		Auth:      auth,
		loginGate: newAttemptGate(3, time.Minute),
	}
	body := []byte(`{"username":"x","password":"long-enough"}`)
	var last int
	for i := 0; i < 4; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
		req.RemoteAddr = "192.0.2.9:1"
		srv.Handler().ServeHTTP(rec, req)
		last = rec.Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("fourth attempt status = %d, want 429", last)
	}
}

func TestEnvAuditDoesNotRecordValue(t *testing.T) {
	mem := newMemProjects()
	p, err := mem.CreateProject(nil, testUserID, store.ProjectInput{
		Name:          "demo",
		RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	apps := newMemApps()
	app, err := apps.CreateApplication(nil, p.ID, store.ApplicationInput{
		Name:          store.DefaultApplicationName,
		RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	auth := populatedAuth()
	srv := withAuth(appServer(mem, apps, newMemDeployments()))
	srv.Auth = auth

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/applications/"+app.ID+"/env", bytes.NewReader(
		[]byte(`{"key":"API_TOKEN","value":"super-secret-value"}`),
	))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	auth.mu.Lock()
	defer auth.mu.Unlock()
	found := false
	for _, e := range auth.audits {
		if e.Action == "env.put" {
			found = true
			if e.Metadata["key"] != "API_TOKEN" {
				t.Fatalf("metadata = %#v", e.Metadata)
			}
			for _, v := range e.Metadata {
				if v == "super-secret-value" {
					t.Fatal("env value must not appear in audit metadata")
				}
			}
		}
	}
	if !found {
		t.Fatal("expected env.put audit")
	}
}
