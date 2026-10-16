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

	"github.com/reyansh7/Forge/internal/store"
)

type memLinks struct {
	mu   sync.Mutex
	byID map[string]store.ApplicationLink
	seq  int
	apps *memApps
}

func newMemLinks(apps *memApps) *memLinks {
	return &memLinks{byID: map[string]store.ApplicationLink{}, apps: apps}
}

func (m *memLinks) CreateApplicationLink(_ context.Context, sourceID, targetID, envKey string) (store.ApplicationLink, error) {
	if err := store.ValidateEnvKey(envKey); err != nil {
		return store.ApplicationLink{}, err
	}
	if sourceID == targetID {
		return store.ApplicationLink{}, errSelfLink
	}
	src, err := m.apps.GetApplication(context.Background(), sourceID)
	if err != nil {
		return store.ApplicationLink{}, err
	}
	tgt, err := m.apps.GetApplication(context.Background(), targetID)
	if err != nil {
		return store.ApplicationLink{}, store.ErrNotFound
	}
	if src.ProjectID != tgt.ProjectID {
		return store.ApplicationLink{}, store.ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, l := range m.byID {
		if l.SourceApplicationID == sourceID && l.EnvKey == envKey {
			return store.ApplicationLink{}, store.ErrConflict
		}
	}
	m.seq++
	l := store.ApplicationLink{
		ID:                  "dddddddd-dddd-4000-8000-00000000000" + itoaDigit(m.seq),
		SourceApplicationID: sourceID,
		TargetApplicationID: targetID,
		EnvKey:              envKey,
		CreatedAt:           time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC),
	}
	m.byID[l.ID] = l
	return l, nil
}

var errSelfLink = errString("cannot link an application to itself")

type errString string

func (e errString) Error() string { return string(e) }

func (m *memLinks) ListApplicationLinks(_ context.Context, sourceID string) ([]store.ApplicationLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.ApplicationLink, 0)
	for _, l := range m.byID {
		if l.SourceApplicationID == sourceID {
			out = append(out, l)
		}
	}
	return out, nil
}

func (m *memLinks) DeleteApplicationLink(_ context.Context, sourceID, linkID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.byID[linkID]
	if !ok || l.SourceApplicationID != sourceID {
		return store.ErrNotFound
	}
	delete(m.byID, linkID)
	return nil
}

func TestCreateLinkSameProject(t *testing.T) {
	mem := newMemProjects()
	p, err := mem.CreateProject(context.Background(), testUserID, store.ProjectInput{
		Name: "demo", RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	apps := newMemApps()
	web, err := apps.CreateApplication(context.Background(), p.ID, store.ApplicationInput{
		Name: "web", RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	api, err := apps.CreateApplication(context.Background(), p.ID, store.ApplicationInput{
		Name: "api", RepositoryURL: store.SampleHelloURL, LocalHost: "api",
	})
	if err != nil {
		t.Fatal(err)
	}
	links := newMemLinks(apps)
	srv := appServer(mem, apps, newMemDeployments())
	srv.Links = links
	rec := httptest.NewRecorder()
	body := `{"target_application_id":"` + api.ID + `","env_key":"VITE_API_URL"}`
	req := httptest.NewRequest(http.MethodPost, "/applications/"+web.ID+"/links", bytes.NewReader([]byte(body)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got linkResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.URL != "http://api.localhost:9080/" || got.EnvKey != "VITE_API_URL" {
		t.Fatalf("%+v", got)
	}
}

func TestCreateLinkCrossProjectIsNotFound(t *testing.T) {
	mem := newMemProjects()
	p1, err := mem.CreateProject(context.Background(), testUserID, store.ProjectInput{
		Name: "a", RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := mem.CreateProject(context.Background(), testUserID, store.ProjectInput{
		Name: "b", RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	apps := newMemApps()
	src, err := apps.CreateApplication(context.Background(), p1.ID, store.ApplicationInput{
		Name: "web", RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := apps.CreateApplication(context.Background(), p2.ID, store.ApplicationInput{
		Name: "api", RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := appServer(mem, apps, newMemDeployments())
	srv.Links = newMemLinks(apps)
	rec := httptest.NewRecorder()
	body := `{"target_application_id":"` + other.ID + `","env_key":"API_URL"}`
	req := httptest.NewRequest(http.MethodPost, "/applications/"+src.ID+"/links", bytes.NewReader([]byte(body)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateLinkRequiresAuth(t *testing.T) {
	srv := &Server{Postgres: stubPing{}, Redis: stubPing{}, Links: newMemLinks(newMemApps())}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/applications/00000000-0000-4000-8000-000000000001/links", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestCreateLinkRejectsReservedKey(t *testing.T) {
	mem := newMemProjects()
	p, err := mem.CreateProject(context.Background(), testUserID, store.ProjectInput{
		Name: "demo", RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	apps := newMemApps()
	web, err := apps.CreateApplication(context.Background(), p.ID, store.ApplicationInput{
		Name: "web", RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	api, err := apps.CreateApplication(context.Background(), p.ID, store.ApplicationInput{
		Name: "api", RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := appServer(mem, apps, newMemDeployments())
	srv.Links = newMemLinks(apps)
	rec := httptest.NewRecorder()
	body := `{"target_application_id":"` + api.ID + `","env_key":"PORT"}`
	req := httptest.NewRequest(http.MethodPost, "/applications/"+web.ID+"/links", bytes.NewReader([]byte(body)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}
