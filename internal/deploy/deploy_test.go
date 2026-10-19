package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/reyansh7/Forge/internal/queue"
	"github.com/reyansh7/Forge/internal/runtime"
	"github.com/reyansh7/Forge/internal/store"
)

type mem struct {
	mu    sync.Mutex
	app   store.Application
	dep   store.Deployment
	env   []store.EnvVar
	live  []store.Deployment
	apps  map[string]store.Application
	links []store.ApplicationLink
	vols  []store.ApplicationVolume
}

func (m *mem) GetApplication(_ context.Context, id string) (store.Application, error) {
	if m.apps != nil {
		if a, ok := m.apps[id]; ok {
			return a, nil
		}
	}
	return m.app, nil
}

func (m *mem) ListApplicationLinks(context.Context, string) ([]store.ApplicationLink, error) {
	return append([]store.ApplicationLink{}, m.links...), nil
}

func (m *mem) ListApplicationVolumes(context.Context, string) ([]store.ApplicationVolume, error) {
	return append([]store.ApplicationVolume{}, m.vols...), nil
}

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

func (m *mem) GitHubCloneToken(context.Context, string) (string, error) {
	return "", nil
}

func (m *mem) CutoverLive(_ context.Context, next store.Deployment) ([]store.Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prev := make([]store.Deployment, 0)
	keep := make([]store.Deployment, 0, len(m.live))
	for _, d := range m.live {
		if d.ID != next.ID && d.ApplicationID == next.ApplicationID && store.SameEnvironment(d.Environment, next.Environment) {
			d.Status = store.StatusStopped
			d.FailedStage = ""
			d.ErrorMessage = SanitizeNotice("replaced by a newer deployment")
			prev = append(prev, d)
			continue
		}
		if d.ID == next.ID {
			continue
		}
		keep = append(keep, d)
	}
	next.Status = store.StatusLive
	next.FailedStage = ""
	next.ErrorMessage = ""
	m.dep = next
	m.live = append(keep, next)
	return prev, nil
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

func (s stubBuild) Build(_ context.Context, _, _, _ string, _ []runtime.EnvPair) (string, error) {
	return s.log, s.err
}

type stubRun struct {
	env          []runtime.EnvPair
	vols         []runtime.VolumeMount
	stopped      []string
	stoppedAtRun []string
}

func (s *stubRun) Run(_ context.Context, _, _ string, env []runtime.EnvPair, _ int, vols []runtime.VolumeMount) (runtime.Instance, error) {
	s.env = env
	s.vols = vols
	s.stoppedAtRun = append([]string{}, s.stopped...)
	return runtime.Instance{ContainerID: "cid", HostPort: 49152}, nil
}

func (s *stubRun) Stop(_ context.Context, name string) error {
	s.stopped = append(s.stopped, name)
	return nil
}

func (stubRun) Logs(context.Context, string, int) (string, error) { return "", nil }

func (stubRun) Inspect(context.Context, string) (runtime.ContainerState, error) {
	return runtime.ContainerState{Exists: true, Running: true, Status: "running"}, nil
}

type stubRouter struct {
	n    int
	last []store.Deployment
	err  error
}

func (s *stubRouter) Apply(_ context.Context, live []store.Deployment) error {
	s.n++
	s.last = append([]store.Deployment{}, live...)
	return s.err
}

func testJob(id string) queue.Job {
	raw, _ := json.Marshal(deployPayload{DeploymentID: id})
	return queue.Job{ID: "j1", Type: queue.TypeDeploy, Payload: raw}
}

type noLease struct{ *mem }

func (noLease) AcquireLease(context.Context, string, string, string, time.Duration) (store.Deployment, error) {
	return store.Deployment{}, store.ErrLeaseLost
}

func (noLease) RenewLease(context.Context, string, string, string, time.Duration) error {
	return nil
}

func (noLease) UpdateOwnedDeployment(context.Context, store.Deployment) error {
	return nil
}

func TestHandlerSkipsWhenLeaseNotAcquired(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &noLease{mem: &mem{
		app: store.Application{ID: aid, ProjectID: pid, Name: "app", RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued, ActiveJobID: "j1"},
	}}
	h := Handler{
		Store:   st,
		Fetcher: stubFetch{},
		Builder: boomBuild{},
		Runner:  &stubRun{},
		Router:  &stubRouter{},
		NodeID:  "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee9",
	}
	if err := h.Handle(context.Background(), testJob(id)); err != nil {
		t.Fatal(err)
	}
	if st.dep.Status != store.StatusQueued {
		t.Fatalf("lost lease must not run, status = %q", st.dep.Status)
	}
}

func TestHandlerIgnoresSupersededJob(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, Name: "app", RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued, ActiveJobID: "newer"},
	}
	h := Handler{
		Store:   st,
		Fetcher: stubFetch{},
		Builder: boomBuild{},
		Runner:  &stubRun{},
		Router:  &stubRouter{},
	}
	if err := h.Handle(context.Background(), testJob(id)); err != nil {
		t.Fatal(err)
	}
	if st.dep.Status != store.StatusQueued {
		t.Fatalf("superseded job must not run, status = %q", st.dep.Status)
	}
}

func TestHandlerNoopsWhenAlreadyFailed(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, Name: "app", RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusFailed, FailedStage: "building"},
	}
	h := Handler{
		Store:   st,
		Fetcher: stubFetch{},
		Builder: boomBuild{},
		Runner:  &stubRun{},
		Router:  &stubRouter{},
	}
	if err := h.Handle(context.Background(), testJob(id)); err != nil {
		t.Fatal(err)
	}
	if st.dep.Status != store.StatusFailed {
		t.Fatalf("status = %q", st.dep.Status)
	}
}

func TestHandlerHappyPath(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, Name: "My Portfolio", RepositoryURL: store.SampleHelloURL},
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
	if st.dep.PublicURL != "http://my-portfolio.localhost:9080/" {
		t.Fatalf("url = %q", st.dep.PublicURL)
	}
	if st.dep.LocalHost != "my-portfolio" {
		t.Fatalf("local_host = %q", st.dep.LocalHost)
	}
	if rt.n != 1 {
		t.Fatalf("caddy applies = %d", rt.n)
	}
	if len(run.env) != 1 || run.env[0].Key != "GREETING" || run.env[0].Value != "hi" {
		t.Fatalf("env = %#v", run.env)
	}
	if st.dep.ContainerName != runtime.WorkloadName(st.app.Name, id) {
		t.Fatalf("container_name = %q", st.dep.ContainerName)
	}
}

func TestHandlerInjectsServiceLink(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	tid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee3"
	api := store.Application{ID: tid, ProjectID: pid, Name: "api", LocalHost: "api"}
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, Name: "web", RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
		env: []store.EnvVar{{Key: "GREETING", Value: "hi"}},
		apps: map[string]store.Application{
			tid: api,
		},
		links: []store.ApplicationLink{{
			SourceApplicationID: aid,
			TargetApplicationID: tid,
			EnvKey:              "VITE_API_URL",
		}},
	}
	run := &stubRun{}
	h := Handler{
		Store:     st,
		Fetcher:   stubFetch{},
		Builder:   stubBuild{},
		Runner:    run,
		Router:    &stubRouter{},
		ProxyBase: "http://127.0.0.1:9080",
		Health:    func(context.Context, int, time.Duration) error { return nil },
	}
	if err := h.Handle(context.Background(), testJob(id)); err != nil {
		t.Fatal(err)
	}
	var got string
	for _, p := range run.env {
		if p.Key == "VITE_API_URL" {
			got = p.Value
		}
	}
	if got != "http://api.localhost:9080/" {
		t.Fatalf("VITE_API_URL = %q env=%#v", got, run.env)
	}
}

func TestHandlerMountsNamedVolume(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, Name: "api", RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
		vols: []store.ApplicationVolume{
			{ApplicationID: aid, Name: "data", MountPath: "/data"},
		},
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
	if len(run.vols) != 1 || run.vols[0].Path != "/data" {
		t.Fatalf("vols = %#v", run.vols)
	}
	want, err := store.DockerVolumeName(aid, "data", store.EnvProduction)
	if err != nil {
		t.Fatal(err)
	}
	if run.vols[0].Name != want {
		t.Fatalf("docker name = %q want %q", run.vols[0].Name, want)
	}
}

func TestHandlerMountsPreviewVolumeIsolated(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, Name: "web", LocalHost: "hello", RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued, Environment: store.EnvPreview},
		vols: []store.ApplicationVolume{
			{ApplicationID: aid, Name: "data", MountPath: "/data"},
		},
	}
	run := &stubRun{}
	h := Handler{
		Store:     st,
		Fetcher:   stubFetch{},
		Builder:   stubBuild{},
		Runner:    run,
		Router:    &stubRouter{},
		ProxyBase: "http://127.0.0.1:9080",
		Health:    func(context.Context, int, time.Duration) error { return nil },
	}
	if err := h.Handle(context.Background(), testJob(id)); err != nil {
		t.Fatal(err)
	}
	if len(run.vols) != 1 || run.vols[0].Path != "/data" {
		t.Fatalf("preview must mount its own volume: %#v", run.vols)
	}
	prod, err := store.DockerVolumeName(aid, "data", store.EnvProduction)
	if err != nil {
		t.Fatal(err)
	}
	want, err := store.DockerVolumeName(aid, "data", store.EnvPreview)
	if err != nil {
		t.Fatal(err)
	}
	if run.vols[0].Name != want {
		t.Fatalf("preview docker name = %q want %q", run.vols[0].Name, want)
	}
	if run.vols[0].Name == prod {
		t.Fatal("preview mounted the production volume")
	}
	if st.dep.PublicURL != "http://hello-preview.localhost:9080/" {
		t.Fatalf("preview url = %q", st.dep.PublicURL)
	}
}

func TestHandlerRollingReusesVolumeWithoutStoppingFirst(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	prev := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeef"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
		vols: []store.ApplicationVolume{
			{ApplicationID: aid, Name: "data", MountPath: "/data"},
		},
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
	if len(run.stoppedAtRun) != 0 {
		t.Fatalf("rolling must keep previous container (and its volume) during run: %#v", run.stoppedAtRun)
	}
	want, err := store.DockerVolumeName(aid, "data", store.EnvProduction)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.vols) != 1 || run.vols[0].Name != want || run.vols[0].Path != "/data" {
		t.Fatalf("rolling must remount the same named volume: %#v", run.vols)
	}
	if len(run.stopped) == 0 {
		t.Fatal("expected previous container to be stopped after cutover")
	}
}

func TestHandlerFailedHealthDoesNotDropPreviousVolume(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	prev := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeef"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, Name: "web", RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
		vols: []store.ApplicationVolume{
			{ApplicationID: aid, Name: "data", MountPath: "/data"},
		},
		live: []store.Deployment{{
			ID:            prev,
			ProjectID:     pid,
			ApplicationID: aid,
			Status:        store.StatusLive,
			Environment:   store.EnvProduction,
			ContainerID:   "old",
			HostPort:      49100,
		}},
	}
	run := &stubRun{}
	h := Handler{
		Store:   st,
		Fetcher: stubFetch{},
		Builder: stubBuild{},
		Runner:  run,
		Router:  &stubRouter{},
		Health:  func(context.Context, int, time.Duration) error { return errors.New("health 503") },
	}
	if err := h.Handle(context.Background(), testJob(id)); err == nil {
		t.Fatal("expected health failure")
	}
	if len(st.live) != 1 || st.live[0].ID != prev || st.live[0].Status != store.StatusLive {
		t.Fatalf("previous live = %#v", st.live)
	}
	oldName := runtime.ResolveContainerName(prev, "")
	for _, name := range run.stopped {
		if name == oldName {
			t.Fatalf("must not stop previous LIVE (volume would stay but traffic would die): %#v", run.stopped)
		}
	}
	want, err := store.DockerVolumeName(aid, "data", store.EnvProduction)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.vols) != 1 || run.vols[0].Name != want {
		t.Fatalf("failed health still provisioned the same volume name: %#v", run.vols)
	}
}

func TestHandlerPreviewDoesNotStopProduction(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	prev := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeef"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, Name: "api", RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued, Environment: store.EnvPreview},
		live: []store.Deployment{{
			ID:            prev,
			ProjectID:     pid,
			ApplicationID: aid,
			Status:        store.StatusLive,
			Environment:   store.EnvProduction,
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
	if len(run.stopped) != 0 {
		t.Fatalf("preview must not stop production: %#v", run.stopped)
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
	if !strings.Contains(st.dep.BuildLog, "line1") || !strings.Contains(st.dep.BuildLog, "[detect]") {
		t.Fatalf("build_log = %q", st.dep.BuildLog)
	}
}

func TestHandlerRejectsOversizeWorkspace(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
	}
	var logs bytes.Buffer
	h := Handler{
		Log:               slog.New(slog.NewTextHandler(&logs, nil)),
		Store:             st,
		Fetcher:           stubFetch{},
		Builder:           stubBuild{},
		Runner:            &stubRun{},
		Router:            &stubRouter{},
		WorkspaceMaxBytes: 1,
		Health:            func(context.Context, int, time.Duration) error { return nil },
	}
	err := h.Handle(context.Background(), testJob(id))
	if err == nil {
		t.Fatal("expected workspace quota error")
	}
	var quota *workspaceQuotaError
	if !errors.As(err, &quota) {
		t.Fatalf("err = %v", err)
	}
	if quota.Limit != 1 || quota.Measured != int64(len("FROM scratch\n")) {
		t.Fatalf("quota = %+v", quota)
	}
	if len(quota.Largest) != 1 || quota.Largest[0].Rel != "Dockerfile" || quota.Largest[0].Size != quota.Measured {
		t.Fatalf("largest = %+v", quota.Largest)
	}
	msg := st.dep.ErrorMessage
	for _, want := range []string{
		"measured 13 bytes",
		"limit 1 bytes",
		"Dockerfile (13 bytes)",
		"not a report that the host filesystem is full",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
	if st.dep.Status != store.StatusFailed || st.dep.FailedStage != string(store.StatusDetecting) {
		t.Fatalf("status=%q stage=%q", st.dep.Status, st.dep.FailedStage)
	}
	text := logs.String()
	for _, want := range []string{
		"transferred_bytes=unavailable",
		"workspace_bytes=13",
		"context_bytes=unavailable",
		"image_bytes=unavailable",
		"detect_ms=unavailable",
		"build_ms=unavailable",
		"start_ms=unavailable",
		"health_ms=unavailable",
		"limit_bytes=1",
		"measured_bytes=13",
		"Dockerfile (13 bytes)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("log missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "transferred_bytes=0") {
		t.Fatalf("transfer size reported as zero:\n%s", text)
	}
}

func TestHandlerLogsStageMeasurements(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, Name: "My Portfolio", RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
	}
	var logs bytes.Buffer
	h := Handler{
		Log:       slog.New(slog.NewTextHandler(&logs, nil)),
		Store:     st,
		Fetcher:   stubFetch{},
		Builder:   stubBuild{},
		Runner:    &stubRun{},
		Router:    &stubRouter{},
		ProxyBase: "http://127.0.0.1:9080",
		Health:    func(context.Context, int, time.Duration) error { return nil },
	}
	if err := h.Handle(context.Background(), testJob(id)); err != nil {
		t.Fatal(err)
	}
	text := logs.String()
	for _, want := range []string{
		"msg=\"deploy measurements\"",
		"transferred_bytes=unavailable",
		"workspace_bytes=13",
		"context_bytes=unavailable",
		"image_bytes=unavailable",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("log missing %q:\n%s", want, text)
		}
	}
	for _, stage := range []string{"fetch_ms", "detect_ms", "build_ms", "start_ms", "health_ms", "total_ms"} {
		if strings.Contains(text, stage+"=unavailable") {
			t.Fatalf("%s unavailable on a finished deploy:\n%s", stage, text)
		}
		if !strings.Contains(text, stage+"=") {
			t.Fatalf("log missing %s:\n%s", stage, text)
		}
	}
}

type sizedBuild struct {
	stubBuild
	size int64
}

func (s sizedBuild) ImageBytes(context.Context, string) (int64, error) {
	return s.size, nil
}

func TestHandlerRecordsContextAndImageBytes(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, Name: "My Portfolio", RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
	}
	var logs bytes.Buffer
	h := Handler{
		Log:       slog.New(slog.NewTextHandler(&logs, nil)),
		Store:     st,
		Fetcher:   stubFetch{},
		Builder:   sizedBuild{stubBuild: stubBuild{log: "#4 transferring context: 2kB 0.1s done\n#3 transferring context: 263B 0.0s done\n"}, size: 4096},
		Runner:    &stubRun{},
		Router:    &stubRouter{},
		ProxyBase: "http://127.0.0.1:9080",
		Health:    func(context.Context, int, time.Duration) error { return nil },
	}
	if err := h.Handle(context.Background(), testJob(id)); err != nil {
		t.Fatal(err)
	}
	text := logs.String()
	for _, want := range []string{"context_bytes=2000", "image_bytes=4096", "transferred_bytes=unavailable"} {
		if !strings.Contains(text, want) {
			t.Fatalf("log missing %q:\n%s", want, text)
		}
	}
}

func TestCheckWorkspaceSizeReportsLargestFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "small.txt"), bytes.Repeat([]byte("a"), 10), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "big.bin"), bytes.Repeat([]byte("b"), 50), 0o600); err != nil {
		t.Fatal(err)
	}
	h := Handler{WorkspaceMaxBytes: 40}
	report, err := h.checkWorkspaceSize(dir)
	var quota *workspaceQuotaError
	if !errors.As(err, &quota) {
		t.Fatalf("err = %v", err)
	}
	if !report.Complete || quota.Measured != 60 || quota.Limit != 40 {
		t.Fatalf("report=%+v quota=%+v", report, quota)
	}
	if len(quota.Largest) != 2 || quota.Largest[0].Rel != "nested/big.bin" || quota.Largest[0].Size != 50 {
		t.Fatalf("largest = %+v", quota.Largest)
	}
	if quota.Largest[1].Rel != "small.txt" || quota.Largest[1].Size != 10 {
		t.Fatalf("second = %+v", quota.Largest[1])
	}
	if !strings.Contains(err.Error(), "nested/big.bin (50 bytes)") {
		t.Fatal(err)
	}
}

func TestCheckWorkspaceSizeKeepsLargestFew(t *testing.T) {
	dir := t.TempDir()
	var sum int64
	for i := 0; i < 8; i++ {
		n := i + 1
		sum += int64(n)
		name := fmt.Sprintf("f%d.bin", i)
		if err := os.WriteFile(filepath.Join(dir, name), bytes.Repeat([]byte("z"), n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h := Handler{WorkspaceMaxBytes: 1}
	_, err := h.checkWorkspaceSize(dir)
	var quota *workspaceQuotaError
	if !errors.As(err, &quota) {
		t.Fatal(err)
	}
	if quota.Measured != sum {
		t.Fatalf("measured = %d, want %d", quota.Measured, sum)
	}
	if len(quota.Largest) != workspaceReportFiles {
		t.Fatalf("kept %d files", len(quota.Largest))
	}
	if quota.Largest[0].Rel != "f7.bin" || quota.Largest[0].Size != 8 {
		t.Fatalf("top = %+v", quota.Largest[0])
	}
}

func TestCheckWorkspaceSizeSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	body := bytes.Repeat([]byte("x"), 20)
	real := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(real, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, "alias.txt")); err != nil {
		t.Skip("symlink not permitted: " + err.Error())
	}
	h := Handler{WorkspaceMaxBytes: 1000}
	report, err := h.checkWorkspaceSize(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Complete || report.Bytes != 20 {
		t.Fatalf("bytes = %d, symlink was followed or double-counted", report.Bytes)
	}
}

func TestRemoveGitMetadataDropsOversizedPack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pack := filepath.Join(dir, ".git", "objects", "pack")
	if err := os.MkdirAll(pack, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "pack.bin"), bytes.Repeat([]byte("p"), 80), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeGitMetadata(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Fatalf(".git still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	h := Handler{WorkspaceMaxBytes: 40}
	report, err := h.checkWorkspaceSize(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Complete || report.Bytes != 13 {
		t.Fatalf("bytes = %d, git metadata was still counted", report.Bytes)
	}
}

func TestWorkspaceQuotaStillRejectsOversizedApplicationFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.bin"), bytes.Repeat([]byte("m"), 80), 0o600); err != nil {
		t.Fatal(err)
	}
	pack := filepath.Join(dir, ".git", "objects", "pack")
	if err := os.MkdirAll(pack, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "pack.bin"), bytes.Repeat([]byte("p"), 200), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeGitMetadata(dir); err != nil {
		t.Fatal(err)
	}
	h := Handler{WorkspaceMaxBytes: 40}
	_, err := h.checkWorkspaceSize(dir)
	var quota *workspaceQuotaError
	if !errors.As(err, &quota) {
		t.Fatalf("err = %v", err)
	}
	if quota.Measured != 80 || quota.Limit != 40 {
		t.Fatalf("quota = %+v", quota)
	}
	if len(quota.Largest) != 1 || quota.Largest[0].Rel != "model.bin" {
		t.Fatalf("largest = %+v", quota.Largest)
	}
	if strings.Contains(err.Error(), ".git") {
		t.Fatal(err)
	}
}

func TestRemoveGitMetadataUnlinksSymlinkWithoutDeletingTarget(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	kept := filepath.Join(outside, "kept.txt")
	if err := os.WriteFile(kept, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".git")); err != nil {
		t.Skip("symlink not permitted: " + err.Error())
	}
	if err := removeGitMetadata(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("symlink target was removed: %v", err)
	}
}

func TestHandlerFailsWhenGitCleanupFails(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
	}
	built := false
	h := Handler{
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store:   st,
		Fetcher: stubFetch{},
		Builder: buildFlag{called: &built},
		Runner:  &stubRun{},
		Router:  &stubRouter{},
		gitCleanup: func(string) error {
			return errors.New("remove git metadata: access denied")
		},
	}
	err := h.Handle(context.Background(), testJob(id))
	if err == nil || !strings.Contains(err.Error(), "remove git metadata") {
		t.Fatalf("err = %v", err)
	}
	if built {
		t.Fatal("docker build ran after git cleanup failed")
	}
	if st.dep.Status != store.StatusFailed || st.dep.FailedStage != string(store.StatusDetecting) {
		t.Fatalf("status=%q stage=%q", st.dep.Status, st.dep.FailedStage)
	}
}

type nodeNoStartFetch struct{}

func (nodeNoStartFetch) Fetch(_ context.Context, _, dest string) error {
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	// Dependency-only manifest. No Dockerfile, so the Node pack runs.
	return os.WriteFile(filepath.Join(dest, "package.json"), []byte(`{"dependencies":{"mongodb":"^7.1.1","sharp":"^0.34.5"}}`), 0o600)
}

func TestHandlerFailsNodeWithoutStartDuringDetecting(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, RepositoryURL: "https://github.com/example/app"},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
	}
	built := false
	var logs bytes.Buffer
	h := Handler{
		Log:     slog.New(slog.NewTextHandler(&logs, nil)),
		Store:   st,
		Fetcher: nodeNoStartFetch{},
		Builder: buildFlag{called: &built},
		Runner:  &stubRun{},
		Router:  &stubRouter{},
	}
	err := h.Handle(context.Background(), testJob(id))
	if err == nil || !strings.Contains(err.Error(), "start script") {
		t.Fatalf("err = %v", err)
	}
	if built {
		t.Fatal("docker build ran after detection refused npm start")
	}
	if st.dep.Status != store.StatusFailed || st.dep.FailedStage != string(store.StatusDetecting) {
		t.Fatalf("status=%q stage=%q", st.dep.Status, st.dep.FailedStage)
	}
	if !strings.Contains(st.dep.ErrorMessage, "start script") || !strings.Contains(st.dep.ErrorMessage, "forge.json") {
		t.Fatalf("error_message = %q", st.dep.ErrorMessage)
	}
	text := logs.String()
	if !strings.Contains(text, "deployment failed") || !strings.Contains(text, "stage=detecting") || !strings.Contains(text, "start script") {
		t.Fatalf("log = %s", text)
	}
}

type gitPackFetch struct{}

func (gitPackFetch) Fetch(_ context.Context, _, dest string) error {
	pack := filepath.Join(dest, ".git", "objects", "pack")
	if err := os.MkdirAll(pack, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(pack, "pack.bin"), bytes.Repeat([]byte("p"), 80), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dest, "Dockerfile"), []byte("FROM scratch\n"), 0o600)
}

func TestHandlerIgnoresOversizedGitMetadata(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, RepositoryURL: "https://github.com/example/app"},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
	}
	var logs bytes.Buffer
	h := Handler{
		Log:               slog.New(slog.NewTextHandler(&logs, nil)),
		Store:             st,
		Fetcher:           gitPackFetch{},
		Builder:           stubBuild{},
		Runner:            &stubRun{},
		Router:            &stubRouter{},
		WorkspaceMaxBytes: 40,
		Health:            func(context.Context, int, time.Duration) error { return nil },
	}
	if err := h.Handle(context.Background(), testJob(id)); err != nil {
		t.Fatal(err)
	}
	if st.dep.Status != store.StatusLive {
		t.Fatalf("status=%q err=%q", st.dep.Status, st.dep.ErrorMessage)
	}
	if !strings.Contains(logs.String(), "workspace_bytes=13") {
		t.Fatalf("log = %s", logs.String())
	}
}

type buildFlag struct{ called *bool }

func (b buildFlag) Build(context.Context, string, string, string, []runtime.EnvPair) (string, error) {
	*b.called = true
	return "", nil
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

func TestHandlerStopsPreviousLiveAfterHealth(t *testing.T) {
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
	if len(run.stoppedAtRun) != 0 {
		t.Fatalf("previous container must stay up during docker run: %#v", run.stoppedAtRun)
	}
	if len(run.stopped) == 0 {
		t.Fatal("expected previous container to be stopped after a healthy cutover")
	}
	if st.dep.Status != store.StatusLive {
		t.Fatalf("status = %q", st.dep.Status)
	}
}

func TestHandlerFailedHealthLeavesPreviousLive(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	prev := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeef"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, Name: "web", RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
		live: []store.Deployment{{
			ID:            prev,
			ProjectID:     pid,
			ApplicationID: aid,
			Status:        store.StatusLive,
			Environment:   store.EnvProduction,
			ContainerID:   "old",
			HostPort:      49100,
		}},
	}
	run := &stubRun{}
	h := Handler{
		Store:   st,
		Fetcher: stubFetch{},
		Builder: stubBuild{},
		Runner:  run,
		Router:  &stubRouter{},
		Health:  func(context.Context, int, time.Duration) error { return errors.New("health 503") },
	}
	if err := h.Handle(context.Background(), testJob(id)); err == nil {
		t.Fatal("expected health failure")
	}
	if st.dep.Status != store.StatusFailed {
		t.Fatalf("new status = %q", st.dep.Status)
	}
	if len(st.live) != 1 || st.live[0].ID != prev || st.live[0].Status != store.StatusLive {
		t.Fatalf("previous live = %#v", st.live)
	}
	oldName := runtime.ResolveContainerName(prev, "")
	for _, name := range run.stopped {
		if name == oldName {
			t.Fatalf("must not stop previous LIVE on failed health: %#v", run.stopped)
		}
	}
}

func TestHandlerRouteKeepsOtherEnvironment(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	prodID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeef"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, Name: "web", LocalHost: "hello", RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued, Environment: store.EnvPreview},
		live: []store.Deployment{{
			ID:            prodID,
			ProjectID:     pid,
			ApplicationID: aid,
			Status:        store.StatusLive,
			Environment:   store.EnvProduction,
			HostPort:      49100,
			LocalHost:     "hello",
		}},
	}
	rt := &stubRouter{}
	h := Handler{
		Store:     st,
		Fetcher:   stubFetch{},
		Builder:   stubBuild{},
		Runner:    &stubRun{},
		Router:    rt,
		ProxyBase: "http://127.0.0.1:9080",
		Health:    func(context.Context, int, time.Duration) error { return nil },
	}
	if err := h.Handle(context.Background(), testJob(id)); err != nil {
		t.Fatal(err)
	}
	foundProd := false
	foundPrev := false
	for _, d := range rt.last {
		if d.ID == prodID {
			foundProd = true
		}
		if d.ID == id {
			foundPrev = true
		}
	}
	if !foundProd || !foundPrev {
		t.Fatalf("caddy live = %#v", rt.last)
	}
}

type boomFetch struct{}

func (boomFetch) Fetch(context.Context, string, string) error {
	return errors.New("must not fetch on rollback")
}

type boomBuild struct{}

func (boomBuild) Build(context.Context, string, string, string, []runtime.EnvPair) (string, error) {
	return "", errors.New("must not build on rollback")
}

type deadRun struct{ stubRun }

func (deadRun) Inspect(context.Context, string) (runtime.ContainerState, error) {
	return runtime.ContainerState{Exists: true, Running: false, Status: "exited", ExitCode: 1}, nil
}

func TestHandlerFailsWhenContainerExits(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, Name: "demo", RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
	}
	run := &deadRun{}
	h := Handler{
		Store:   st,
		Fetcher: stubFetch{},
		Builder: stubBuild{},
		Runner:  run,
		Router:  &stubRouter{},
		Health:  func(context.Context, int, time.Duration) error { return errors.New("health must not run") },
	}
	if err := h.Handle(context.Background(), testJob(id)); err == nil {
		t.Fatal("expected error")
	}
	if st.dep.Status != store.StatusFailed || st.dep.FailedStage != string(store.StatusDeploying) {
		t.Fatalf("status=%q stage=%q", st.dep.Status, st.dep.FailedStage)
	}
	if !strings.Contains(st.dep.ErrorMessage, "process startup failed") || strings.Contains(st.dep.ErrorMessage, "readiness check failed") {
		t.Fatalf("err = %q", st.dep.ErrorMessage)
	}
	if len(run.stopped) == 0 {
		t.Fatal("failed container must be removed")
	}
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
