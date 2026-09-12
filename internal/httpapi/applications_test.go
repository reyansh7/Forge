package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/reyansh7/Forge/internal/store"
)

type memApps struct {
	mu        sync.Mutex
	byID      map[string]store.Application
	env       map[string]map[string]string
	seq       int
	conflicts map[string]bool
}

func newMemApps() *memApps {
	return &memApps{
		byID:      map[string]store.Application{},
		env:       map[string]map[string]string{},
		conflicts: map[string]bool{},
	}
}

func (m *memApps) CreateApplication(_ context.Context, projectID string, in store.ApplicationInput) (store.Application, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := projectID + "/" + in.Name
	if m.conflicts[key] {
		return store.Application{}, store.ErrConflict
	}
	m.seq++
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	a := store.Application{
		ID:            "cccccccc-cccc-4000-8000-00000000000" + itoaDigit(m.seq),
		ProjectID:     projectID,
		Name:          in.Name,
		RepositoryURL: in.RepositoryURL,
		RootDirectory: in.RootDirectory,
		HealthPath:    in.HealthPath,
		LocalHost:     in.LocalHost,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if a.RootDirectory == "" {
		a.RootDirectory = "."
	}
	if a.HealthPath == "" {
		a.HealthPath = "/"
	}
	m.byID[a.ID] = a
	m.conflicts[key] = true
	m.env[a.ID] = map[string]string{}
	return a, nil
}

func (m *memApps) GetApplication(_ context.Context, id string) (store.Application, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.byID[id]
	if !ok {
		return store.Application{}, store.ErrNotFound
	}
	return a, nil
}

func (m *memApps) ListApplicationsByProject(_ context.Context, projectID string) ([]store.Application, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.Application, 0)
	for _, a := range m.byID {
		if a.ProjectID == projectID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *memApps) UpdateApplication(_ context.Context, id string, in store.ApplicationInput) (store.Application, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.byID[id]
	if !ok {
		return store.Application{}, store.ErrNotFound
	}
	a.Name = in.Name
	a.RepositoryURL = in.RepositoryURL
	a.RootDirectory = in.RootDirectory
	a.HealthPath = in.HealthPath
	a.LocalHost = in.LocalHost
	if a.RootDirectory == "" {
		a.RootDirectory = "."
	}
	if a.HealthPath == "" {
		a.HealthPath = "/"
	}
	m.byID[id] = a
	return a, nil
}

func (m *memApps) DeleteApplication(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[id]; !ok {
		return store.ErrNotFound
	}
	delete(m.byID, id)
	delete(m.env, id)
	return nil
}

func (m *memApps) ListEnvVars(_ context.Context, applicationID string) ([]store.EnvVar, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.env[applicationID]
	out := make([]store.EnvVar, 0, len(src))
	for k, v := range src {
		out = append(out, store.EnvVar{Key: k, Value: v})
	}
	return out, nil
}

func (m *memApps) PutEnvVar(_ context.Context, applicationID, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.env[applicationID] == nil {
		m.env[applicationID] = map[string]string{}
	}
	m.env[applicationID][key] = value
	return nil
}

func (m *memApps) ReplaceEnvVars(_ context.Context, applicationID string, vars []store.EnvVar) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := map[string]string{}
	for _, ev := range vars {
		next[ev.Key] = ev.Value
	}
	m.env[applicationID] = next
	return nil
}

func (m *memApps) DeleteEnvVar(_ context.Context, applicationID, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.env[applicationID][key]; !ok {
		return store.ErrNotFound
	}
	delete(m.env[applicationID], key)
	return nil
}

type stubRuntime struct {
	stopped []string
	logs    string
	running bool
}

func (s *stubRuntime) Logs(context.Context, string, int) (string, error) { return s.logs, nil }
func (s *stubRuntime) FollowLogs(ctx context.Context, _ string, _ int, write func(string) error) error {
	if s.logs != "" {
		if err := write(strings.TrimRight(s.logs, "\n")); err != nil {
			return err
		}
	}
	return ctx.Err()
}
func (s *stubRuntime) Stop(_ context.Context, name string) error {
	s.stopped = append(s.stopped, name)
	return nil
}
func (s *stubRuntime) Running(context.Context, string) (bool, error) { return s.running, nil }

func appServer(projects ProjectStore, apps ApplicationStore, deps DeploymentStore) *Server {
	return withAuth(&Server{
		Postgres:    stubPing{},
		Redis:       stubPing{},
		Projects:    projects,
		Apps:        apps,
		Deployments: deps,
		Jobs:        &stubJobs{},
		Runtime:     &stubRuntime{logs: "hello from container\n"},
	})
}

func TestCreateApplicationCreated(t *testing.T) {
	mem := newMemProjects()
	p, err := mem.CreateProject(context.Background(), testUserID, store.ProjectInput{
		Name:          "demo",
		RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := appServer(mem, newMemApps(), newMemDeployments())
	body := []byte(`{"name":"api","repository_url":"forge://hello"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects/"+p.ID+"/applications", bytes.NewReader(body))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPutEnvRejectsPORT(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodPut, "/applications/"+app.ID+"/env", bytes.NewReader([]byte(`{"key":"PORT","value":"9"}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestPutEnvAccepted(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodPut, "/applications/"+app.ID+"/env", bytes.NewReader([]byte(`{"key":"GREETING","value":"hi"}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestReplaceEnvBulk(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodPut, "/applications/"+app.ID+"/env/bulk", bytes.NewReader([]byte(`{"vars":[{"key":"GREETING","value":"hi"}]}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestApplicationLogsNeedLive(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodGet, "/applications/"+app.ID+"/logs", nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestStopApplication(t *testing.T) {
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
	d.HostPort = 49152
	_ = deps.UpdateDeployment(context.Background(), d)

	rt := &stubRuntime{}
	srv := appServer(mem, apps, deps)
	srv.Runtime = rt
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/applications/"+app.ID+"/stop", nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(rt.stopped) != 1 || !strings.Contains(rt.stopped[0], "forge-run-") {
		t.Fatalf("stopped = %#v", rt.stopped)
	}
}

func TestCreateDeploymentConflictWhenMultipleApps(t *testing.T) {
	mem := newMemProjects()
	p, err := mem.CreateProject(context.Background(), testUserID, store.ProjectInput{
		Name:          "demo",
		RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	apps := newMemApps()
	_, _ = apps.CreateApplication(context.Background(), p.ID, store.ApplicationInput{Name: "a", RepositoryURL: store.SampleHelloURL})
	_, _ = apps.CreateApplication(context.Background(), p.ID, store.ApplicationInput{Name: "b", RepositoryURL: store.SampleHelloURL})
	srv := deployServer(mem, apps, newMemDeployments(), &stubJobs{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects/"+p.ID+"/deployments", bytes.NewReader([]byte(`{}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestCreateDeploymentRejectedWhenLive(t *testing.T) {
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
		Name:          store.DefaultApplicationName,
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
	d.HostPort = 49152
	_ = deps.UpdateDeployment(context.Background(), d)

	rt := &stubRuntime{running: true}
	srv := deployServer(mem, apps, deps, &stubJobs{})
	srv.Runtime = rt
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/applications/"+app.ID+"/deployments", bytes.NewReader([]byte(`{}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateDeploymentRecoversStaleLive(t *testing.T) {
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
		Name:          store.DefaultApplicationName,
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
	d.HostPort = 49152
	_ = deps.UpdateDeployment(context.Background(), d)

	rt := &stubRuntime{running: false}
	jobs := &stubJobs{}
	srv := deployServer(mem, apps, deps, jobs)
	srv.Runtime = rt
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/applications/"+app.ID+"/deployments", bytes.NewReader([]byte(`{}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestApplicationHealthStoppedWithoutLive(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodGet, "/applications/"+app.ID+"/health", nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "stopped" {
		t.Fatalf("status = %v", body["status"])
	}
}

func TestApplicationHealthUsesContainerStateNotHTTP(t *testing.T) {
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
	d.HostPort = 49152
	d.PublicURL = "http://127.0.0.1:9080/d/" + d.ID + "/"
	_ = deps.UpdateDeployment(context.Background(), d)

	rt := &stubRuntime{running: true}
	srv := appServer(mem, apps, deps)
	srv.Runtime = rt

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/applications/"+app.ID+"/health", nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var healthy map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &healthy); err != nil {
		t.Fatal(err)
	}
	if healthy["status"] != "healthy" {
		t.Fatalf("status = %v", healthy["status"])
	}
	if _, ok := healthy["http_status"]; ok {
		t.Fatal("http_status must be absent: health must not HTTP-probe the workload")
	}

	rt.running = false
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/applications/"+app.ID+"/health", nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var unhealthy map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &unhealthy); err != nil {
		t.Fatal(err)
	}
	if unhealthy["status"] != "unhealthy" {
		t.Fatalf("status = %v", unhealthy["status"])
	}
}
