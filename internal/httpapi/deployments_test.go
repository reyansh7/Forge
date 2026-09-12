package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/reyansh7/Forge/internal/queue"
	"github.com/reyansh7/Forge/internal/store"
)

type memDeployments struct {
	mu   sync.Mutex
	byID map[string]store.Deployment
	seq  int
}

func newMemDeployments() *memDeployments {
	return &memDeployments{byID: map[string]store.Deployment{}}
}

func (m *memDeployments) CreateDeployment(_ context.Context, applicationID string) (store.Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	d := store.Deployment{
		ID:            "bbbbbbbb-bbbb-4000-8000-00000000000" + itoaDigit(m.seq),
		ProjectID:     "00000000-0000-4000-8000-000000000001",
		ApplicationID: applicationID,
		Status:        store.StatusQueued,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	m.byID[d.ID] = d
	return d, nil
}

func (m *memDeployments) CreateRollbackDeployment(ctx context.Context, sourceID string) (store.Deployment, error) {
	m.mu.Lock()
	src, ok := m.byID[sourceID]
	m.mu.Unlock()
	if !ok || src.ImageName == "" {
		return store.Deployment{}, store.ErrNotFound
	}
	d, err := m.CreateDeployment(ctx, src.ApplicationID)
	if err != nil {
		return store.Deployment{}, err
	}
	d.RollbackOf = src.ID
	d.ImageName = src.ImageName
	d.RuntimeKind = src.RuntimeKind
	m.mu.Lock()
	m.byID[d.ID] = d
	m.mu.Unlock()
	return d, nil
}

func (m *memDeployments) GetDeployment(_ context.Context, id string) (store.Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.byID[id]
	if !ok {
		return store.Deployment{}, store.ErrNotFound
	}
	return d, nil
}

func (m *memDeployments) ListDeploymentsByProject(_ context.Context, projectID string) ([]store.Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.Deployment, 0)
	for _, d := range m.byID {
		if d.ProjectID == projectID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (m *memDeployments) ListDeploymentsByApplication(_ context.Context, applicationID string) ([]store.Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.Deployment, 0)
	for _, d := range m.byID {
		if d.ApplicationID == applicationID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (m *memDeployments) ListLiveDeployments(context.Context) ([]store.Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.Deployment, 0)
	for _, d := range m.byID {
		if d.Status == store.StatusLive {
			out = append(out, d)
		}
	}
	return out, nil
}

func (m *memDeployments) CountInProgressByOwner(_ context.Context, _ string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, d := range m.byID {
		if d.Status.InProgress() {
			n++
		}
	}
	return n, nil
}

func (m *memDeployments) UpdateDeployment(_ context.Context, d store.Deployment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byID[d.ID] = d
	return nil
}

func (m *memDeployments) markAllFailed() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, d := range m.byID {
		d.Status = store.StatusFailed
		m.byID[id] = d
	}
}

func deployServer(projects ProjectStore, apps ApplicationStore, deps DeploymentStore, jobs JobQueue) *Server {
	return withAuth(&Server{
		Postgres:    stubPing{},
		Redis:       stubPing{},
		Projects:    projects,
		Apps:        apps,
		Deployments: deps,
		Jobs:        jobs,
	})
}

func TestCreateDeploymentAccepted(t *testing.T) {
	mem := newMemProjects()
	p, err := mem.CreateProject(context.Background(), testUserID, store.ProjectInput{
		Name:          "demo",
		RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	apps := newMemApps()
	if _, err := apps.CreateApplication(context.Background(), p.ID, store.ApplicationInput{
		Name:          store.DefaultApplicationName,
		RepositoryURL: store.SampleHelloURL,
	}); err != nil {
		t.Fatal(err)
	}
	jobs := &stubJobs{}
	srv := deployServer(mem, apps, newMemDeployments(), jobs)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects/"+p.ID+"/deployments", bytes.NewReader([]byte(`{}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if jobs.last.Type != queue.TypeDeploy {
		t.Fatalf("job type = %q", jobs.last.Type)
	}
	var payload map[string]string
	if err := json.Unmarshal(jobs.last.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["deployment_id"] == "" {
		t.Fatal("missing deployment_id")
	}
	if _, ok := payload["command"]; ok {
		t.Fatal("command must not be in payload")
	}
}

func TestDeployConcurrencyQuota(t *testing.T) {
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
	if _, err := deps.CreateDeployment(context.Background(), app.ID); err != nil {
		t.Fatal(err)
	}
	srv := deployServer(mem, apps, deps, &stubJobs{})
	srv.Limits.MaxInflightDeploys = 1
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects/"+p.ID+"/deployments", bytes.NewReader([]byte(`{}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("deploy concurrency quota exceeded")) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestDeployRateLimit(t *testing.T) {
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
	srv := deployServer(mem, apps, deps, &stubJobs{})
	srv.Limits.MaxInflightDeploys = 20
	srv.deployGate = newAttemptGate(2, time.Minute)

	post := func() *httptest.ResponseRecorder {
		deps.markAllFailed()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/applications/"+app.ID+"/deployments", bytes.NewReader([]byte(`{}`)))
		testHandler(srv).ServeHTTP(rec, req)
		return rec
	}
	if rec := post(); rec.Code != http.StatusAccepted {
		t.Fatalf("first status = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := post(); rec.Code != http.StatusAccepted {
		t.Fatalf("second status = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := post(); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limit status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateDeploymentUnknownProject(t *testing.T) {
	srv := deployServer(newMemProjects(), newMemApps(), newMemDeployments(), &stubJobs{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects/00000000-0000-4000-8000-000000000099/deployments", bytes.NewReader([]byte(`{}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestGetDeploymentNotFound(t *testing.T) {
	srv := deployServer(newMemProjects(), newMemApps(), newMemDeployments(), &stubJobs{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/deployments/00000000-0000-4000-8000-000000000099", nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestListDeploymentsEmpty(t *testing.T) {
	mem := newMemProjects()
	p, err := mem.CreateProject(context.Background(), testUserID, store.ProjectInput{
		Name:          "demo",
		RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	apps := newMemApps()
	if _, err := apps.CreateApplication(context.Background(), p.ID, store.ApplicationInput{
		Name:          store.DefaultApplicationName,
		RepositoryURL: store.SampleHelloURL,
	}); err != nil {
		t.Fatal(err)
	}
	srv := deployServer(mem, apps, newMemDeployments(), &stubJobs{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/projects/"+p.ID+"/deployments", nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var list []deploymentResponse
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("%#v", list)
	}
}

func TestRollbackRequiresImage(t *testing.T) {
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
	srv := deployServer(mem, apps, deps, &stubJobs{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/deployments/"+d.ID+"/rollback", bytes.NewReader([]byte(`{}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRollbackAccepted(t *testing.T) {
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
	d.Status = store.StatusStopped
	d.ImageName = "forge-app-old"
	d.RuntimeKind = "dockerfile"
	_ = deps.UpdateDeployment(context.Background(), d)

	jobs := &stubJobs{}
	srv := deployServer(mem, apps, deps, jobs)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/deployments/"+d.ID+"/rollback", bytes.NewReader([]byte(`{}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if jobs.last.Type != queue.TypeDeploy {
		t.Fatalf("job type = %q", jobs.last.Type)
	}
}
