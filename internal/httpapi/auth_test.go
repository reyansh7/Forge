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
