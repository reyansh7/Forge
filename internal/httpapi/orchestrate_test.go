package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/reyansh7/Forge/internal/detect"
	"github.com/reyansh7/Forge/internal/queue"
	"github.com/reyansh7/Forge/internal/store"
)

func TestRollupStatusRequiresEveryService(t *testing.T) {
	if rollupStatus([]string{"live", "live"}) != "live" {
		t.Fatal("all live")
	}
	if rollupStatus([]string{"live", "failed"}) != "failed" {
		t.Fatal("partial failure must not be success")
	}
	if rollupStatus([]string{"live", "building"}) != "deploying" {
		t.Fatal("in progress")
	}
	if rollupStatus(nil) != "failed" {
		t.Fatal("empty")
	}
}

type recJobs struct {
	mu   sync.Mutex
	jobs []queue.Job
}

func (r *recJobs) Enqueue(_ context.Context, job queue.Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs = append(r.jobs, job)
	return nil
}

type memProjectDeploys struct {
	mu   sync.Mutex
	byID map[string]store.ProjectDeployment
	svcs map[string][]store.ProjectDeploymentService
	seq  int
}

func newMemProjectDeploys() *memProjectDeploys {
	return &memProjectDeploys{
		byID: map[string]store.ProjectDeployment{},
		svcs: map[string][]store.ProjectDeploymentService{},
	}
}

func (m *memProjectDeploys) CreateProjectDeployment(_ context.Context, projectID, note string, plan []byte) (store.ProjectDeployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	out := store.ProjectDeployment{
		ID:        fmt.Sprintf("eeeeeeee-eeee-4000-8000-%012d", m.seq),
		ProjectID: projectID,
		Status:    "deploying",
		Note:      note,
		Plan:      append([]byte(nil), plan...),
		CreatedAt: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC),
	}
	m.byID[out.ID] = out
	return out, nil
}

func (m *memProjectDeploys) AddProjectDeploymentService(_ context.Context, in store.ProjectDeploymentService) (store.ProjectDeploymentService, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	in.ID = fmt.Sprintf("ffffffff-ffff-4000-8000-%012d", m.seq)
	in.CreatedAt = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	m.svcs[in.ProjectDeploymentID] = append(m.svcs[in.ProjectDeploymentID], in)
	return in, nil
}

func (m *memProjectDeploys) SetProjectDeploymentStatus(_ context.Context, id, status, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.byID[id]
	if !ok {
		return store.ErrNotFound
	}
	if cur.Status == "live" {
		return nil
	}
	cur.Status = status
	if message != "" {
		cur.ErrorMessage = message
	}
	m.byID[id] = cur
	return nil
}

func (m *memProjectDeploys) GetProjectDeployment(_ context.Context, id string) (store.ProjectDeployment, []store.ProjectDeploymentService, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.byID[id]
	if !ok {
		return store.ProjectDeployment{}, nil, store.ErrNotFound
	}
	svcs := append([]store.ProjectDeploymentService(nil), m.svcs[id]...)
	return cur, svcs, nil
}

func (m *memProjectDeploys) ListProjectDeployments(_ context.Context, projectID string) ([]store.ProjectDeployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.ProjectDeployment
	for _, item := range m.byID {
		if item.ProjectID == projectID {
			out = append(out, item)
		}
	}
	return out, nil
}

func TestDeployAllQueuesExistingPipeline(t *testing.T) {
	mem := newMemProjects()
	p, err := mem.CreateProject(context.Background(), testUserID, store.ProjectInput{
		Name:          "mono",
		RepositoryURL: "https://github.com/octo/mono.git",
	})
	if err != nil {
		t.Fatal(err)
	}
	apps := newMemApps()
	deps := newMemDeployments()
	links := newMemLinks(apps)
	jobs := &recJobs{}
	rollouts := newMemProjectDeploys()
	srv := deployServer(mem, apps, deps, jobs)
	srv.Links = links
	srv.ProjectDeploys = rollouts
	srv.Discoverer = func(context.Context, store.Project) ([]detect.Service, error) {
		return []detect.Service{
			{Name: "frontend", Root: "frontend", Kind: "node", Framework: "vite-react", Runtime: "static", Port: 80, Deployable: true},
			{Name: "backend", Root: "backend", Kind: "python", Framework: "fastapi", Port: 8080, Deployable: true},
			{Name: "ml-model", Root: "ml-model", Kind: "python", Port: 8000, Deployable: true},
			{Name: "pkg", Root: "pkg", Deployable: false, Detail: "library"},
		}, nil
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects/"+p.ID+"/deploy", bytes.NewReader([]byte(`{}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var view projectDeployResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Status != "deploying" {
		t.Fatalf("status %s body %s", view.Status, rec.Body.String())
	}
	if len(view.Services) != 3 || len(jobs.jobs) != 3 {
		t.Fatalf("services %d jobs %d", len(view.Services), len(jobs.jobs))
	}
	if view.Services[0].Name != "ml-model" || view.Services[1].Name != "backend" || view.Services[2].Name != "frontend" {
		t.Fatalf("order %+v", view.Services)
	}
	for i, svc := range view.Services {
		var payload map[string]string
		if err := json.Unmarshal(jobs.jobs[i].Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["deployment_id"] != svc.DeploymentID || jobs.jobs[i].Type != queue.TypeDeploy {
			t.Fatalf("job %d payload %+v svc %+v", i, payload, svc)
		}
		if svc.InternalURL == "" || svc.Status != "queued" {
			t.Fatalf("svc %+v", svc)
		}
	}
	if len(view.Links) != 2 || view.Links[0].EnvKey != "VITE_API_URL" || view.Links[1].EnvKey != "ML_SERVICE_URL" {
		t.Fatalf("links %+v", view.Links)
	}
	front := view.Services[2]
	got, err := links.ListApplicationLinks(context.Background(), front.ApplicationID)
	if err != nil || len(got) != 1 || got[0].EnvKey != "VITE_API_URL" {
		t.Fatalf("stored links %+v err=%v", got, err)
	}
	apiApp, err := apps.GetApplication(context.Background(), view.Services[1].ApplicationID)
	if err != nil || apiApp.RootDirectory != "backend" {
		t.Fatalf("backend app %+v err=%v", apiApp, err)
	}

	// A required failure is not a successful project deployment.
	failed := deps.byID[view.Services[1].DeploymentID]
	failed.Status = store.StatusFailed
	failed.ErrorMessage = "health check failed"
	deps.byID[failed.ID] = failed
	for id, d := range deps.byID {
		if id == failed.ID {
			continue
		}
		d.Status = store.StatusLive
		deps.byID[id] = d
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/projects/"+p.ID+"/deploys/"+view.ID, nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Status != "failed" || view.Attention == "" {
		t.Fatalf("expected failed attention, got %+v", view)
	}
}

func TestDeployAllCanSelectOneService(t *testing.T) {
	mem := newMemProjects()
	p, err := mem.CreateProject(context.Background(), testUserID, store.ProjectInput{
		Name:          "mono",
		RepositoryURL: "https://github.com/octo/mono.git",
	})
	if err != nil {
		t.Fatal(err)
	}
	jobs := &recJobs{}
	srv := deployServer(mem, newMemApps(), newMemDeployments(), jobs)
	srv.ProjectDeploys = newMemProjectDeploys()
	srv.Discoverer = func(context.Context, store.Project) ([]detect.Service, error) {
		return []detect.Service{
			{Name: "api", Root: ".", Package: "./cmd/api", Kind: "go", Port: 8080, Deployable: true},
			{Name: "worker", Root: ".", Package: "./cmd/worker", Kind: "go", Port: 8080, Deployable: true},
		}, nil
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects/"+p.ID+"/deploy", bytes.NewReader([]byte(`{"services":["api"]}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var view projectDeployResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Services) != 1 || view.Services[0].Name != "api" || view.Services[0].Package != "./cmd/api" {
		t.Fatalf("%+v", view.Services)
	}
	if len(jobs.jobs) != 1 {
		t.Fatalf("jobs %d", len(jobs.jobs))
	}
}
