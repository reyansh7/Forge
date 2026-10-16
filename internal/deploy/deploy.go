// Package deploy is the worker pipeline for one application deployment.
//
// Flow: load application → fetch its repo (unless rollback) → confine
// root_directory → detect → build → run with env vars *alongside* the
// previous LIVE container → health check on health_path → Caddy Host
// switch → mark LIVE → stop the previous container.
//
// A failed health check or Caddy load leaves the previous LIVE row
// and its container running. This is rolling deploy, not canary.
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
	"sort"
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
// Two apps in one project may both be LIVE; replace is per
// application and environment so preview cannot stop production.
type Store interface {
	GetApplication(ctx context.Context, id string) (store.Application, error)
	ListEnvVars(ctx context.Context, applicationID string) ([]store.EnvVar, error)
	ListApplicationLinks(ctx context.Context, sourceID string) ([]store.ApplicationLink, error)
	ListApplicationVolumes(ctx context.Context, applicationID string) ([]store.ApplicationVolume, error)
	GetDeployment(ctx context.Context, id string) (store.Deployment, error)
	UpdateDeployment(ctx context.Context, d store.Deployment) error
	ListLiveDeployments(ctx context.Context) ([]store.Deployment, error)
	CutoverLive(ctx context.Context, next store.Deployment) ([]store.Deployment, error)
	// GitHubCloneToken is the sealed-then-opened PAT for a bound app.
	// Empty means public clone (or no GitHub connection).
	GitHubCloneToken(ctx context.Context, applicationID string) (string, error)
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
	// 0 means config.DefaultWorkspaceMaxBytes (256 MiB). This is
	// measured after Fetch and before docker build. It is an
	// application cap on regular-file bytes, not a reading of free
	// space on the host disk, and not a container --storage-opt.
	// FORGE_WORKSPACE_MAX_BYTES overrides it.
	WorkspaceMaxBytes int64
	ProxyBase         string
	HealthTimeout     time.Duration
	// NodeID is this worker's nodes.id. Empty skips leasing so unit
	// tests that drive the pipeline directly do not need a lease row.
	// Production cmd/worker always sets it.
	NodeID string
	// AppsDomain reserves {slug}.{zone} before a production cutover.
	// Empty skips that reservation. ControlHost is refused as a slug
	// target so an app cannot take the dashboard name.
	AppsDomain  string
	ControlHost string
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
	// Re-queue replaces active_job_id before RPUSH. A job still on the
	// Redis list from the previous push must not start a second build.
	// An empty id is a row claimed before migration 0014; those still run.
	if d.ActiveJobID != "" && job.ID != d.ActiveJobID {
		h.log().Info("deploy job superseded", "deployment_id", d.ID, "job_id", job.ID)
		return nil
	}
	// A frontend queued beside its API must not build until that API
	// is live or has failed. ErrDefer puts the same job back on the
	// list. The active job id still matches, so this is not a second
	// execution. The lease is not taken yet.
	if view, err := h.sagaView(ctx, id); err != nil {
		return err
	} else if dependenciesPending(view) {
		return queue.ErrDefer
	}
	runCtx := ctx
	if h.NodeID != "" {
		owned, acquired, err := h.acquireLease(ctx, d, job.ID)
		if err != nil {
			return err
		}
		if !acquired {
			return nil
		}
		d = owned
		var cancel context.CancelFunc
		runCtx, cancel = context.WithCancel(ctx)
		defer cancel()
		go h.keepLease(runCtx, cancel, d.ID, job.ID)
	}
	app, err := h.Store.GetApplication(runCtx, d.ApplicationID)
	if err != nil {
		return h.fail(ctx, d, store.StatusDetecting, err)
	}

	err = h.runPipeline(runCtx, d, app)
	if errors.Is(err, errAbandoned) {
		return nil
	}
	return err
}

func (h Handler) runPipeline(ctx context.Context, d store.Deployment, app store.Application) error {
	started := time.Now()
	// meas is filled as stages finish. A stage that never ran stays
	// unavailable, including a zero that would look like a fast no-op.
	// The defer logs the same shape on success and on failure.
	var meas deployMeasurements
	defer func() {
		meas.total = measuredMS(time.Since(started))
		h.logMeasurements(d.ID, meas)
	}()
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
	// A cancelled or already-failed project saga must not start another
	// build that could replace a healthy release. Single-application
	// deploys are not in a saga, so the reason is empty.
	if why, err := h.projectBlockReason(ctx, d.ID); err != nil {
		return h.fail(ctx, d, store.StatusDetecting, err)
	} else if why != "" {
		return h.fail(ctx, d, store.StatusQueued, fmt.Errorf("project deploy stopped: %s", why))
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
		detected, err := h.runBuild(ctx, &d, app, work, env, &meas)
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

	// Rolling: do not stop the previous LIVE container before docker
	// run. The new process binds a *new* published port. Traffic stays
	// on the old Host until health passes and Caddy switches. Stopping
	// first was the downtime window this increment removes.
	vols, err := h.loadRunVolumes(ctx, app.ID, d.Environment)
	if err != nil {
		return h.fail(ctx, d, store.StatusProvisioning, err)
	}

	if err := h.setStatus(ctx, &d, store.StatusDeploying, d.RuntimeKind, ""); err != nil {
		return err
	}
	startAt := time.Now()
	inst, sagaView, err := h.startContainer(ctx, d, app, env, spec.ListenPort(), vols)
	meas.start = measuredMS(time.Since(startAt))
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
	healthAt := time.Now()
	err = health(ctx, inst.HostPort, timeout)
	meas.health = measuredMS(time.Since(healthAt))
	if err != nil {
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
	if err := h.guardActive(ctx, d.ID, d.ActiveJobID, d.Status); err != nil {
		_ = h.Runner.Stop(ctx, container)
		return err
	}
	// Promotion is the traffic switch. If a required sibling already
	// failed, leave the previous healthy container and Caddy route
	// alone. A sibling that was already promoted is restored by
	// compensate, newest first, after this failure is recorded.
	if why, err := h.projectBlockReason(ctx, d.ID); err != nil {
		_ = h.Runner.Stop(ctx, container)
		return h.fail(ctx, d, store.StatusDeploying, err)
	} else if why != "" {
		_ = h.Runner.Stop(ctx, container)
		return h.fail(ctx, d, store.StatusDeploying, fmt.Errorf("project deploy stopped before promotion: %s", why))
	}
	// Private services stay off Caddy. They are reachable by their
	// project-network alias. An explicit local_host, or a frontend,
	// still gets the public Host route. Loopback publish remains so
	// the health check can run; it is not a public listener.
	if sagaView.InSaga && !exposePublic(app.Name, app.LocalHost) {
		d.PublicURL = store.InternalServiceURL(slug, spec.ListenPort())
	} else {
		routeSlug := store.RouteLocalHost(d.Environment, slug)
		d.PublicURL = proxy.PublicURL(h.ProxyBase, d.ID)
		if hostURL := proxy.HostPublicURL(h.ProxyBase, routeSlug); hostURL != "" {
			d.PublicURL = hostURL
		}
		if env, err := store.ParseEnvironment(d.Environment); err == nil && env == store.EnvProduction {
			if err := h.ensurePlatformHost(ctx, app.ID, slug); err != nil {
				_ = h.Runner.Stop(ctx, container)
				return h.fail(ctx, d, store.StatusDeploying, err)
			}
		}
		if err := h.route(ctx, d); err != nil {
			_ = h.Runner.Stop(ctx, container)
			h.restoreRoutes(ctx)
			return h.fail(ctx, d, store.StatusDeploying, err)
		}
	}

	if err := h.guardActive(ctx, d.ID, d.ActiveJobID, d.Status); err != nil {
		_ = h.Runner.Stop(ctx, container)
		h.restoreRoutes(ctx)
		return err
	}
	// Persist LIVE only after Caddy switched. The previous same-env
	// LIVE row is stopped in the same transaction so the unique
	// (application_id, environment) index is never double-LIVE.
	d.Status = store.StatusLive
	d.FailedStage = ""
	d.ErrorMessage = ""
	replaced, err := h.Store.CutoverLive(ctx, d)
	if err != nil {
		_ = h.Runner.Stop(ctx, container)
		h.restoreRoutes(ctx)
		return h.fail(ctx, d, store.StatusDeploying, err)
	}
	// A saga keeps the previous container until every required service
	// is live. Single-application deploys stop it here, as before.
	h.finishSaga(ctx, d, replaced)
	h.log().Info("deployment live",
		"id", d.ID,
		"kind", d.RuntimeKind,
		"port", d.HostPort,
		"duration_ms", time.Since(started).Milliseconds(),
	)
	return nil
}

func (h Handler) fetchSource(ctx context.Context, d store.Deployment, app store.Application, dest string) error {
	// Auto-deploy records a SHA. Private repos need the operator's
	// GitHub token. Both go through GitFetcher so the PAT never lands
	// in repository_url (userinfo is rejected). Tests that only
	// implement Fetcher keep using Fetch.
	token := ""
	if h.Store != nil {
		t, err := h.Store.GitHubCloneToken(ctx, app.ID)
		if err != nil {
			return err
		}
		token = t
	}
	if gf, ok := h.Fetcher.(runtime.GitFetcher); ok && (strings.TrimSpace(d.CommitSHA) != "" || token != "") {
		return gf.FetchRef(ctx, app.RepositoryURL, dest, d.CommitSHA, token)
	}
	return h.Fetcher.Fetch(ctx, app.RepositoryURL, dest)
}

func (h Handler) runBuild(ctx context.Context, d *store.Deployment, app store.Application, work string, env []runtime.EnvPair, meas *deployMeasurements) (detect.Result, error) {
	if err := h.setStatus(ctx, d, store.StatusDetecting, "", ""); err != nil {
		return detect.Result{}, err
	}
	fetchAt := time.Now()
	err := h.fetchSource(ctx, *d, app, work)
	meas.fetch = measuredMS(time.Since(fetchAt))
	if err != nil {
		return detect.Result{}, h.fail(ctx, *d, store.StatusDetecting, err)
	}
	report, err := h.checkWorkspaceSize(work)
	if report.Complete {
		meas.workspaceKnown = true
		meas.workspaceBytes = report.Bytes
	}
	if err != nil {
		return detect.Result{}, h.fail(ctx, *d, store.StatusDetecting, err)
	}

	buildDir, err := runtime.ConfineRoot(work, app.RootDirectory)
	if err != nil {
		return detect.Result{}, h.fail(ctx, *d, store.StatusDetecting, err)
	}
	// Pin the Go package inside the module root the detector sees.
	// cmd/api is not its own module; go.mod stays one directory up.
	// The file is control-plane data, overwritten every deploy, and
	// detect ignores any value that is not "." or ./cmd/<name>.
	if pkg, err := store.ValidateBuildPackage(app.BuildPackage); err != nil {
		return detect.Result{}, h.fail(ctx, *d, store.StatusDetecting, err)
	} else if pkg != "" {
		if err := os.WriteFile(filepath.Join(buildDir, ".forge-build-package"), []byte(pkg+"\n"), 0o600); err != nil {
			return detect.Result{}, h.fail(ctx, *d, store.StatusDetecting, err)
		}
	}

	detectAt := time.Now()
	detected, err := detect.Tree(buildDir)
	if err != nil {
		if pkg, ok := RepairGoPackage(err.Error()); ok && strings.TrimSpace(app.BuildPackage) == "" {
			if werr := os.WriteFile(filepath.Join(buildDir, ".forge-build-package"), []byte(pkg+"\n"), 0o600); werr != nil {
				meas.detect = measuredMS(time.Since(detectAt))
				return detect.Result{}, h.fail(ctx, *d, store.StatusDetecting, werr)
			}
			if g, ok := h.Store.(sagaStore); ok {
				if serr := g.SetBuildPackage(ctx, app.ID, pkg); serr != nil {
					h.log().Error("persist repaired build package failed", "application_id", app.ID, "err", serr)
				}
			}
			app.BuildPackage = pkg
			detected, err = detect.Tree(buildDir)
		}
	}
	meas.detect = measuredMS(time.Since(detectAt))
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
	buildAt := time.Now()
	log, err := h.Builder.Build(ctx, buildDir, image, d.RuntimeKind, env)
	if err != nil {
		if pkg, ok := RepairGoPackage(log + "\n" + err.Error()); ok && strings.TrimSpace(app.BuildPackage) == "" {
			if werr := os.WriteFile(filepath.Join(buildDir, ".forge-build-package"), []byte(pkg+"\n"), 0o600); werr == nil {
				if g, ok := h.Store.(sagaStore); ok {
					_ = g.SetBuildPackage(ctx, app.ID, pkg)
				}
				app.BuildPackage = pkg
				repaired, rerr := h.Builder.Build(ctx, buildDir, image, d.RuntimeKind, env)
				log += "\nrepair: set build_package " + pkg + " and rebuilt\n" + repaired
				err = rerr
			}
		}
	}
	meas.build = measuredMS(time.Since(buildAt))
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

// platformHostStore is optional. Postgres implements it. Unit-test
// fakes do not, and they leave AppsDomain empty so this is not called.
type platformHostStore interface {
	EnsurePlatformHost(ctx context.Context, applicationID, slug, appsDomain string) (store.ApplicationDomain, error)
}

func (h Handler) ensurePlatformHost(ctx context.Context, applicationID, slug string) error {
	if strings.TrimSpace(h.AppsDomain) == "" {
		return nil
	}
	host, err := store.PlatformHostname(slug, h.AppsDomain)
	if err != nil {
		return err
	}
	if h.ControlHost != "" && strings.EqualFold(host, strings.TrimSuffix(h.ControlHost, ".")) {
		return fmt.Errorf("platform hostname %s is reserved for the control plane", host)
	}
	p, ok := h.Store.(platformHostStore)
	if !ok {
		return fmt.Errorf("platform host: store cannot reserve %s", host)
	}
	_, err = p.EnsurePlatformHost(ctx, applicationID, slug, h.AppsDomain)
	return err
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
	// updated before clients see public_url. Keep other applications
	// and the other environment (preview vs production) routed.
	// Same-slot previous Host is dropped here — that is the traffic
	// switch — while its container still runs until CutoverLive + stop.
	merged := make([]store.Deployment, 0, len(live)+1)
	for _, d := range live {
		if d.ID == current.ID {
			continue
		}
		if d.ApplicationID == current.ApplicationID && store.SameEnvironment(d.Environment, current.Environment) {
			continue
		}
		merged = append(merged, d)
	}
	merged = append(merged, current)
	return h.Router.Apply(ctx, merged)
}

func (h Handler) restoreRoutes(ctx context.Context) error {
	if h.Router == nil || h.Store == nil {
		return fmt.Errorf("caddy router is not configured")
	}
	live, err := h.Store.ListLiveDeployments(ctx)
	if err != nil {
		h.log().Error("restore caddy after failed cutover failed", "err", err)
		return err
	}
	if err := h.Router.Apply(ctx, live); err != nil {
		h.log().Error("caddy restore failed", "err", err)
		return err
	}
	return nil
}

func (h Handler) stopReplaced(ctx context.Context, previous []store.Deployment) {
	// Stop the replaced container only. Named volumes stay: `docker rm`
	// does not pass -v, and the worker has no RemoveVolume. Deleting
	// disk here would make rolling and rollback destructive.
	for _, d := range previous {
		_ = h.Runner.Stop(ctx, runtime.ResolveContainerName(d.ID, d.ContainerName))
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
	links, err := h.applyServiceLinks(ctx, applicationID, out)
	if err != nil {
		return nil, err
	}
	return links, nil
}

// applyServiceLinks appends Host URLs for same-project links.
//
// Link keys overwrite operator env of the same name so a pasted sibling
// URL cannot drift from the control-plane edge. Values are public
// .localhost addresses, not secrets — still do not log them in case an
// operator pointed env_key at something they treat as private.
func (h Handler) applyServiceLinks(ctx context.Context, applicationID string, env []runtime.EnvPair) ([]runtime.EnvPair, error) {
	links, err := h.Store.ListApplicationLinks(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return env, nil
	}
	byKey := make(map[string]int, len(env))
	for i, p := range env {
		byKey[p.Key] = i
	}
	for _, link := range links {
		if err := store.ValidateEnvKey(link.EnvKey); err != nil {
			return nil, err
		}
		tgt, err := h.Store.GetApplication(ctx, link.TargetApplicationID)
		if err != nil {
			return nil, err
		}
		listen := 0
		if live, lerr := h.Store.ListLiveDeployments(ctx); lerr == nil {
			for _, row := range live {
				if row.ApplicationID == tgt.ID && row.ListenPort > 0 {
					listen = row.ListenPort
				}
			}
		}
		u := serviceLinkURL(h.ProxyBase, tgt, listen)
		if err := store.ValidateEnvValue(u); err != nil {
			return nil, err
		}
		if i, ok := byKey[link.EnvKey]; ok {
			env[i].Value = u
			continue
		}
		env = append(env, runtime.EnvPair{Key: link.EnvKey, Value: u})
		byKey[link.EnvKey] = len(env) - 1
	}
	return env, nil
}

// loadRunVolumes maps persisted declarations to runtime mounts.
//
// Docker names are computed from the application UUID and environment
// here so a client cannot attach another app's volume by posting a
// name, and preview cannot resolve to the production volume. Empty
// list means today's no-volume docker run. The same env-scoped name
// is reused on every deploy of that slot, including rolling while
// the previous container is still mounted — Docker named volumes
// allow that; Forge never copies or deletes the volume to "make
// room" for the new container.
func (h Handler) loadRunVolumes(ctx context.Context, applicationID string, environment string) ([]runtime.VolumeMount, error) {
	if _, err := store.ParseEnvironment(environment); err != nil {
		return nil, err
	}
	if h.Store == nil {
		return nil, nil
	}
	rows, err := h.Store.ListApplicationVolumes(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	out := make([]runtime.VolumeMount, 0, len(rows))
	for _, v := range rows {
		name, err := store.DockerVolumeName(applicationID, v.Name, environment)
		if err != nil {
			return nil, err
		}
		path, err := store.ValidateMountPath(v.MountPath)
		if err != nil {
			return nil, err
		}
		out = append(out, runtime.VolumeMount{Name: name, Path: path})
	}
	return out, nil
}

func (h Handler) workspace() string {
	if strings.TrimSpace(h.WorkspaceDir) != "" {
		return h.WorkspaceDir
	}
	return filepath.Join(os.TempDir(), "forge-work")
}

// workspaceReportFiles is how many paths a quota error names. The walk
// may see every file in the clone; only this many path strings are kept.
const workspaceReportFiles = 5

// workspaceFile is one regular file inside the fetched tree. Rel is
// relative to the workspace root so logs do not include the host temp path.
type workspaceFile struct {
	Rel  string
	Size int64
}

// workspaceReport is the result of one finished walk. Complete is false
// when the walk itself failed; Bytes is then a partial sum and must not
// be logged as the workspace size.
type workspaceReport struct {
	Bytes    int64
	Largest  []workspaceFile
	Complete bool
}

// workspaceQuotaError is the application cap, not a full disk. The host
// being out of space fails earlier, as a clone or write error from the OS.
type workspaceQuotaError struct {
	Limit    int64
	Measured int64
	Largest  []workspaceFile
}

func (e *workspaceQuotaError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "workspace exceeds application disk quota: measured %d bytes, limit %d bytes (FORGE_WORKSPACE_MAX_BYTES; default %d). This is the configured workspace cap, not a report that the host filesystem is full.",
		e.Measured, e.Limit, config.DefaultWorkspaceMaxBytes)
	if len(e.Largest) == 0 {
		return b.String()
	}
	b.WriteString(" Largest files:")
	for i, f := range e.Largest {
		if i > 0 {
			b.WriteString(";")
		}
		fmt.Fprintf(&b, " %s (%d bytes)", f.Rel, f.Size)
	}
	return b.String()
}

func (e *workspaceQuotaError) summary() string {
	var b strings.Builder
	for i, f := range e.Largest {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s (%d bytes)", f.Rel, f.Size)
	}
	return b.String()
}

// stageMeasurement is one pipeline interval. known is false when that
// stage did not run (rollback skips fetch, a quota failure skips build).
// A known 0 is a real sub-millisecond stage, not a missing measurement.
type stageMeasurement struct {
	known bool
	ms    int64
}

func measuredMS(d time.Duration) stageMeasurement {
	return stageMeasurement{known: true, ms: d.Milliseconds()}
}

func (s stageMeasurement) logValue() any {
	if !s.known {
		return "unavailable"
	}
	return s.ms
}

// deployMeasurements is what this worker can observe without asking git
// or Docker for numbers they do not return. transferred bytes are never
// stored here: a shallow clone does not report wire size, and 0 would
// look like an empty transfer.
type deployMeasurements struct {
	fetch          stageMeasurement
	detect         stageMeasurement
	build          stageMeasurement
	start          stageMeasurement
	health         stageMeasurement
	total          stageMeasurement
	workspaceBytes int64
	workspaceKnown bool
}

func (m deployMeasurements) workspaceLogValue() any {
	if !m.workspaceKnown {
		return "unavailable"
	}
	return m.workspaceBytes
}

func (h Handler) logMeasurements(id string, m deployMeasurements) {
	// transferred_bytes stays the string "unavailable". workspace_bytes
	// is the sum of regular files after fetch, which is not the network
	// transfer. No env values, tokens, or absolute workspace paths.
	h.log().Info("deploy measurements",
		"deployment_id", id,
		"fetch_ms", m.fetch.logValue(),
		"transferred_bytes", "unavailable",
		"workspace_bytes", m.workspaceLogValue(),
		"detect_ms", m.detect.logValue(),
		"build_ms", m.build.logValue(),
		"start_ms", m.start.logValue(),
		"health_ms", m.health.logValue(),
		"total_ms", m.total.logValue(),
	)
}

// checkWorkspaceSize walks the fetched tree and fails if regular-file
// bytes exceed the quota. The walk finishes so the measured total and
// the largest files are the real ones, not whichever path happened to
// cross the cap first. The clone has already written those files.
// Symlinks are not followed, so a link to / cannot inflate the count
// into a host-wide scan. Only workspaceReportFiles paths are retained.
// Failure here is StatusDetecting: we have not built.
func (h Handler) checkWorkspaceSize(root string) (workspaceReport, error) {
	max := h.WorkspaceMaxBytes
	if max <= 0 {
		max = config.DefaultWorkspaceMaxBytes
	}
	var measured int64
	var largest []workspaceFile
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Type() is the symlink itself, not the target. WalkDir does
		// not descend into a symlink to a directory. Skip before Info
		// so we never stat the target.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		measured += info.Size()
		largest = keepLargestFiles(largest, workspaceFile{
			Rel:  workspaceRel(root, path),
			Size: info.Size(),
		})
		return nil
	})
	report := workspaceReport{Bytes: measured, Largest: largest, Complete: err == nil}
	if err != nil {
		return report, err
	}
	if measured > max {
		return report, &workspaceQuotaError{Limit: max, Measured: measured, Largest: largest}
	}
	return report, nil
}

func keepLargestFiles(top []workspaceFile, f workspaceFile) []workspaceFile {
	if len(top) < workspaceReportFiles {
		top = append(top, f)
	} else if f.Size <= top[len(top)-1].Size {
		return top
	} else {
		top[len(top)-1] = f
	}
	sort.Slice(top, func(i, j int) bool { return top[i].Size > top[j].Size })
	return top
}

func workspaceRel(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		rel = filepath.Base(path)
	}
	rel = filepath.ToSlash(rel)
	const maxRel = 180
	if len(rel) > maxRel {
		rel = rel[:maxRel]
	}
	return rel
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

// leaseAPI is optional. *store.Postgres implements it. Pipeline tests
// that leave NodeID empty never call it.
type leaseAPI interface {
	AcquireLease(ctx context.Context, id, jobID, nodeID string, ttl time.Duration) (store.Deployment, error)
	RenewLease(ctx context.Context, id, jobID, nodeID string, ttl time.Duration) error
	UpdateOwnedDeployment(ctx context.Context, d store.Deployment) error
}

func (h Handler) acquireLease(ctx context.Context, d store.Deployment, jobID string) (store.Deployment, bool, error) {
	ls, ok := h.Store.(leaseAPI)
	if !ok {
		return d, false, fmt.Errorf("worker node is set but the store cannot lease deployments")
	}
	owned, err := ls.AcquireLease(ctx, d.ID, jobID, h.NodeID, store.LeaseTTL)
	if errors.Is(err, store.ErrLeaseLost) {
		h.log().Info("deploy lease not acquired", "deployment_id", d.ID, "job_id", jobID)
		return d, false, nil
	}
	if err != nil {
		return d, false, err
	}
	if owned.NodeID == "" {
		owned.NodeID = h.NodeID
	}
	return owned, true, nil
}

func (h Handler) keepLease(ctx context.Context, cancel context.CancelFunc, id, jobID string) {
	ls, ok := h.Store.(leaseAPI)
	if !ok {
		return
	}
	t := time.NewTicker(store.LeaseRenewEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			err := ls.RenewLease(ctx, id, jobID, h.NodeID, store.LeaseTTL)
			if err != nil && ctx.Err() == nil {
				h.log().Error("deploy lease lost", "deployment_id", id, "err", err)
				cancel()
				return
			}
		}
	}
}

func (h Handler) setStatus(ctx context.Context, d *store.Deployment, status store.Status, kind, errMsg string) error {
	if err := h.guardActive(ctx, d.ID, d.ActiveJobID, d.Status); err != nil {
		return err
	}
	d.Status = status
	if kind != "" {
		d.RuntimeKind = kind
	}
	if h.NodeID != "" {
		d.NodeID = h.NodeID
	}
	d.ErrorMessage = store.SanitizeErrorMessage(errMsg)
	return h.persist(ctx, *d)
}

func (h Handler) guardActive(ctx context.Context, id, jobID string, owned store.Status) error {
	cur, err := h.Store.GetDeployment(ctx, id)
	if err != nil {
		return err
	}
	if jobID != "" && cur.ActiveJobID != "" && cur.ActiveJobID != jobID {
		return errAbandoned
	}
	if cur.Status == store.StatusFailed || cur.Status == store.StatusStopped {
		return errAbandoned
	}
	// The sweeper put the row back to queued under a new job. This
	// pipeline's later writes would clobber that recovery.
	if owned != "" && owned != store.StatusQueued && cur.Status == store.StatusQueued && cur.ActiveJobID != jobID {
		return errAbandoned
	}
	return nil
}

func (h Handler) persist(ctx context.Context, d store.Deployment) error {
	if h.NodeID != "" {
		if ls, ok := h.Store.(leaseAPI); ok {
			err := ls.UpdateOwnedDeployment(ctx, d)
			if errors.Is(err, store.ErrLeaseLost) {
				return errAbandoned
			}
			return err
		}
	}
	return h.Store.UpdateDeployment(ctx, d)
}

func (h Handler) fail(ctx context.Context, d store.Deployment, stage store.Status, cause error) error {
	if err := h.guardActive(ctx, d.ID, d.ActiveJobID, d.Status); err != nil {
		return err
	}
	d.Status = store.StatusFailed
	d.FailedStage = string(stage)
	d.ErrorMessage = store.SanitizeErrorMessage(cause.Error())
	if h.NodeID != "" {
		d.NodeID = h.NodeID
	}
	if err := h.persist(ctx, d); err != nil {
		if errors.Is(err, errAbandoned) {
			return errAbandoned
		}
		h.log().Error("failed to persist deployment failure", "id", d.ID, "err", err)
	}
	// Tell the rest of the saga to stop promoting. Zero rows when this
	// deployment is a normal single-application deploy.
	if g, ok := h.Store.(projectDeployGate); ok {
		if err := g.MarkProjectDeployFailed(ctx, d.ID, d.ErrorMessage); err != nil {
			h.log().Error("mark project deployment failed", "id", d.ID, "err", err)
		}
	}
	// Undoes promotions that already switched traffic, newest first.
	// A compensation failure is logged and stored. It does not turn
	// the original failure into success, and it does not start a new
	// image to "roll back" a migration.
	h.compensate(ctx, d.ID)
	h.log().Error("deployment failed", "id", d.ID, "stage", stage, "err", cause)
	var quota *workspaceQuotaError
	if errors.As(cause, &quota) {
		h.log().Error("workspace quota exceeded",
			"deployment_id", d.ID,
			"limit_bytes", quota.Limit,
			"measured_bytes", quota.Measured,
			"largest_files", quota.summary(),
		)
	}
	return cause
}

// projectDeployGate is optional. Postgres implements it. Memory fakes
// used by pipeline tests do not, and those deploys are not sagas.
type projectDeployGate interface {
	MarkProjectDeployFailed(ctx context.Context, deploymentID, message string) error
	ProjectDeployBlockReason(ctx context.Context, deploymentID string) (string, error)
}

func (h Handler) projectBlockReason(ctx context.Context, deploymentID string) (string, error) {
	g, ok := h.Store.(projectDeployGate)
	if !ok {
		return "", nil
	}
	return g.ProjectDeployBlockReason(ctx, deploymentID)
}

// SanitizeNotice is a control-plane sentence, not a user-supplied string.
func SanitizeNotice(s string) string {
	return store.SanitizeErrorMessage(s)
}
