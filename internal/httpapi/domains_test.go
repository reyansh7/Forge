package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/reyansh7/Forge/internal/store"
)

type memDomains struct {
	mu   sync.Mutex
	byID map[string]store.ApplicationDomain
	seq  int
}

func newMemDomains() *memDomains {
	return &memDomains{byID: map[string]store.ApplicationDomain{}}
}

func (m *memDomains) CreateApplicationDomain(_ context.Context, applicationID, hostname, environment string) (store.ApplicationDomain, error) {
	hostname, err := store.ValidateHostname(hostname)
	if err != nil {
		return store.ApplicationDomain{}, err
	}
	environment, err = store.ParseEnvironment(environment)
	if err != nil {
		return store.ApplicationDomain{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, d := range m.byID {
		if d.Hostname == hostname {
			return store.ApplicationDomain{}, store.ErrConflict
		}
		if d.ApplicationID == applicationID {
			n++
		}
	}
	if n >= 8 {
		return store.ApplicationDomain{}, errString("at most 8 custom domains per application")
	}
	m.seq++
	d := store.ApplicationDomain{
		ID:            "dddddddd-dddd-4000-8000-00000000000" + itoaDigit(m.seq),
		ApplicationID: applicationID,
		Hostname:      hostname,
		Environment:   environment,
		Status:        store.DomainPending,
		Token:         "tok" + itoaDigit(m.seq) + "aaaaaaaaaaaaaaaa",
	}
	m.byID[d.ID] = d
	return d, nil
}

func (m *memDomains) ListApplicationDomains(_ context.Context, applicationID string) ([]store.ApplicationDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.ApplicationDomain, 0)
	for _, d := range m.byID {
		if d.ApplicationID == applicationID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (m *memDomains) GetApplicationDomain(_ context.Context, applicationID, domainID string) (store.ApplicationDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.byID[domainID]
	if !ok || d.ApplicationID != applicationID {
		return store.ApplicationDomain{}, store.ErrNotFound
	}
	return d, nil
}

func (m *memDomains) ActivateApplicationDomain(_ context.Context, applicationID, domainID string) (store.ApplicationDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.byID[domainID]
	if !ok || d.ApplicationID != applicationID {
		return store.ApplicationDomain{}, store.ErrNotFound
	}
	d.Status = store.DomainActive
	m.byID[domainID] = d
	return d, nil
}

func (m *memDomains) DeleteApplicationDomain(_ context.Context, applicationID, domainID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.byID[domainID]
	if !ok || d.ApplicationID != applicationID {
		return store.ErrNotFound
	}
	delete(m.byID, domainID)
	return nil
}

func TestCreateDomainRejectsLocalhost(t *testing.T) {
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
	srv := appServer(mem, apps, newMemDeployments())
	srv.Domains = newMemDomains()
	rec := httptest.NewRecorder()
	body := `{"hostname":"hello.localhost","environment":"production"}`
	req := httptest.NewRequest(http.MethodPost, "/applications/"+app.ID+"/domains", bytes.NewReader([]byte(body)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateDomainConflictAndVerify(t *testing.T) {
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
	other, err := apps.CreateApplication(context.Background(), p.ID, store.ApplicationInput{
		Name: "web", RepositoryURL: store.SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	doms := newMemDomains()
	srv := appServer(mem, apps, newMemDeployments())
	srv.Domains = doms

	rec := httptest.NewRecorder()
	body := `{"hostname":"App.Example.COM"}`
	req := httptest.NewRequest(http.MethodPost, "/applications/"+app.ID+"/domains", bytes.NewReader([]byte(body)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created domainResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Hostname != "app.example.com" || created.Status != store.DomainPending {
		t.Fatalf("%+v", created)
	}
	if created.TXTName != "_forge-challenge.app.example.com" || created.TXTValue == "" {
		t.Fatalf("challenge = %+v", created)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/applications/"+other.ID+"/domains", bytes.NewReader([]byte(`{"hostname":"app.example.com"}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("steal status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/applications/"+app.ID+"/domains/"+created.ID+"/verify", nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("verify without TXT = %d", rec.Code)
	}

	srv.LookupTXT = func(_ context.Context, name string) ([]string, error) {
		if name != created.TXTName {
			t.Fatalf("looked up %q", name)
		}
		return []string{created.TXTValue}, nil
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/applications/"+app.ID+"/domains/"+created.ID+"/verify", nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify = %d body=%s", rec.Code, rec.Body.String())
	}
	var active domainResponse
	if err := json.NewDecoder(rec.Body).Decode(&active); err != nil {
		t.Fatal(err)
	}
	if active.Status != store.DomainActive {
		t.Fatalf("%+v", active)
	}
}

func TestCreateDomainRequiresAuth(t *testing.T) {
	srv := &Server{Postgres: stubPing{}, Redis: stubPing{}, Domains: newMemDomains()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/applications/00000000-0000-4000-8000-000000000001/domains", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}
