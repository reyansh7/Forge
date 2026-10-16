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

type memVolumes struct {
	mu   sync.Mutex
	byID map[string]store.ApplicationVolume
	seq  int
}

func newMemVolumes() *memVolumes {
	return &memVolumes{byID: map[string]store.ApplicationVolume{}}
}

func (m *memVolumes) CreateApplicationVolume(_ context.Context, applicationID, name, mountPath string) (store.ApplicationVolume, error) {
	name, err := store.ValidateVolumeName(name)
	if err != nil {
		return store.ApplicationVolume{}, err
	}
	mountPath, err = store.ValidateMountPath(mountPath)
	if err != nil {
		return store.ApplicationVolume{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, v := range m.byID {
		if v.ApplicationID != applicationID {
			continue
		}
		n++
		if v.Name == name || v.MountPath == mountPath {
			return store.ApplicationVolume{}, store.ErrConflict
		}
		if mountsOverlapHTTP(v.MountPath, mountPath) {
			return store.ApplicationVolume{}, errString("mount path overlaps " + v.MountPath)
		}
	}
	if n >= 8 {
		return store.ApplicationVolume{}, errString("at most 8 volumes per application")
	}
	m.seq++
	v := store.ApplicationVolume{
		ID:            "eeeeeeee-eeee-4000-8000-00000000000" + itoaDigit(m.seq),
		ApplicationID: applicationID,
		Name:          name,
		MountPath:     mountPath,
	}
	m.byID[v.ID] = v
	return v, nil
}

func mountsOverlapHTTP(a, b string) bool {
	return a == b || (len(a) > 0 && (startsWithPath(a, b) || startsWithPath(b, a)))
}

func startsWithPath(p, prefix string) bool {
	return len(p) > len(prefix) && p[:len(prefix)] == prefix && p[len(prefix)] == '/'
}

func (m *memVolumes) ListApplicationVolumes(_ context.Context, applicationID string) ([]store.ApplicationVolume, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.ApplicationVolume, 0)
	for _, v := range m.byID {
		if v.ApplicationID == applicationID {
			out = append(out, v)
		}
	}
	return out, nil
}

func (m *memVolumes) GetApplicationVolume(_ context.Context, applicationID, volumeID string) (store.ApplicationVolume, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.byID[volumeID]
	if !ok || v.ApplicationID != applicationID {
		return store.ApplicationVolume{}, store.ErrNotFound
	}
	return v, nil
}

func (m *memVolumes) DeleteApplicationVolume(_ context.Context, applicationID, volumeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.byID[volumeID]
	if !ok || v.ApplicationID != applicationID {
		return store.ErrNotFound
	}
	delete(m.byID, volumeID)
	return nil
}

func TestCreateVolumeNamedOnly(t *testing.T) {
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
	srv.Volumes = newMemVolumes()
	rec := httptest.NewRecorder()
	body := `{"name":"data","mount_path":"/data"}`
	req := httptest.NewRequest(http.MethodPost, "/applications/"+app.ID+"/volumes", bytes.NewReader([]byte(body)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got volumeResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.MountPath != "/data" || got.Name != "data" {
		t.Fatalf("%+v", got)
	}
	want, err := store.DockerVolumeName(app.ID, "data", store.EnvProduction)
	if err != nil {
		t.Fatal(err)
	}
	if got.DockerName != want {
		t.Fatalf("docker_name = %q want %q", got.DockerName, want)
	}
	preview, err := store.DockerVolumeName(app.ID, "data", store.EnvPreview)
	if err != nil {
		t.Fatal(err)
	}
	if got.PreviewDockerName != preview {
		t.Fatalf("preview_docker_name = %q want %q", got.PreviewDockerName, preview)
	}
	if got.DockerName == got.PreviewDockerName {
		t.Fatal("preview docker name must be distinct")
	}
}

func TestCreateVolumeRejectsHostPath(t *testing.T) {
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
	srv.Volumes = newMemVolumes()
	rec := httptest.NewRecorder()
	body := `{"name":"sock","mount_path":"/var/run/docker.sock"}`
	req := httptest.NewRequest(http.MethodPost, "/applications/"+app.ID+"/volumes", bytes.NewReader([]byte(body)))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteVolumeWhileLiveConflicts(t *testing.T) {
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
	deps := newMemDeployments()
	live := store.Deployment{
		ID:            "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ApplicationID: app.ID,
		ProjectID:     p.ID,
		Status:        store.StatusLive,
		CreatedAt:     time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC),
	}
	deps.byID[live.ID] = live
	vols := newMemVolumes()
	v, err := vols.CreateApplicationVolume(context.Background(), app.ID, "data", "/data")
	if err != nil {
		t.Fatal(err)
	}
	srv := appServer(mem, apps, deps)
	srv.Volumes = vols
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/applications/"+app.ID+"/volumes/"+v.ID, nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateVolumeRequiresAuth(t *testing.T) {
	srv := &Server{Postgres: stubPing{}, Redis: stubPing{}, Volumes: newMemVolumes()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/applications/00000000-0000-4000-8000-000000000001/volumes", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestDeleteVolumeWhilePreviewLiveConflicts(t *testing.T) {
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
	deps := newMemDeployments()
	live := store.Deployment{
		ID:            "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ApplicationID: app.ID,
		ProjectID:     p.ID,
		Status:        store.StatusLive,
		Environment:   store.EnvPreview,
		CreatedAt:     time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC),
	}
	deps.byID[live.ID] = live
	vols := newMemVolumes()
	v, err := vols.CreateApplicationVolume(context.Background(), app.ID, "data", "/data")
	if err != nil {
		t.Fatal(err)
	}
	srv := appServer(mem, apps, deps)
	srv.Volumes = vols
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/applications/"+app.ID+"/volumes/"+v.ID, nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

type recordingVolumeRuntime struct {
	stubRuntime
	removed []string
}

func (r *recordingVolumeRuntime) RemoveVolume(_ context.Context, name string) error {
	r.removed = append(r.removed, name)
	return nil
}

func TestDeleteVolumeRemovesPreviewAndProductionNames(t *testing.T) {
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
	vols := newMemVolumes()
	v, err := vols.CreateApplicationVolume(context.Background(), app.ID, "data", "/data")
	if err != nil {
		t.Fatal(err)
	}
	rt := &recordingVolumeRuntime{}
	srv := appServer(mem, apps, newMemDeployments())
	srv.Volumes = vols
	srv.Runtime = rt
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/applications/"+app.ID+"/volumes/"+v.ID, nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	want, err := store.DockerVolumeNames(app.ID, "data")
	if err != nil {
		t.Fatal(err)
	}
	if len(rt.removed) != 2 || rt.removed[0] != want[0] || rt.removed[1] != want[1] {
		t.Fatalf("removed = %#v want %#v", rt.removed, want)
	}
}
