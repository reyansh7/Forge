package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/reyansh7/Forge/internal/queue"
	"github.com/reyansh7/Forge/internal/runtime"
	"github.com/reyansh7/Forge/internal/store"
)

type mem struct {
	mu   sync.Mutex
	app  store.Application
	dep  store.Deployment
	env  []store.EnvVar
	live []store.Deployment
}

func (m *mem) GetApplication(context.Context, string) (store.Application, error) { return m.app, nil }

func (m *mem) ListEnvVars(context.Context, string) ([]store.EnvVar, error) {
	return append([]store.EnvVar{}, m.env...), nil
}

func (m *mem) GetDeployment(context.Context, string) (store.Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dep, nil
}

func (m *mem) UpdateDeployment(_ context.Context, d store.Deployment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dep = d
	return nil
}

func (m *mem) ListLiveDeployments(context.Context) ([]store.Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]store.Deployment{}, m.live...), nil
}

type stubFetch struct{}

func (stubFetch) Fetch(_ context.Context, _, dest string) error {
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dest, "Dockerfile"), []byte("FROM scratch\n"), 0o600)
}

type stubBuild struct {
	err error
	log string
}

func (s stubBuild) Build(_ context.Context, _, _, _ string) (string, error) {
	return s.log, s.err
}

type stubRun struct {
	env     []runtime.EnvPair
	stopped []string
}

func (s *stubRun) Run(_ context.Context, _, _ string, env []runtime.EnvPair) (runtime.Instance, error) {
	s.env = env
	return runtime.Instance{ContainerID: "cid", HostPort: 49152}, nil
}

func (s *stubRun) Stop(_ context.Context, name string) error {
	s.stopped = append(s.stopped, name)
	return nil
}

func (stubRun) Logs(context.Context, string, int) (string, error) { return "", nil }

type stubRouter struct{ n int }

func (s *stubRouter) Apply(context.Context, []store.Deployment) error {
	s.n++
	return nil
}

func testJob(id string) queue.Job {
	raw, _ := json.Marshal(deployPayload{DeploymentID: id})
	return queue.Job{ID: "j1", Type: queue.TypeDeploy, Payload: raw}
}

func TestHandlerHappyPath(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
		env: []store.EnvVar{{Key: "GREETING", Value: "hi"}},
	}
	rt := &stubRouter{}
	run := &stubRun{}
	h := Handler{
		Store:     st,
		Fetcher:   stubFetch{},
		Builder:   stubBuild{},
		Runner:    run,
		Router:    rt,
		ProxyBase: "http://127.0.0.1:9080",
		Health:    func(context.Context, int, time.Duration) error { return nil },
	}
	if err := h.Handle(context.Background(), testJob(id)); err != nil {
		t.Fatal(err)
	}
	if st.dep.Status != store.StatusLive {
		t.Fatalf("status = %q", st.dep.Status)
	}
	if st.dep.PublicURL != "http://127.0.0.1:9080/d/"+id+"/" {
		t.Fatalf("url = %q", st.dep.PublicURL)
	}
	if rt.n != 1 {
		t.Fatalf("caddy applies = %d", rt.n)
	}
	if len(run.env) != 1 || run.env[0].Key != "GREETING" || run.env[0].Value != "hi" {
		t.Fatalf("env = %#v", run.env)
	}
}

func TestHandlerRecordsBuildFailure(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
	}
	h := Handler{
		Store:   st,
		Fetcher: stubFetch{},
		Builder: stubBuild{err: errors.New("boom"), log: "line1\n"},
		Runner:  &stubRun{},
		Router:  &stubRouter{},
		Health:  func(context.Context, int, time.Duration) error { return nil },
	}
	if err := h.Handle(context.Background(), testJob(id)); err == nil {
		t.Fatal("expected error")
	}
	if st.dep.Status != store.StatusFailed || st.dep.FailedStage != string(store.StatusBuilding) {
		t.Fatalf("status=%q stage=%q", st.dep.Status, st.dep.FailedStage)
	}
	if st.dep.BuildLog != "line1\n" {
		t.Fatalf("build_log = %q", st.dep.BuildLog)
	}
}

func TestHandlerRejectsUnknownType(t *testing.T) {
	h := Handler{Store: &mem{}}
	err := h.Handle(context.Background(), queue.Job{ID: "1", Type: queue.TypeExample})
	if !errors.Is(err, queue.ErrUnknownType) {
		t.Fatalf("err = %v", err)
	}
}

func TestHandlerRejectsCommandPayloadWithoutID(t *testing.T) {
	h := Handler{Store: &mem{}}
	err := h.Handle(context.Background(), queue.Job{
		ID:      "1",
		Type:    queue.TypeDeploy,
		Payload: json.RawMessage(`{"command":"rm -rf /"}`),
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestHandlerStopsPreviousLiveBeforeRun(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	prev := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeef"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
		live: []store.Deployment{{
			ID:            prev,
			ProjectID:     pid,
			ApplicationID: aid,
			Status:        store.StatusLive,
			ContainerID:   "old",
		}},
	}
	run := &stubRun{}
	h := Handler{
		Store:   st,
		Fetcher: stubFetch{},
		Builder: stubBuild{},
		Runner:  run,
		Router:  &stubRouter{},
		Health:  func(context.Context, int, time.Duration) error { return nil },
	}
	if err := h.Handle(context.Background(), testJob(id)); err != nil {
		t.Fatal(err)
	}
	if len(run.stopped) == 0 {
		t.Fatal("expected previous container to be stopped before the new run")
	}
}

type boomFetch struct{}

func (boomFetch) Fetch(context.Context, string, string) error {
	return errors.New("must not fetch on rollback")
}

type boomBuild struct{}

func (boomBuild) Build(context.Context, string, string, string) (string, error) {
	return "", errors.New("must not build on rollback")
}

func TestHandlerRollbackReusesImage(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	src := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeef"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, RepositoryURL: store.SampleHelloURL, HealthPath: "/"},
		dep: store.Deployment{
			ID:            id,
			ProjectID:     pid,
			ApplicationID: aid,
			Status:        store.StatusQueued,
			RollbackOf:    src,
			ImageName:     "forge-app-oldimage",
			RuntimeKind:   "dockerfile",
		},
	}
	run := &stubRun{}
	h := Handler{
		Store:     st,
		Fetcher:   boomFetch{},
		Builder:   boomBuild{},
		Runner:    run,
		Router:    &stubRouter{},
		ProxyBase: "http://127.0.0.1:9080",
		Health:    func(context.Context, int, time.Duration) error { return nil },
	}
	if err := h.Handle(context.Background(), testJob(id)); err != nil {
		t.Fatal(err)
	}
	if st.dep.Status != store.StatusLive {
		t.Fatalf("status = %q", st.dep.Status)
	}
}
