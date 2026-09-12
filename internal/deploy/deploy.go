// Package deploy is the worker pipeline for one application deployment.
//
// Flow: load application → fetch its repo (unless rollback) → confine
// root_directory → detect → build → run with env vars → health check on
// health_path → update Caddy → LIVE.
//
// Rollback copies image_name from a prior row and skips fetch/build so
// a client cannot supply an image or a host command.
//
// User code is never exec'd on the host. Git/docker are control-plane
// tools; the repository runs inside `docker build` / `docker run`.
package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/reyansh7/Forge/internal/config"
	"github.com/reyansh7/Forge/internal/detect"
	"github.com/reyansh7/Forge/internal/proxy"
	"github.com/reyansh7/Forge/internal/queue"
	"github.com/reyansh7/Forge/internal/runtime"
	"github.com/reyansh7/Forge/internal/store"
)

// Store is the durable application + deployment boundary for the worker.
//
// The worker fetches the application's repository_url, not the project's.
// Two apps in one project may both be LIVE; replace is per application.
type Store interface {
	GetApplication(ctx context.Context, id string) (store.Application, error)
	ListEnvVars(ctx context.Context, applicationID string) ([]store.EnvVar, error)
	GetDeployment(ctx context.Context, id string) (store.Deployment, error)
	UpdateDeployment(ctx context.Context, d store.Deployment) error
	ListLiveDeployments(ctx context.Context) ([]store.Deployment, error)
}

// Handler runs queue.TypeDeploy jobs.
type Handler struct {
	Log     *slog.Logger
	Store   Store
	Fetcher runtime.Fetcher
	Builder runtime.Builder
	Runner  runtime.Runner
	Router  proxy.Router
	// WorkspaceDir is the parent of per-deploy clone trees. Empty uses
	// the process temp directory. Trees are removed after the job.
	WorkspaceDir string
	// WorkspaceMaxBytes is the Phase 5 disk quota on a fetched repo.
	// 0 means config.DefaultWorkspaceMaxBytes. This is measured after
	// Fetch and before docker build so a huge tree cannot fill the
	// control-plane disk. It is not a container --storage-opt.
	WorkspaceMaxBytes int64
	ProxyBase         string
	HealthTimeout     time.Duration
	// Health is optional. Tests inject a success stub; production uses
	// runtime.HealthGET against the published loopback port.
	Health func(ctx context.Context, port int, timeout time.Duration) error
}

type deployPayload struct {
	DeploymentID string `json:"deployment_id"`
}

func (h Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func (h Handler) Handle(ctx context.Context, job queue.Job) error {
	if job.Type != queue.TypeDeploy {
		return fmt.Errorf("%w: %q", queue.ErrUnknownType, job.Type)
	}

	var p deployPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return fmt.Errorf("deploy payload: %w", err)
	}
	id, err := store.ParseUUID(p.DeploymentID)
	if err != nil {
		return fmt.Errorf("deploy payload: %w", err)
	}

	d, err := h.Store.GetDeployment(ctx, id)
	if err != nil {
		return err
	}
	if !d.Status.InProgress() && d.Status != store.StatusQueued {
		// Operator Stop (or worker restart) already closed this row.
		return nil
	}
	app, err := h.Store.GetApplication(ctx, d.ApplicationID)
	if err != nil {
		return h.fail(ctx, d, store.StatusDetecting, err)
	}

	err = h.runPipeline(ctx, d, app)
	if errors.Is(err, errAbandoned) {
		return nil
	}
	return err
}

func (h Handler) runPipeline(ctx context.Context, d store.Deployment, app store.Application) error {
	started := time.Now()
	stage := func(name string) {
		// Stage names only — no env values, no git URLs with userinfo.
		h.log().Info("deploy stage",
			"deployment_id", d.ID,
			"stage", name,
			"elapsed_ms", time.Since(started).Milliseconds(),
		)
	}
	work := filepath.Join(h.workspace(), d.ID)
	// Remove the clone tree when we return so a failed detect cannot
	// leave user source on disk indefinitely.
	defer func() { _ = os.RemoveAll(work) }()

	// Rollback reuses a prior image_name. Fetching the repo again would
	// ignore the operator's "restore this artifact" intent and would let
	// a client-shaped job try to smuggle a different URL. The image name
	// was copied from the source row by the API, not from the job JSON.
	env, err := h.loadRunEnv(ctx, app.ID)
	if err != nil {
		return h.fail(ctx, d, store.StatusDetecting, err)
	}

	rollback := strings.TrimSpace(d.RollbackOf) != ""
	spec := detect.Result{Port: detect.DefaultPort, PortSource: "forge_default"}
	if rollback {
		stage("rollback")
		if err := h.runRollback(ctx, &d); err != nil {
			return err
		}
	} else {
		stage("build")
		detected, err := h.runBuild(ctx, &d, app, work, env)
		if err != nil {
			return err
		}
		spec = detected
	}

	stage("provision")
	if err := h.setStatus(ctx, &d, store.StatusProvisioning, d.RuntimeKind, ""); err != nil {
		return err
	}
	container := runtime.WorkloadName(app.Name, d.ID)
	d.ContainerName = container
	d.ListenPort = spec.ListenPort()
	d.RuntimeType = spec.RuntimeOrDefault()
	d.PortSource = spec.PortSource
	h.log().Info("provision creating container",
		"deployment_id", d.ID,
		"container", container,
		"image", d.ImageName,
		"listen_port", d.ListenPort,
		"runtime", d.RuntimeType,
	)

	// Stop the previous LIVE container *before* docker run so two
	// clicks cannot leave two processes bound to two ports. HTTP also
	// refuses a new job while one is running; this is the worker-side
	// invariant if a job was already queued.
	h.stopPrevious(ctx, d)

	if err := h.setStatus(ctx, &d, store.StatusDeploying, d.RuntimeKind, ""); err != nil {
		return err
	}
	inst, err := h.Runner.Run(ctx, d.ImageName, container, env, spec.ListenPort())
	if err != nil {
		return h.fail(ctx, d, store.StatusProvisioning, fmt.Errorf("container create/start failed: %w", err))
	}
	d.ContainerID = inst.ContainerID
	d.HostPort = inst.HostPort
	h.log().Info("provision container created",
		"deployment_id", d.ID,
		"container", container,
		"container_id", inst.ContainerID,
		"host_port", inst.HostPort,
		"listen_port", spec.ListenPort(),
	)

	if st, inspErr := h.Runner.Inspect(ctx, container); inspErr == nil && st.Exists && !st.Running {
		logs, _ := h.Runner.Logs(ctx, container, 80)
		_ = h.Runner.Stop(ctx, container)
		return h.fail(ctx, d, store.StatusDeploying, fmt.Errorf("Container %s exited immediately (status %s, exit %d).\n\n%s", container, st.Status, st.ExitCode, strings.TrimSpace(logs)))
	}

	if err := h.setStatus(ctx, &d, store.StatusHealthCheck, d.RuntimeKind, ""); err != nil {
		_ = h.Runner.Stop(ctx, container)
		return err
	}
	timeout := h.HealthTimeout
	if timeout == 0 {
		timeout = 45 * time.Second
	}
	path := healthPath(app, spec)
	health := h.Health
	if health == nil {
		health = func(ctx context.Context, port int, timeout time.Duration) error {
			return runtime.ProbeHTTP(ctx, runtime.Probe{
				Port:       port,
				Path:       path,
				Timeout:    timeout,
				ListenPort: spec.ListenPort(),
				PortSource: spec.PortSource,
				BindHint:   spec.BindHint,
				Container:  container,
				Inspect: func(ctx context.Context) (runtime.ContainerState, error) {
					return h.Runner.Inspect(ctx, container)
				},
				TailLogs: func(ctx context.Context) string {
					text, _ := h.Runner.Logs(ctx, container, 80)
					return text
				},
			})
		}
	}
	stage("health")
	h.log().Info("health checking", "url", fmt.Sprintf("http://127.0.0.1:%d%s", inst.HostPort, path))
	if err := health(ctx, inst.HostPort, timeout); err != nil {
		_ = h.Runner.Stop(ctx, container)
		return h.fail(ctx, d, store.StatusHealthCheck, err)
	}

	// Operator slug wins. An empty slug still gets a Host route so
	// Vite/SPA assets work at / on {name}.localhost. A missing slug
	// is not a deploy failure and is not why health 404s.
	slug := strings.TrimSpace(app.LocalHost)
	if slug == "" {
		slug = store.SuggestedLocalHost(app.Name, app.ID)
	}
	d.LocalHost = slug
	d.PublicURL = proxy.PublicURL(h.ProxyBase, d.ID)
	if hostURL := proxy.HostPublicURL(h.ProxyBase, slug); hostURL != "" {
		d.PublicURL = hostURL
	}
	if err := h.guardActive(ctx, d.ID); err != nil {
		_ = h.Runner.Stop(ctx, container)
		return err
	}
	if err := h.route(ctx, d); err != nil {
		_ = h.Runner.Stop(ctx, container)
		return h.fail(ctx, d, store.StatusDeploying, err)
	}

	if err := h.guardActive(ctx, d.ID); err != nil {
		_ = h.Runner.Stop(ctx, container)
		return err
	}
	d.Status = store.StatusLive
	d.FailedStage = ""
	d.ErrorMessage = ""
	if err := h.Store.UpdateDeployment(ctx, d); err != nil {
		_ = h.Runner.Stop(ctx, container)
		return err
	}

	h.stopPrevious(ctx, d)
	h.log().Info("deployment live",
		"id", d.ID,
		"kind", d.RuntimeKind,
		"port", d.HostPort,
		"duration_ms", time.Since(started).Milliseconds(),
	)
	return nil
}

func (h Handler) runBuild(ctx context.Context, d *store.Deployment, app store.Application, work string, env []runtime.EnvPair) (detect.Result, error) {
	if err := h.setStatus(ctx, d, store.StatusDetecting, "", ""); err != nil {
		return detect.Result{}, err
	}
	if err := h.Fetcher.Fetch(ctx, app.RepositoryURL, work); err != nil {
		return detect.Result{}, h.fail(ctx, *d, store.StatusDetecting, err)
	}
	if err := h.checkWorkspaceSize(work); err != nil {
		return detect.Result{}, h.fail(ctx, *d, store.StatusDetecting, err)
	}

	buildDir, err := runtime.ConfineRoot(work, app.RootDirectory)
	if err != nil {
		return detect.Result{}, h.fail(ctx, *d, store.StatusDetecting, err)
	}

	detected, err := detect.Tree(buildDir)
	if err != nil {
		return detect.Result{}, h.fail(ctx, *d, store.StatusDetecting, err)
	}
	d.RuntimeKind = string(detected.Kind)
	d.RuntimeType = detected.RuntimeOrDefault()
	d.PortSource = detected.PortSource
	d.ListenPort = detected.ListenPort()
	h.log().Info("detect complete",
		"deployment_id", d.ID,
		"kind", detected.Kind,
		"framework", detected.Framework,
		"runtime", d.RuntimeType,
		"port", d.ListenPort,
		"port_source", d.PortSource,
	)

	if err := h.setStatus(ctx, d, store.StatusBuilding, d.RuntimeKind, ""); err != nil {
		return detect.Result{}, err
	}
	image := "forge-app-" + strings.ReplaceAll(d.ID, "-", "")
	log, err := h.Builder.Build(ctx, buildDir, image, d.RuntimeKind, env)
	d.BuildLog = store.SanitizeBuildLog(detected.LogLines() + log)
	d.ImageName = image
	if err != nil {
		return detect.Result{}, h.fail(ctx, *d, store.StatusBuilding, err)
	}
	if err := h.setStatus(ctx, d, store.StatusBuilding, d.RuntimeKind, ""); err != nil {
		return detect.Result{}, err
	}
	return detected, nil
}

func (h Handler) runRollback(ctx context.Context, d *store.Deployment) error {
	// Skip fetch/detect/build. The source image is a Forge-owned tag
	// (forge-app-{uuid}). If it was deleted from Docker, Run fails and
	// we persist FAILED instead of cloning whatever is on git HEAD now.
	if err := h.setStatus(ctx, d, store.StatusBuilding, d.RuntimeKind, ""); err != nil {
		return err
	}
	if strings.TrimSpace(d.ImageName) == "" {
		return h.fail(ctx, *d, store.StatusBuilding, fmt.Errorf("rollback source has no image"))
	}
	d.BuildLog = store.SanitizeBuildLog("rollback: reused image " + d.ImageName + " (skipped fetch/build)\n")
	return h.setStatus(ctx, d, store.StatusBuilding, d.RuntimeKind, "")
}

func (h Handler) route(ctx context.Context, current store.Deployment) error {
	if h.Router == nil {
		return fmt.Errorf("caddy router is not configured")
	}
	live, err := h.Store.ListLiveDeployments(ctx)
	if err != nil {
		return err
	}
	// Include the deployment we are about to mark LIVE so Caddy is
	// updated before clients see public_url.
	merged := make([]store.Deployment, 0, len(live)+1)
	seen := false
	for _, d := range live {
		if d.ApplicationID == current.ApplicationID {
			// Replace this application's previous live route only.
			// Other apps in the same project stay routed.
			continue
		}
		if d.ID == current.ID {
			seen = true
		}
		merged = append(merged, d)
	}
	if !seen {
		merged = append(merged, current)
	}
	return h.Router.Apply(ctx, merged)
}

func (h Handler) stopPrevious(ctx context.Context, current store.Deployment) {
	live, err := h.Store.ListLiveDeployments(ctx)
	if err != nil {
		return
	}
	for _, d := range live {
		if d.ApplicationID == current.ApplicationID && d.ID != current.ID {
			_ = h.Runner.Stop(ctx, runtime.ResolveContainerName(d.ID, d.ContainerName))
			d.Status = store.StatusStopped
			d.FailedStage = ""
			d.ErrorMessage = SanitizeNotice("replaced by a newer deployment")
			_ = h.Store.UpdateDeployment(ctx, d)
		}
	}
}

func (h Handler) loadRunEnv(ctx context.Context, applicationID string) ([]runtime.EnvPair, error) {
	vars, err := h.Store.ListEnvVars(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	out := make([]runtime.EnvPair, 0, len(vars))
	for _, ev := range vars {
		// Values are operator metadata, not secrets-management.
		// Do not log ev.Value. Re-validate so a corrupted row cannot
		// become a docker -e argument with a newline.
		if err := store.ValidateEnvKey(ev.Key); err != nil {
			return nil, err
		}
		if err := store.ValidateEnvValue(ev.Value); err != nil {
			return nil, err
		}
		out = append(out, runtime.EnvPair{Key: ev.Key, Value: ev.Value})
	}
	return out, nil
}

func (h Handler) workspace() string {
	if strings.TrimSpace(h.WorkspaceDir) != "" {
		return h.WorkspaceDir
	}
	return filepath.Join(os.TempDir(), "forge-work")
}

// checkWorkspaceSize walks the fetched tree and fails if regular-file
// bytes exceed the quota. Symlinks are not followed (Walk does not
// descend into them), so a link to / cannot inflate the count into a
// host-wide scan. Failure here is StatusDetecting: we have not built.
func (h Handler) checkWorkspaceSize(root string) error {
	max := h.WorkspaceMaxBytes
	if max <= 0 {
		max = config.DefaultWorkspaceMaxBytes
	}
	var n int64
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info == nil || !info.Mode().IsRegular() {
			return nil
		}
		n += info.Size()
		if n > max {
			return fmt.Errorf("workspace exceeds disk quota (%d bytes)", max)
		}
		return nil
	})
	return err
}

func healthPath(app store.Application, spec detect.Result) string {
	// Operator health_path wins when it is not the implicit default.
	// forge.json health is the repo-level override for `/`.
	if p := strings.TrimSpace(app.HealthPath); p != "" && p != "/" {
		return p
	}
	if p := spec.HealthOrDefault(); p != "" {
		return p
	}
	return "/"
}

// errAbandoned means the operator (or worker restart) already closed
// this row. Further stage writes must not resurrect it as BUILDING/LIVE.
var errAbandoned = errors.New("deployment was stopped")

func (h Handler) setStatus(ctx context.Context, d *store.Deployment, status store.Status, kind, errMsg string) error {
	if err := h.guardActive(ctx, d.ID); err != nil {
		return err
	}
	d.Status = status
	if kind != "" {
		d.RuntimeKind = kind
	}
	d.ErrorMessage = store.SanitizeErrorMessage(errMsg)
	return h.Store.UpdateDeployment(ctx, *d)
}

func (h Handler) guardActive(ctx context.Context, id string) error {
	cur, err := h.Store.GetDeployment(ctx, id)
	if err != nil {
		return err
	}
	if cur.Status == store.StatusFailed || cur.Status == store.StatusStopped {
		return errAbandoned
	}
	return nil
}

func (h Handler) fail(ctx context.Context, d store.Deployment, stage store.Status, cause error) error {
	d.Status = store.StatusFailed
	d.FailedStage = string(stage)
	d.ErrorMessage = store.SanitizeErrorMessage(cause.Error())
	if err := h.Store.UpdateDeployment(ctx, d); err != nil {
		h.log().Error("failed to persist deployment failure", "id", d.ID, "err", err)
	}
	h.log().Error("deployment failed", "id", d.ID, "stage", stage, "err", cause)
	return cause
}

// SanitizeNotice is a control-plane sentence, not a user-supplied string.
func SanitizeNotice(s string) string {
	return store.SanitizeErrorMessage(s)
}
