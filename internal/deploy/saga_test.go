package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/reyansh7/Forge/internal/queue"
	"github.com/reyansh7/Forge/internal/runtime"
	"github.com/reyansh7/Forge/internal/store"
)

func TestRepairGoPackageAcceptsOneCommand(t *testing.T) {
	pkg, ok := RepairGoPackage("no Go files in /app\n./cmd/api\n")
	if !ok || pkg != "./cmd/api" {
		t.Fatalf("got %q %v", pkg, ok)
	}
	if _, ok := RepairGoPackage("Several main packages: ./cmd/api, ./cmd/worker"); ok {
		t.Fatal("two commands must not be repaired")
	}
	if _, ok := RepairGoPackage("dockerfile failed"); ok {
		t.Fatal("unrelated error")
	}
}

func TestDependenciesAndCompensationOrder(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	view := store.SagaView{
		InSaga:      true,
		ServiceName: "frontend",
		Siblings: []store.SagaSibling{
			{Name: "backend", Status: "building"},
			{Name: "frontend", Status: "queued"},
		},
	}
	if !dependenciesPending(view) {
		t.Fatal("frontend must wait for backend")
	}
	view.Siblings[0].Status = string(store.StatusFailed)
	if dependenciesPending(view) {
		t.Fatal("a failed dependency is not a wait; promotion is refused later")
	}
	if exposePublic("api", "", "") || !exposePublic("frontend", "", "") || !exposePublic("api", "api", "") || exposePublic("ml-model", "", "private") || !exposePublic("ml-model", "", "public") {
		t.Fatal("private unless frontend or explicit host")
	}
	view = store.SagaView{InSaga: true, Siblings: []store.SagaSibling{
		{ServiceID: "s1", Name: "ml", DeploymentID: "d1", PreviousDeploymentID: "p1", PromotedAt: now},
		{ServiceID: "s2", Name: "api", DeploymentID: "d2", PreviousDeploymentID: "p2", PromotedAt: now.Add(time.Minute)},
		{ServiceID: "s3", Name: "web", DeploymentID: "d3"},
	}}
	steps := compensationOrder(view, "d3")
	if len(steps) != 2 || steps[0].Name != "api" || steps[1].Name != "ml" {
		t.Fatalf("%+v", steps)
	}
}

func TestServiceLinkURLKeepsPublicAddress(t *testing.T) {
	pub := store.Application{ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2", Name: "frontend", LocalHost: "web"}
	if got := serviceLinkURL("http://127.0.0.1:9080", pub, 8080, "API_URL"); got != "http://web.localhost:9080/" {
		t.Fatalf("public %s", got)
	}
	priv := store.Application{ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee3", Name: "api"}
	if got := serviceLinkURL("http://127.0.0.1:9080", priv, 5000, "ML_SERVICE_URL"); got != "http://api:5000/" {
		t.Fatalf("private %s", got)
	}
	if store.BridgeAlias("api", "", priv.ID) != "api" {
		t.Fatal("bridge alias must stay api so a baked nginx proxy can resolve it")
	}
	if store.SuggestedLocalHost("api", priv.ID) == "api" {
		t.Fatal("public slug must not be the reserved label api")
	}
}

func TestBrowserLinkUsesProxyAndOneSlash(t *testing.T) {
	api := store.Application{ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee3", Name: "backend"}
	got := serviceLinkURL("http://127.0.0.1:9080", api, 8000, "NEXT_PUBLIC_API_URL")
	if strings.Contains(got, ":8000") || strings.Contains(got, "backend:") {
		t.Fatalf("browser URL used container DNS: %s", got)
	}
	if got != "http://backend.localhost:9080/" {
		t.Fatalf("public %s", got)
	}
	if strings.HasSuffix(got, "//") || !strings.HasSuffix(got, "/") {
		t.Fatalf("trailing slash %s", got)
	}
	internal := serviceLinkURL("http://127.0.0.1:9080", api, 8000, "ML_SERVICE_URL")
	if internal != "http://backend:8000/" {
		t.Fatalf("internal %s", internal)
	}
	front := store.Application{ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee4", Name: "frontend", LocalHost: "frontend"}
	origin := serviceLinkURL("http://127.0.0.1:9080", front, 3000, "CORS_ORIGIN")
	if origin != "http://frontend.localhost:9080" {
		t.Fatalf("origin %s", origin)
	}
	joined := store.JoinPublicURL(got, "/api/resumes")
	if joined != "http://backend.localhost:9080/api/resumes" {
		t.Fatalf("join %s", joined)
	}
}

func TestExplicitDependsOnDoesNotWaitOnUnlistedSibling(t *testing.T) {
	plan, err := json.Marshal(map[string]any{
		"services": []map[string]any{
			{"name": "frontend", "depends_on": []string{"backend"}},
			{"name": "backend", "depends_on": []string{"ml-model"}},
			{"name": "ml-model"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	view := store.SagaView{InSaga: true, ServiceName: "frontend", Plan: plan, Siblings: []store.SagaSibling{
		{Name: "ml-model", Status: string(store.StatusFailed)},
		{Name: "backend", Status: string(store.StatusLive)},
	}}
	if dependencyFailed(view) != "" {
		t.Fatal("frontend lists only the backend")
	}
	view.Siblings[1].Status = string(store.StatusFailed)
	if dependencyFailed(view) != "backend" {
		t.Fatalf("got %s", dependencyFailed(view))
	}
}

func TestDependencyFailedStopsLaterBuild(t *testing.T) {
	mlDown := store.SagaView{InSaga: true, ServiceName: "backend", Siblings: []store.SagaSibling{
		{Name: "ml-model", Status: string(store.StatusFailed)},
	}}
	if dependencyFailed(mlDown) != "ml-model" {
		t.Fatal("backend must not build after ML failed")
	}
	mlDown.Siblings[0].Status = string(store.StatusLive)
	if dependencyFailed(mlDown) != "" {
		t.Fatal("a live ML service is not a build stop")
	}
	front := store.SagaView{InSaga: true, ServiceName: "frontend", Siblings: []store.SagaSibling{
		{Name: "backend", Status: string(store.StatusFailed)},
	}}
	if dependencyFailed(front) != "backend" {
		t.Fatal("frontend must not build after the backend failed")
	}
	if !dependenciesPending(store.SagaView{InSaga: true, ServiceName: "backend", Siblings: []store.SagaSibling{
		{Name: "ml-model", Status: string(store.StatusHealthCheck)},
	}}) {
		t.Fatal("backend must wait until ML is ready")
	}
}

type sagaMem struct {
	*mem
	view     store.SagaView
	byID     map[string]store.Deployment
	commit   bool
	stops    []store.Deployment
	comp     map[string]string
	compErr  map[string]string
	restored []string
}

func (s *sagaMem) SagaView(context.Context, string) (store.SagaView, error) {
	return s.view, nil
}

func (s *sagaMem) MarkPromoted(_ context.Context, _, previousID string) error {
	s.view.Siblings = append(s.view.Siblings, store.SagaSibling{PreviousDeploymentID: previousID, PromotedAt: time.Now()})
	return nil
}

func (s *sagaMem) CommitIfReady(context.Context, string) ([]store.Deployment, bool, error) {
	if !s.commit {
		return nil, false, nil
	}
	s.commit = false
	return s.stops, true, nil
}

func (s *sagaMem) RecordCompensation(_ context.Context, serviceID, state, message string) error {
	if s.comp == nil {
		s.comp = map[string]string{}
		s.compErr = map[string]string{}
	}
	s.comp[serviceID] = state
	s.compErr[serviceID] = message
	return nil
}

func (s *sagaMem) RestorePrevious(_ context.Context, _, previousID string) error {
	s.restored = append(s.restored, previousID)
	if prev, ok := s.byID[previousID]; ok {
		prev.Status = store.StatusLive
		s.live = []store.Deployment{prev}
	}
	return nil
}

func (s *sagaMem) SetBuildPackage(context.Context, string, string) error { return nil }

func (s *sagaMem) MarkProjectDeployFailed(context.Context, string, string) error { return nil }

func (s *sagaMem) ProjectDeployBlockReason(context.Context, string) (string, error) {
	return "", nil
}

func (s *sagaMem) GetDeployment(ctx context.Context, id string) (store.Deployment, error) {
	if d, ok := s.byID[id]; ok {
		return d, nil
	}
	return s.mem.GetDeployment(ctx, id)
}

type joinRun struct {
	*stubRun
	network string
	alias   string
	down    map[string]bool
}

func (j *joinRun) EnsureNetwork(_ context.Context, name string) error {
	j.network = name
	return nil
}

func (j *joinRun) RunJoined(_ context.Context, image, containerName string, env []runtime.EnvPair, containerPort int, vols []runtime.VolumeMount, network, alias string) (runtime.Instance, error) {
	j.network = network
	j.alias = alias
	return j.Run(context.Background(), image, containerName, env, containerPort, vols)
}

func (j *joinRun) Inspect(_ context.Context, name string) (runtime.ContainerState, error) {
	if j.down[name] {
		return runtime.ContainerState{Exists: true, Running: false, Status: "exited"}, nil
	}
	return runtime.ContainerState{Exists: true, Running: true, Status: "running"}, nil
}

func TestSagaHoldsPreviousContainerUntilCommit(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	prev := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeef"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &sagaMem{
		mem: &mem{
			app: store.Application{ID: aid, ProjectID: pid, Name: "frontend", RepositoryURL: store.SampleHelloURL},
			dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
			live: []store.Deployment{{
				ID: prev, ProjectID: pid, ApplicationID: aid, Status: store.StatusLive, ContainerName: "old",
			}},
		},
		view: store.SagaView{InSaga: true, ProjectID: pid, ServiceName: "frontend", Siblings: []store.SagaSibling{
			{Name: "frontend", Status: "queued"},
			{Name: "api", Status: string(store.StatusLive)},
		}},
	}
	run := &joinRun{stubRun: &stubRun{}}
	h := Handler{
		Store: st, Fetcher: stubFetch{}, Builder: stubBuild{}, Runner: run, Router: &stubRouter{},
		Health: func(context.Context, int, time.Duration) error { return nil },
	}
	if err := h.Handle(context.Background(), testJob(id)); err != nil {
		t.Fatal(err)
	}
	if run.network != "forgep-aaaaaaaabbbb" || run.alias == "" {
		t.Fatalf("network %q alias %q", run.network, run.alias)
	}
	oldName := runtime.ResolveContainerName(prev, "old")
	for _, name := range run.stopped {
		if name == oldName {
			t.Fatalf("previous container stopped before saga commit: %#v", run.stopped)
		}
	}
	if st.dep.Status != store.StatusLive {
		t.Fatalf("status %s", st.dep.Status)
	}
}

func TestSagaCommitStopsHeldContainers(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	prev := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeef"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &sagaMem{
		mem: &mem{
			app: store.Application{ID: aid, ProjectID: pid, Name: "frontend", RepositoryURL: store.SampleHelloURL},
			dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
			live: []store.Deployment{{
				ID: prev, ApplicationID: aid, Status: store.StatusLive, ContainerName: "old",
			}},
		},
		view:   store.SagaView{InSaga: true, ProjectID: pid, ServiceName: "frontend"},
		commit: true,
		stops:  []store.Deployment{{ID: prev, ContainerName: "old"}},
	}
	run := &joinRun{stubRun: &stubRun{}}
	h := Handler{
		Store: st, Fetcher: stubFetch{}, Builder: stubBuild{}, Runner: run, Router: &stubRouter{},
		Health: func(context.Context, int, time.Duration) error { return nil },
	}
	if err := h.Handle(context.Background(), testJob(id)); err != nil {
		t.Fatal(err)
	}
	oldName := runtime.ResolveContainerName(prev, "old")
	found := false
	for _, name := range run.stopped {
		if name == oldName {
			found = true
		}
	}
	if !found {
		t.Fatalf("commit should stop held container, stopped %#v", run.stopped)
	}
}

func TestPrivateServiceSkipsCaddy(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &sagaMem{
		mem: &mem{
			app: store.Application{ID: aid, ProjectID: pid, Name: "api", RepositoryURL: store.SampleHelloURL},
			dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
		},
		view: store.SagaView{InSaga: true, ProjectID: pid, ServiceName: "api"},
	}
	rt := &stubRouter{}
	h := Handler{
		Store: st, Fetcher: stubFetch{}, Builder: stubBuild{}, Runner: &joinRun{stubRun: &stubRun{}}, Router: rt,
		Health: func(context.Context, int, time.Duration) error { return nil },
	}
	if err := h.Handle(context.Background(), testJob(id)); err != nil {
		t.Fatal(err)
	}
	if rt.n != 0 {
		t.Fatalf("private service was routed %d", rt.n)
	}
	if st.dep.PublicURL == "" || st.dep.PublicURL == "http://api.localhost:9080/" {
		t.Fatalf("url %q", st.dep.PublicURL)
	}
}

func TestCompensationRestoresNewestFirstAndRecordsFailure(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	newID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee10"
	prevID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee11"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	prev := store.Deployment{ID: prevID, Status: store.StatusStopped, ContainerName: "prev", HostPort: 1}
	promoted := store.Deployment{ID: newID, Status: store.StatusLive, ContainerName: "promoted", HostPort: 2}
	st := &sagaMem{
		mem: &mem{
			app: store.Application{ID: aid, ProjectID: pid, Name: "frontend", RepositoryURL: store.SampleHelloURL},
			dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
		},
		view: store.SagaView{InSaga: true, ProjectID: pid, ServiceName: "frontend", Siblings: []store.SagaSibling{{
			ServiceID: "bbbbbbbb-bbbb-4000-8000-000000000001", Name: "api", Status: string(store.StatusLive),
			DeploymentID: newID, PreviousDeploymentID: prevID, PromotedAt: now,
		}}},
		byID: map[string]store.Deployment{prevID: prev, newID: promoted},
	}
	run := &joinRun{stubRun: &stubRun{}, down: map[string]bool{
		runtime.ResolveContainerName(prevID, "prev"): true,
	}}
	h := Handler{
		Store: st, Fetcher: stubFetch{}, Builder: stubBuild{}, Runner: run, Router: &stubRouter{},
		Health: func(context.Context, int, time.Duration) error { return errors.New("health failed") },
	}
	if err := h.Handle(context.Background(), testJob(id)); err == nil {
		t.Fatal("expected health failure")
	}
	if st.comp["bbbbbbbb-bbbb-4000-8000-000000000001"] != "failed" {
		t.Fatalf("comp %+v", st.comp)
	}
	msg := st.compErr["bbbbbbbb-bbbb-4000-8000-000000000001"]
	if !strings.Contains(msg, "not running") || len(st.restored) != 0 {
		t.Fatalf("err %q restored %#v", msg, st.restored)
	}
	promotedName := runtime.ResolveContainerName(newID, "promoted")
	for _, name := range run.stopped {
		if name == promotedName {
			t.Fatalf("promoted container stopped even though the previous one is gone: %#v", run.stopped)
		}
	}
}

func TestCompensationRestoresRunningPrevious(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	newID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee10"
	prevID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee11"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &sagaMem{
		mem: &mem{
			app: store.Application{ID: aid, ProjectID: pid, Name: "frontend", RepositoryURL: store.SampleHelloURL},
			dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
		},
		view: store.SagaView{InSaga: true, ProjectID: pid, ServiceName: "frontend", Siblings: []store.SagaSibling{{
			ServiceID: "bbbbbbbb-bbbb-4000-8000-000000000001", Name: "api", Status: string(store.StatusLive),
			DeploymentID: newID, PreviousDeploymentID: prevID, PromotedAt: time.Now(),
		}}},
		byID: map[string]store.Deployment{
			prevID: {ID: prevID, Status: store.StatusStopped, ContainerName: "prev"},
			newID:  {ID: newID, Status: store.StatusLive, ContainerName: "promoted"},
		},
	}
	run := &joinRun{stubRun: &stubRun{}}
	rt := &stubRouter{}
	h := Handler{
		Store: st, Fetcher: stubFetch{}, Builder: stubBuild{}, Runner: run, Router: rt,
		Health: func(context.Context, int, time.Duration) error { return errors.New("unhealthy") },
	}
	if err := h.Handle(context.Background(), testJob(id)); err == nil {
		t.Fatal("expected failure")
	}
	if st.comp["bbbbbbbb-bbbb-4000-8000-000000000001"] != "restored" || len(st.restored) != 1 {
		t.Fatalf("comp %+v restored %#v err %+v", st.comp, st.restored, st.compErr)
	}
	if rt.n == 0 {
		t.Fatal("expected caddy restore")
	}
	promotedName := runtime.ResolveContainerName(newID, "promoted")
	prevName := runtime.ResolveContainerName(prevID, "prev")
	stoppedPromoted, stoppedPrev := false, false
	for _, name := range run.stopped {
		if name == promotedName {
			stoppedPromoted = true
		}
		if name == prevName {
			stoppedPrev = true
		}
	}
	if !stoppedPromoted || stoppedPrev {
		t.Fatalf("stopped %#v", run.stopped)
	}
}

func TestCompensationRecordsRoutingFailure(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	newID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee10"
	prevID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee11"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &sagaMem{
		mem: &mem{
			app: store.Application{ID: aid, ProjectID: pid, Name: "frontend", RepositoryURL: store.SampleHelloURL},
			dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
		},
		view: store.SagaView{InSaga: true, ProjectID: pid, ServiceName: "frontend", Siblings: []store.SagaSibling{{
			ServiceID: "bbbbbbbb-bbbb-4000-8000-000000000001", Name: "api", Status: string(store.StatusLive),
			DeploymentID: newID, PreviousDeploymentID: prevID, PromotedAt: time.Now(),
		}}},
		byID: map[string]store.Deployment{
			prevID: {ID: prevID, Status: store.StatusStopped, ContainerName: "prev"},
			newID:  {ID: newID, Status: store.StatusLive, ContainerName: "promoted"},
		},
	}
	run := &joinRun{stubRun: &stubRun{}}
	h := Handler{
		Store: st, Fetcher: stubFetch{}, Builder: stubBuild{}, Runner: run,
		Router: &stubRouter{err: errors.New("caddy rejected the config")},
		Health: func(context.Context, int, time.Duration) error { return errors.New("unhealthy") },
	}
	if err := h.Handle(context.Background(), testJob(id)); err == nil {
		t.Fatal("expected failure")
	}
	msg := st.compErr["bbbbbbbb-bbbb-4000-8000-000000000001"]
	if st.comp["bbbbbbbb-bbbb-4000-8000-000000000001"] != "failed" || !strings.Contains(msg, "routing") {
		t.Fatalf("comp %+v err %q", st.comp, msg)
	}
	promotedName := runtime.ResolveContainerName(newID, "promoted")
	for _, name := range run.stopped {
		if name == promotedName {
			t.Fatalf("promoted container stopped after routing restore failed: %#v", run.stopped)
		}
	}
}

func TestDeferredUntilDependencyReady(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &sagaMem{
		mem: &mem{
			app: store.Application{ID: aid, ProjectID: pid, Name: "frontend", RepositoryURL: store.SampleHelloURL},
			dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
		},
		view: store.SagaView{InSaga: true, ServiceName: "frontend", Siblings: []store.SagaSibling{
			{Name: "frontend", Status: "queued"},
			{Name: "api", Status: "building"},
		}},
	}
	b := &callsBuild{}
	h := Handler{Store: st, Fetcher: stubFetch{}, Builder: b, Runner: &stubRun{}, Router: &stubRouter{}}
	err := h.Handle(context.Background(), testJob(id))
	if !errors.Is(err, queue.ErrDefer) {
		t.Fatal(err)
	}
	if b.calls != 0 || st.dep.Status != store.StatusQueued {
		t.Fatalf("calls %d status %s", b.calls, st.dep.Status)
	}
}

type callsBuild struct{ calls int }

func (b *callsBuild) Build(context.Context, string, string, string, []runtime.EnvPair) (string, error) {
	b.calls++
	return "", nil
}

type retryBuild struct{ calls int }

func (b *retryBuild) Build(context.Context, string, string, string, []runtime.EnvPair) (string, error) {
	b.calls++
	if b.calls == 1 {
		return "no Go files in /app\n./cmd/api\n", errors.New("docker build failed")
	}
	return "rebuilt", nil
}

func TestBuildRepairRetriesOnce(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	pid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	aid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	st := &mem{
		app: store.Application{ID: aid, ProjectID: pid, RepositoryURL: store.SampleHelloURL},
		dep: store.Deployment{ID: id, ProjectID: pid, ApplicationID: aid, Status: store.StatusQueued},
	}
	b := &retryBuild{}
	h := Handler{
		Store: st, Fetcher: stubFetch{}, Builder: b, Runner: &stubRun{}, Router: &stubRouter{},
		Health: func(context.Context, int, time.Duration) error { return nil },
	}
	if err := h.Handle(context.Background(), testJob(id)); err != nil {
		t.Fatal(err)
	}
	if b.calls != 2 || st.dep.Status != store.StatusLive {
		t.Fatalf("calls %d status %s log %s", b.calls, st.dep.Status, st.dep.BuildLog)
	}
	if !strings.Contains(st.dep.BuildLog, "repair: set build_package ./cmd/api") {
		t.Fatalf("log %s", st.dep.BuildLog)
	}
}
