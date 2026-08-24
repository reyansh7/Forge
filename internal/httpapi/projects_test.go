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

// memProjects is an in-memory ProjectStore.
// HTTP tests prove status codes and JSON without Docker or Postgres.
// mu protects the map because httptest can run handlers concurrently.
type memProjects struct {
	mu   sync.Mutex
	byID map[string]store.Project
	seq  int
}

func newMemProjects() *memProjects {
	return &memProjects{byID: map[string]store.Project{}}
}

func (m *memProjects) CreateProject(_ context.Context, in store.ProjectInput) (store.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	// IDs look like real UUIDs so ParseProjectID on GET /projects/{id} succeeds.
	p := store.Project{
		ID:            "00000000-0000-4000-8000-00000000000" + itoaDigit(m.seq),
		Name:          in.Name,
		RepositoryURL: in.RepositoryURL,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	m.byID[p.ID] = p
	return p, nil
}

func (m *memProjects) GetProject(_ context.Context, id string) (store.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.byID[id]
	if !ok {
		return store.Project{}, store.ErrNotFound
	}
	return p, nil
}

func (m *memProjects) ListProjects(context.Context) ([]store.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.Project, 0, len(m.byID))
	for _, p := range m.byID {
		out = append(out, p)
	}
	return out, nil
}

func itoaDigit(n int) string {
	if n < 0 || n > 9 {
		return "x"
	}
	return string(rune('0' + n))
}

func projectServer(store ProjectStore) *Server {
	// httptest drives Handler() without opening :8080.
	return &Server{
		Postgres: stubPing{},
		Redis:    stubPing{},
		Projects: store,
	}
}

func TestCreateProjectCreated(t *testing.T) {
	// Happy path: 201, JSON body, Location header.
	srv := projectServer(newMemProjects())
	body := []byte(`{"name":"demo","repository_url":"https://github.com/example/app.git"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects", bytes.NewReader(body))
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got projectResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "demo" || got.ID == "" {
		t.Fatalf("got %+v", got)
	}
	if rec.Header().Get("Location") != "/projects/"+got.ID {
		t.Fatalf("Location = %q", rec.Header().Get("Location"))
	}
}

func TestCreateProjectRejectsInvalidBody(t *testing.T) {
	// Empty name must be 400, not an INSERT.
	srv := projectServer(newMemProjects())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects", bytes.NewReader([]byte(`{"name":"","repository_url":"https://example.com/r.git"}`)))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestCreateProjectRejectsFileURL(t *testing.T) {
	// file:// must not reach the store.
	srv := projectServer(newMemProjects())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects", bytes.NewReader([]byte(`{"name":"x","repository_url":"file:///etc/passwd"}`)))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestListProjectsEmpty(t *testing.T) {
	// Empty list must be [] (not null, not 404).
	srv := projectServer(newMemProjects())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/projects", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var list []projectResponse
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("body = %#v", list)
	}
}

func TestGetProjectNotFound(t *testing.T) {
	// Well-formed UUID that is not in the store → 404, not 500.
	srv := projectServer(newMemProjects())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/projects/00000000-0000-4000-8000-000000000099", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetProjectInvalidID(t *testing.T) {
	// Garbage path segment → 400, not a driver error.
	srv := projectServer(newMemProjects())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/projects/not-a-uuid", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestGetProjectOK(t *testing.T) {
	// GET after a create through the same fake store.
	mem := newMemProjects()
	srv := projectServer(mem)
	created, err := mem.CreateProject(context.Background(), store.ProjectInput{
		Name:          "demo",
		RepositoryURL: "https://github.com/example/app.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/projects/"+created.ID, nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHealthStillOKWithProjectsWired(t *testing.T) {
	// Adding /projects must not change GET /health.
	srv := projectServer(newMemProjects())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}
