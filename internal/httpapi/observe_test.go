package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/reyansh7/Forge/internal/observe"
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
	if body.CounterScope != "api_process" || body.StartedAt.IsZero() || body.CollectedAt.IsZero() {
		t.Fatalf("window not reported: %+v", body.Snapshot)
	}
	if body.QueueDepth != "unavailable" {
		t.Fatalf("queue depth = %q", body.QueueDepth)
	}
	if len(body.Services) != 1 {
		t.Fatalf("services = %+v", body.Services)
	}
	svc := body.Services[0]
	if svc.Health != observe.HealthUnhealthy || svc.Container != string(observe.ProbeNotProbed) {
		t.Fatalf("service = %+v", svc)
	}
	if strings.Contains(svc.HealthReason, "not running") {
		t.Fatalf("failed attempt described as not running: %s", svc.HealthReason)
	}
	if svc.RestartCount != "unavailable" || svc.Resources != "unavailable" || svc.Uptime != "unavailable" {
		t.Fatalf("invented measurement: %+v", svc)
	}
	if body.ApplicationsUnique != 1 || body.DeploymentsTotal != body.Services[0].Attempts {
		t.Fatalf("attempts %d unique %d", body.DeploymentsTotal, body.ApplicationsUnique)
	}
}

func TestMetricsProbeErrorIsNotStopped(t *testing.T) {
	mem, apps, deps, app := metricsFixture(t)
	d := liveDeployment(t, deps, app)
	srv := appServer(mem, apps, deps)
	srv.Runtime.(*stubRuntime).probeErr = errors.New("docker unavailable")
	body := getMetricsOK(t, srv)
	if body.ContainersChecked != 1 || body.ContainersRunning != 0 {
		t.Fatalf("probe counts = %+v", body)
	}
	if len(body.Services) != 1 {
		t.Fatalf("services = %+v", body.Services)
	}
	svc := body.Services[0]
	if svc.Health != observe.HealthUnknown || svc.Container != string(observe.ProbeError) {
		t.Fatalf("service = %+v", svc)
	}
	if strings.Contains(strings.ToLower(svc.HealthReason), "not running") {
		t.Fatalf("probe error treated as stopped: %s", svc.HealthReason)
	}
	if svc.ReleaseID != d.ID || svc.ReleaseStatus != string(store.StatusLive) {
		t.Fatalf("release = %s %s", svc.ReleaseID, svc.ReleaseStatus)
	}
}

func TestMetricsRunningOnDeadWorkerIsDegraded(t *testing.T) {
	mem, apps, deps, app := metricsFixture(t)
	d := liveDeployment(t, deps, app)
	d.NodeID = testNodeID
	if err := deps.UpdateDeployment(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	nodes := newMemNodes()
	nodes.byName["lab"] = store.Node{ID: testNodeID, Name: "lab", Status: store.NodeDead}
	srv := appServer(mem, apps, deps)
	srv.Runtime.(*stubRuntime).running = true
	srv.Nodes = nodes
	body := getMetricsOK(t, srv)
	if body.ContainersRunning != 1 || body.NodesDead != 1 {
		t.Fatalf("counts running=%d dead=%d", body.ContainersRunning, body.NodesDead)
	}
	svc := body.Services[0]
	if svc.Health != observe.HealthDegraded || svc.Container != string(observe.ProbeRunning) {
		t.Fatalf("service = %+v", svc)
	}
}

func TestBuildServiceMetricsSeparatesAttemptsFromProbes(t *testing.T) {
	app := store.Application{ID: "app-1", ProjectID: "proj-1", Name: "web"}
	live := store.Deployment{
		ID: "live-1", ProjectID: "proj-1", ApplicationID: "app-1",
		Status: store.StatusLive, CreatedAt: time.Now().Add(-time.Hour), CommitSHA: "abc",
	}
	failed := store.Deployment{
		ID: "fail-1", ProjectID: "proj-1", ApplicationID: "app-1",
		Status: store.StatusFailed, CreatedAt: time.Now(), ErrorMessage: "health check timed out",
		FailedStage: "health_check",
	}
	rows := buildServiceMetrics([]store.Application{app}, []store.Deployment{live, failed}, nil, nil)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Attempts != 2 || rows[0].Health != observe.HealthUnknown {
		t.Fatalf("row = %+v", rows[0])
	}
	if rows[0].Container != string(observe.ProbeNotProbed) {
		t.Fatalf("container = %s", rows[0].Container)
	}
	if rows[0].ReleaseID != "live-1" || rows[0].FailedDeploymentID != "fail-1" {
		t.Fatalf("release/failed = %s %s", rows[0].ReleaseID, rows[0].FailedDeploymentID)
	}
	if rows[0].FailedReason != "health check timed out" {
		t.Fatalf("reason = %q", rows[0].FailedReason)
	}
	observed, unique := countApplications(rows)
	if observed != 1 || unique != 1 {
		t.Fatalf("observed %d unique %d", observed, unique)
	}
	probed := buildServiceMetrics([]store.Application{app}, []store.Deployment{live, failed}, map[string]string{
		live.ID: string(observe.ProbeRunning),
	}, nil)
	if len(probed) != 1 || probed[0].Health != observe.HealthDegraded || probed[0].Container != string(observe.ProbeRunning) {
		t.Fatalf("probed = %+v", probed)
	}
}

func metricsFixture(t *testing.T) (*memProjects, *memApps, *memDeployments, store.Application) {
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
		Name: "app", RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return mem, apps, newMemDeployments(), app
}

func liveDeployment(t *testing.T, deps *memDeployments, app store.Application) store.Deployment {
	t.Helper()
	d, err := deps.CreateDeployment(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.ProjectID = app.ProjectID
	d.Status = store.StatusLive
	d.UpdatedAt = d.CreatedAt
	if err := deps.UpdateDeployment(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return d
}

func getMetricsOK(t *testing.T, srv *Server) metricsResponse {
	t.Helper()
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
	return body
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
