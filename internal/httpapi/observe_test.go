package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/reyansh7/Forge/internal/store"
)

func TestMetricsRequiresAuth(t *testing.T) {
	srv := appServer(newMemProjects(), newMemApps(), newMemDeployments())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMetricsReturnsOwnerCounts(t *testing.T) {
	mem := newMemProjects()
	p, err := mem.CreateProject(context.Background(), testUserID, store.ProjectInput{
		Name:          "demo",
		RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	apps := newMemApps()
	app, err := apps.CreateApplication(context.Background(), p.ID, store.ApplicationInput{
		Name:          "app",
		RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	deps := newMemDeployments()
	d, err := deps.CreateDeployment(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.ProjectID = p.ID
	d.Status = store.StatusFailed
	d.UpdatedAt = d.CreatedAt.Add(2 * time.Second)
	if err := deps.UpdateDeployment(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	srv := appServer(mem, apps, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body metricsResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.DeploymentsTotal != 1 || body.DeploymentsFailed != 1 {
		t.Fatalf("%+v", body)
	}
	if body.LastDeployDurationMS != 2000 {
		t.Fatalf("duration = %d", body.LastDeployDurationMS)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("expected request id")
	}
}

func TestLogStreamRequiresAuth(t *testing.T) {
	srv := appServer(newMemProjects(), newMemApps(), newMemDeployments())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/applications/00000000-0000-4000-8000-000000000099/logs/stream", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestLogStreamNeedsLive(t *testing.T) {
	mem := newMemProjects()
	p, err := mem.CreateProject(context.Background(), testUserID, store.ProjectInput{
		Name:          "demo",
		RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	apps := newMemApps()
	app, err := apps.CreateApplication(context.Background(), p.ID, store.ApplicationInput{
		Name:          "app",
		RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := appServer(mem, apps, newMemDeployments())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/applications/"+app.ID+"/logs/stream", nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestLogStreamOtherOperatorIs404(t *testing.T) {
	mem := newMemProjects()
	p, err := mem.CreateProject(context.Background(), testUserID, store.ProjectInput{
		Name:          "demo",
		RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	apps := newMemApps()
	app, err := apps.CreateApplication(context.Background(), p.ID, store.ApplicationInput{
		Name:          "app",
		RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := appServer(mem, apps, newMemDeployments())
	rec := serveOther(srv, httptest.NewRequest(http.MethodGet, "/applications/"+app.ID+"/logs/stream", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestLogStreamWritesSSE(t *testing.T) {
	mem := newMemProjects()
	p, err := mem.CreateProject(context.Background(), testUserID, store.ProjectInput{
		Name:          "demo",
		RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	apps := newMemApps()
	app, err := apps.CreateApplication(context.Background(), p.ID, store.ApplicationInput{
		Name:          "app",
		RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	deps := newMemDeployments()
	d, err := deps.CreateDeployment(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.Status = store.StatusLive
	if err := deps.UpdateDeployment(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	srv := appServer(mem, apps, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/applications/"+app.ID+"/logs/stream?tail=20", nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "hello from container") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}
