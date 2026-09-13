package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/reyansh7/Forge/internal/queue"
	"github.com/reyansh7/Forge/internal/schedule"
	"github.com/reyansh7/Forge/internal/store"
)

const testNodeID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

type stubPlacer struct {
	node store.Node
	err  error
}

func (s stubPlacer) Place(context.Context) (store.Node, error) {
	return s.node, s.err
}

type memNodes struct {
	mu      sync.Mutex
	byName  map[string]store.Node
	depNode map[string]string
}

func newMemNodes() *memNodes {
	return &memNodes{byName: map[string]store.Node{}, depNode: map[string]string{}}
}

func (m *memNodes) CreateNode(_ context.Context, name, advertiseHost string) (store.Node, string, error) {
	name, err := store.ValidateNodeName(name)
	if err != nil {
		return store.Node{}, "", err
	}
	host, err := store.ValidateAdvertiseHost(advertiseHost)
	if err != nil {
		return store.Node{}, "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byName[name]; ok {
		return store.Node{}, "", store.ErrConflict
	}
	n := store.Node{ID: testNodeID, Name: name, Status: store.NodeReady, AdvertiseHost: host}
	m.byName[name] = n
	return n, "join-token-once", nil
}

func (m *memNodes) ListNodes(context.Context) ([]store.Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.Node, 0, len(m.byName))
	for _, n := range m.byName {
		out = append(out, n)
	}
	return out, nil
}

func (m *memNodes) SetDeploymentNode(_ context.Context, deploymentID, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.depNode[deploymentID] = nodeID
	return nil
}

func TestNodesRequireAuth(t *testing.T) {
	srv := &Server{Postgres: stubPing{}, Redis: stubPing{}, Nodes: newMemNodes()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/nodes", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestCreateAndListNodes(t *testing.T) {
	nodes := newMemNodes()
	srv := withAuth(&Server{Postgres: stubPing{}, Redis: stubPing{}, Nodes: nodes})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/nodes", bytes.NewReader([]byte(`{"name":"lab2"}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created nodeResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.JoinToken == "" || created.Name != "lab2" {
		t.Fatalf("%+v", created)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/nodes", nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var listed []nodeResponse
	if err := json.NewDecoder(rec.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].JoinToken != "" || listed[0].Name != "lab2" {
		t.Fatalf("%+v", listed)
	}
}

func TestCreateDeploymentPlacesOnNode(t *testing.T) {
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
	sink := queue.NewDirected()
	nodes := newMemNodes()
	jobs := &stubJobs{}
	srv := deployServer(mem, apps, newMemDeployments(), jobs)
	srv.Nodes = nodes
	srv.Placer = stubPlacer{node: store.Node{ID: testNodeID, Name: "lab2"}}
	srv.Sink = sink

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects/"+p.ID+"/deployments", bytes.NewReader([]byte(`{}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if jobs.last.ID != "" {
		t.Fatal("must not enqueue on the shared list when placed")
	}
	if len(sink.JobsOn(testNodeID)) != 1 {
		t.Fatalf("expected job on node list")
	}
	var body createDeploymentResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.NodeID != testNodeID {
		t.Fatalf("node_id = %q", body.NodeID)
	}
}

func TestCreateDeploymentConflictWhenNoCapacity(t *testing.T) {
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
	deps := newMemDeployments()
	jobs := &stubJobs{}
	srv := deployServer(mem, apps, deps, jobs)
	srv.Nodes = newMemNodes()
	srv.Placer = stubPlacer{err: schedule.ErrNoCapacity}
	srv.Sink = queue.NewDirected()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects/"+p.ID+"/deployments", bytes.NewReader([]byte(`{}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if jobs.last.ID != "" {
		t.Fatal("must not enqueue")
	}
}

func TestEnqueueExamplePlacesWhenPlacerSet(t *testing.T) {
	sink := queue.NewDirected()
	st := &stubJobs{}
	srv := jobServer(st)
	srv.Placer = stubPlacer{node: store.Node{ID: testNodeID, Name: "lab2"}}
	srv.Sink = sink
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/jobs", bytes.NewReader([]byte(`{"type":"example"}`)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if st.last.ID != "" {
		t.Fatal("must not use shared list")
	}
	if len(sink.JobsOn(testNodeID)) != 1 {
		t.Fatal("expected directed example job")
	}
}
