// Package httpapi is the control-plane HTTP surface.
//
// Routes here are Forge's own API (health, projects, jobs, deployments).
// They are not the HTTP servers of apps users deploy — those sit behind
// Caddy on 127.0.0.1:9080.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/reyansh7/Forge/internal/detect"
	"github.com/reyansh7/Forge/internal/githubapi"
	"github.com/reyansh7/Forge/internal/observe"
	"github.com/reyansh7/Forge/internal/store"
)

// StatusChecker reports whether a dependency is reachable.
//
// The interface is small on purpose: health only needs Ping. Tests inject
// a fake that returns an error; production injects *store.Postgres / RedisPinger.
// Implementations must not run user code.
type StatusChecker interface {
	Ping(ctx context.Context) error
}

// Server is the control-plane HTTP process's handler bundle.
//
// cmd/api fills these fields. Postgres and Redis satisfy StatusChecker
// (/health). The same *store.Postgres also satisfies ProjectStore,
// ApplicationStore, and DeploymentStore. Jobs is the queue abstraction
// (not Redis commands). Tests swap fakes so Docker is not required.
type Server struct {
	Log      *slog.Logger
	Postgres StatusChecker
	Redis    StatusChecker
	// Projects is unused by /health. Project routes require it.
	Projects ProjectStore
	// Jobs is unused by /health. POST /jobs and deployments require it.
	Jobs JobQueue
	// Deployments is unused by /health. Deployment routes require it.
	Deployments DeploymentStore
	// Apps is unused by /health. Application, env, logs, and stop require it.
	Apps ApplicationStore
	// Runtime is unused by GET /health. Application health, logs, and
	// stop talk to Docker here (inspect/read/stop only). Build and run
	// stay on the worker.
	Runtime AppRuntime
	// Router is unused by /health. Stop reapplies Caddy without the
	// stopped deployment so dead ports are not routed.
	Router RouteApplier
	// Auth is unused by GET /health. Every other control-plane route
	// requires a session. Tests inject memAuth; cmd/api injects Postgres.
	Auth IdentityStore
	// Metrics is process-local HTTP/enqueue counters (Phase 4).
	// Deploy history still comes from PostgreSQL at GET /metrics time.
	Metrics *observe.Metrics
	// Nodes is unused by /health. POST/GET /nodes and deploy placement
	// persist node_id. Tests leave this nil so Jobs.Enqueue stays on
	// the shared list.
	Nodes NodeRegistry
	// Placer is unused by /health. When set with Sink, deploys go to
	// one node's Redis list. Placement logic lives in internal/schedule.
	Placer Placer
	// Sink is unused by /health. Directed RPUSH; tests inject queue.Directed.
	Sink JobSink
	// Links is unused by /health. Same-project service edges (Phase 7.1).
	Links LinkStore
	// ProjectDeploys records Deploy All sagas. Nil disables
	// POST /projects/{id}/deploy. Postgres implements it.
	ProjectDeploys ProjectDeployStore
	// Discoverer overrides GitHub repository discovery. Tests set it.
	// Production leaves it nil and uses the connected GitHub account.
	Discoverer func(ctx context.Context, project store.Project) ([]detect.Service, error)
	// Volumes is unused by /health. Persistent named volumes (Phase 7.5).
	Volumes VolumeStore
	// Domains is unused by /health. Custom hostnames + ACME (Phase 7.6).
	Domains DomainStore
	// GitHubAccounts is unused by /health. OAuth tokens and repo
	// bindings (Phase 7.7). Tests inject a memory store.
	GitHubAccounts GitHubStore
	// GitHub is unused by /health. GitHub REST/OAuth. Tests inject a stub
	// so unit tests never dial api.github.com.
	GitHub githubapi.API
	// GitHubClientID / GitHubClientSecret come from FORGE_GITHUB_*.
	// Empty means connect is disabled. Never log the secret.
	GitHubClientID     string
	GitHubClientSecret string
	// GitHubAllowedUserIDs is the personal allowlist. Empty refuses
	// GitHub login and GitHub connect. Webhooks stay HMAC-checked.
	GitHubAllowedUserIDs []int64
	// PublicBase is the dashboard origin for OAuth redirects.
	PublicBase string
	// WebhookBase is the URL prefix GitHub POSTs to.
	WebhookBase string
	// AppsDomain assigns https://{slug}.{AppsDomain}/ . Empty disables it.
	AppsDomain string
	// ControlHost is the dashboard hostname Caddy serves when AppsDomain is set.
	ControlHost string
	// LookupTXT is optional DNS for domain verify. Tests inject a stub
	// so unit tests never dial the network. Production uses the default resolver.
	LookupTXT func(ctx context.Context, name string) ([]string, error)
	// ProxyPublicBase is the Caddy URL used to preview service-link Host
	// addresses. Empty means http://127.0.0.1:9080.
	ProxyPublicBase string
	// Limits is Phase 5 operator quotas. Zero fields use defaults.
	Limits Limits
	// SecureCookies is set when the API serves TLS so forge_session
	// is not sent on plaintext HTTP.
	SecureCookies bool
	// Schemas lists applied migration filenames for GET /operator/status.
	Schemas SchemaLister

	loginGate  *attemptGate
	deployGate *attemptGate
}

// Limits are per-operator resource caps (Phase 5).
type Limits struct {
	MaxProjectsPerOwner int
	MaxInflightDeploys  int
	MaxInflightPerNode  int
}

// SchemaLister is the HTTP → schema_migrations boundary.
type SchemaLister interface {
	SchemaVersions(ctx context.Context) ([]string, error)
}

func (s *Server) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Handler returns the mux wrapped in withAuth.
//
// Only Forge control-plane routes belong here (not the HTTP servers of
// apps users deploy — those sit behind Caddy).
//
// Go 1.22 method-aware patterns ("GET /health") reject POST to the same
// path with 405. GET /projects and GET /projects/{id} are different
// patterns; the mux picks the more specific one for /projects/<uuid>.
//
// Public: GET /health, GET /auth/status, POST /auth/bootstrap,
// POST /auth/signup, POST /auth/login, GET /auth/github,
// POST /auth/logout, GET /integrations/github/callback,
// POST /webhooks/github/{id}.
// The webhook still requires a valid HMAC. Everything else needs a
// session (Bearer or forge_session cookie). Auth == nil fails closed
// (401), so a miswired API cannot revert to Phase 0's loopback-only gate.
// GET /metrics, GET /operator/status, and GET /applications/{id}/logs/stream require a session.
func (s *Server) Handler() http.Handler {
	if s.Metrics == nil {
		s.Metrics = &observe.Metrics{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /auth/status", s.authStatus)
	mux.HandleFunc("POST /auth/bootstrap", s.bootstrap)
	mux.HandleFunc("POST /auth/signup", s.signup)
	mux.HandleFunc("POST /auth/login", s.login)
	mux.HandleFunc("GET /auth/github", s.githubLogin)
	mux.HandleFunc("POST /auth/logout", s.logout)
	mux.HandleFunc("GET /auth/me", s.me)
	mux.HandleFunc("POST /projects", s.createProject)
	mux.HandleFunc("GET /projects", s.listProjects)
	mux.HandleFunc("GET /projects/{id}", s.getProject)
	mux.HandleFunc("GET /projects/{id}/plan", s.projectPlan)
	mux.HandleFunc("POST /projects/{id}/deploy", s.deployProject)
	mux.HandleFunc("GET /projects/{id}/deploys", s.listProjectDeploys)
	mux.HandleFunc("GET /projects/{id}/deploys/{deployId}", s.getProjectDeploy)
	mux.HandleFunc("POST /projects/{id}/deploys/{deployId}/cancel", s.cancelProjectDeploy)
	mux.HandleFunc("POST /projects/{id}/applications", s.createApplication)
	mux.HandleFunc("GET /projects/{id}/applications", s.listApplications)
	mux.HandleFunc("GET /applications/{id}", s.getApplication)
	mux.HandleFunc("PATCH /applications/{id}", s.updateApplication)
	mux.HandleFunc("DELETE /applications/{id}", s.deleteApplication)
	mux.HandleFunc("POST /applications/{id}/deployments", s.createApplicationDeployment)
	mux.HandleFunc("GET /applications/{id}/deployments", s.listApplicationDeployments)
	mux.HandleFunc("GET /applications/{id}/logs", s.getApplicationLogs)
	mux.HandleFunc("GET /applications/{id}/logs/stream", s.streamApplicationLogs)
	mux.HandleFunc("GET /metrics", s.getMetrics)
	mux.HandleFunc("GET /operator/status", s.operatorStatus)
	mux.HandleFunc("GET /applications/{id}/health", s.getApplicationHealth)
	mux.HandleFunc("POST /applications/{id}/stop", s.stopApplication)
	mux.HandleFunc("POST /applications/{id}/restart", s.restartApplication)
	mux.HandleFunc("GET /applications/{id}/env", s.listEnv)
	mux.HandleFunc("PUT /applications/{id}/env", s.putEnv)
	mux.HandleFunc("PUT /applications/{id}/env/bulk", s.replaceEnv)
	mux.HandleFunc("DELETE /applications/{id}/env/{key}", s.deleteEnv)
	mux.HandleFunc("POST /projects/{id}/deployments", s.createDeployment)
	mux.HandleFunc("GET /projects/{id}/deployments", s.listDeployments)
	mux.HandleFunc("GET /deployments/{id}", s.getDeployment)
	mux.HandleFunc("POST /deployments/{id}/rollback", s.rollbackDeployment)
	mux.HandleFunc("POST /deployments/{id}/retry", s.retryDeployment)
	mux.HandleFunc("POST /jobs", s.enqueueJob)
	mux.HandleFunc("GET /nodes", s.listNodes)
	mux.HandleFunc("POST /nodes", s.createNode)
	mux.HandleFunc("GET /applications/{id}/links", s.listLinks)
	mux.HandleFunc("POST /applications/{id}/links", s.createLink)
	mux.HandleFunc("DELETE /applications/{id}/links/{linkId}", s.deleteLink)
	mux.HandleFunc("GET /applications/{id}/volumes", s.listVolumes)
	mux.HandleFunc("POST /applications/{id}/volumes", s.createVolume)
	mux.HandleFunc("DELETE /applications/{id}/volumes/{volumeId}", s.deleteVolume)
	mux.HandleFunc("GET /applications/{id}/domains", s.listDomains)
	mux.HandleFunc("POST /applications/{id}/domains", s.createDomain)
	mux.HandleFunc("POST /applications/{id}/domains/{domainId}/verify", s.verifyDomain)
	mux.HandleFunc("DELETE /applications/{id}/domains/{domainId}", s.deleteDomain)
	mux.HandleFunc("GET /integrations/github/status", s.githubStatus)
	mux.HandleFunc("GET /integrations/github/start", s.githubStart)
	mux.HandleFunc("GET /integrations/github/callback", s.githubCallback)
	mux.HandleFunc("POST /integrations/github/disconnect", s.githubDisconnect)
	mux.HandleFunc("GET /integrations/github/repos", s.githubRepos)
	mux.HandleFunc("GET /applications/{id}/env-templates", s.listEnvTemplates)
	mux.HandleFunc("POST /applications/{id}/env-templates", s.applyEnvTemplate)
	mux.HandleFunc("GET /applications/{id}/github", s.getApplicationGitHub)
	mux.HandleFunc("PUT /applications/{id}/github", s.putApplicationGitHub)
	mux.HandleFunc("DELETE /applications/{id}/github", s.deleteApplicationGitHub)
	mux.HandleFunc("POST /webhooks/github/{id}", s.githubWebhook)
	return stripDashboardPrefix(s.withObserve(s.withAuth(mux)))
}

// stripDashboardPrefix rewrites /forge-api/... to /... before auth and
// routing. The Next.js dashboard calls /forge-api/*; if a rewrite
// forwards that prefix to this process, the mux would 404 and withAuth
// would 401 public auth routes.
func stripDashboardPrefix(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		canon := canonicalAPIPath(r.URL.Path)
		if canon == r.URL.Path {
			next.ServeHTTP(w, r)
			return
		}
		cp := r.Clone(r.Context())
		cp.URL.Path = canon
		next.ServeHTTP(w, cp)
	})
}

// healthResponse is the JSON body. Field names are the public contract
// for curl, Compose healthchecks, and later a dashboard.
type healthResponse struct {
	Status   string `json:"status"`
	Postgres string `json:"postgres"`
	Redis    string `json:"redis"`
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	// Bound the pings so a hung database cannot stall this handler forever.
	// The request context is also cancelled if the client disconnects.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	resp := healthResponse{
		Status:   "ok",
		Postgres: "ok",
		Redis:    "ok",
	}

	if err := s.Postgres.Ping(ctx); err != nil {
		s.logger().Error("postgres ping failed", "err", err)
		resp.Postgres = "error"
		resp.Status = "degraded"
	}
	if err := s.Redis.Ping(ctx); err != nil {
		s.logger().Error("redis ping failed", "err", err)
		resp.Redis = "error"
		resp.Status = "degraded"
	}

	// 503 (not 500) means "this process is up, a dependency is not."
	// Load balancers and Compose can use that distinction.
	code := http.StatusOK
	if resp.Status != "ok" {
		code = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	// Encode errors are rare (broken conn). Ignoring them avoids a second
	// write after WriteHeader; the client already has a status code.
	_ = json.NewEncoder(w).Encode(resp)
}
