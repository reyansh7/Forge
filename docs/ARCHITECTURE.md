# Forge Architecture

## 1. What Is Forge?

Forge is a self-hosted Platform-as-a-Service (PaaS) built from scratch.

The goal is to provide a system where a user can connect a Git repository, and Forge can:

1. Detect the application.
2. Build the application.
3. Package it into a deployable artifact/container.
4. Provision an isolated runtime.
5. Start the application.
6. Verify that the application is healthy.
7. Expose it through a reverse proxy.
8. Provide deployment and runtime information through a dashboard.

Long-term product direction (developer experience, production capability, self-host vs managed, AWS readiness) is in `docs/PRODUCT_VISION.md`. That file does **not** authorize implementation. This file is the technical architecture and constraints. Sequencing is `docs/ROADMAP.md`.

Forge is intended to provide a practical understanding of the infrastructure behind platforms such as Railway, Render, and similar PaaS products — and to evolve, phase by phase, into a production-capable system. It is **not** specified by copying those products.

The initial objective remains: a correct, understandable, self-hostable **local** system. Scale, public DNS, and multi-region are later phases.

When Forge is later hosted off the local machine, the first cloud target is **AWS within the Free Tier**. That is a design constraint from this point forward, not permission to implement AWS now. See section 11.

### 1.1 Source-of-truth hierarchy

```text
PRODUCT_VISION.md      long-term direction; does not authorize implementation
ROADMAP.md             phased implementation; START PHASE N authorizes work
ARCHITECTURE.md        technical architecture and constraints (this file)
DEVELOPMENT_RULES.md   engineering and security rules
CURSOR_ENVIRONMENT.md  Cursor tooling only
AGENTS.md              agent operating contract
```

If these documents conflict, stop and name the conflict. Do not silently pick a side.

---

## 2. Core Principle

The most important security assumption in Forge is:

> USER CODE IS UNTRUSTED CODE.

Any code originating from a user's repository must be treated as potentially malicious.

The control plane must never assume that application code is trustworthy.

This affects:

- build execution
- runtime execution
- filesystem access
- networking
- environment variables
- secrets
- resource usage
- process isolation
- container lifecycle
- logging
- deployment
- cleanup

The control plane and user workloads must have clearly defined security boundaries.

---

## 3. High-Level Architecture

The initial conceptual architecture is:

```text
                    ┌────────────────────┐
                    │      User          │
                    └─────────┬──────────┘
                              │
                              ▼
                    ┌────────────────────┐
                    │  Next.js Dashboard │
                    └─────────┬──────────┘
                              │
                              ▼
                    ┌────────────────────┐
                    │      Go API        │
                    │    Control Plane   │
                    └──────┬─────┬───────┘
                           │     │
              ┌────────────┘     └─────────────┐
              ▼                                ▼
      ┌───────────────┐                ┌───────────────┐
      │  PostgreSQL   │                │     Redis     │
      │ Source of     │                │ Queue / Cache │
      │ Truth         │                └───────┬───────┘
      └───────────────┘                        │
                                               ▼
                                      ┌─────────────────┐
                                      │     Worker      │
                                      │ Build / Deploy  │
                                      └────────┬────────┘
                                               │
                                               ▼
                                      ┌─────────────────┐
                                      │ Docker Runtime  │
                                      │ User Workload   │
                                      └────────┬────────┘
                                               │
                                               ▼
                                      ┌─────────────────┐
                                      │ Health Check    │
                                      └────────┬────────┘
                                               │
                                               ▼
                                      ┌─────────────────┐
                                      │ Caddy / Reverse │
                                      │ Proxy           │
                                      └────────┬────────┘
                                               │
                                               ▼
                                           Internet
```

This diagram represents the initial architecture and logical responsibilities. Exact implementation details may evolve as the project progresses.

---

## 4. Major Components

### 4.1 Go API

The Go API is the primary control-plane service.

Responsibilities include (some are **not yet implemented** — see Current use):

- authentication and authorization (Phase 3: session + project owner_id)
- project management
- application management
- deployment requests
- deployment state
- environment configuration
- runtime metadata
- API endpoints
- communication with PostgreSQL
- enqueueing asynchronous work
- coordination with workers

The API should not directly execute arbitrary user application code.

Long-running or potentially dangerous operations should be delegated to appropriate workers or isolated execution environments.

### 4.2 Next.js Dashboard

The Next.js application provides the developer-facing interface.

Initial responsibilities include:

- authentication UI
- projects
- applications
- deployments
- deployment status
- logs
- environment configuration
- basic runtime information

The dashboard communicates with the Go API rather than directly controlling infrastructure.

**Current use (Phase 4):** `web/` is a Next.js App Router UI on `127.0.0.1:3000` with a black/red theme. `/login` asks for Sign in first (name + password). A link opens Sign up (name + password + confirm). Each login replaces the `forge_session` cookie and revokes the previous session so two operators do not share a live cookie in the same browser. Subsequent API calls send credentials (cookie) and, after login, a bearer token. It lists projects and applications the operator owns, manages env vars (including .env paste/upload and bulk replace), application settings (`root_directory`, `health_path`, `local_host`), queues deployments and rollbacks, polls status, streams runtime logs over SSE (snapshots remain), shows persisted `build_log` and `duration_ms`, and can stop a live app or cancel an in-progress deploy. `/observe` shows authorized metrics. Browser calls go to `/forge-api/*`, which Next.js rewrites to the Go API (the log stream has a dedicated App Router proxy so follow is not buffered). The dashboard does not talk to Docker, Redis, or Caddy. Tracing and alerts are later phases.

### 4.3 PostgreSQL

PostgreSQL is the durable source of truth for Forge's control-plane state.

It will eventually store information such as:

- users
- projects
- applications
- deployments
- build metadata
- runtime metadata
- environment configuration metadata
- deployment states
- relevant audit information

PostgreSQL is authoritative for persistent Forge state.

Redis must not become the source of truth for durable state.

The exact schema must be designed incrementally during the appropriate implementation phase.

**Current schema (Phase 6):** `schema_migrations`; `users` (bcrypt `password_hash`); `sessions` (`token_hash` is SHA-256 of the bearer/cookie, never the raw token); `audit_events` (no env values or passwords); `projects` (`owner_id` → users); `applications` (`id`, `project_id`, `name`, `repository_url`, `root_directory`, `health_path`, `local_host`, timestamps, unique `(project_id, name)`, unique non-empty `local_host`); `application_env_vars`; `nodes` (`id`, `name` DNS label, `token_hash` SHA-256 of the join token, `status` ready/draining/dead, `advertise_host`, `last_seen_at`); `deployments` (`id`, `project_id`, `application_id`, `status`, `failed_stage`, `error_message`, `runtime_kind`, `runtime_type`, `port_source`, `host_port`, `listen_port`, `container_id`, `container_name`, `public_url`, `image_name`, `build_log`, `rollback_of`, `node_id` → nodes, timestamps). A project is a folder owned by one operator. An application is the deployable unit. Creating a project also inserts a default application named `app` with the same repository URL. Status values include `stopped`. Two apps in one project may both be LIVE; **one application has at most one LIVE deployment** (unique index). The API refuses Deploy or Rollback while that container is still running. `root_directory` is a relative path inside the clone (default `.`); the worker confines it so `..` cannot escape the fetch tree. `health_path` is an HTTP path on loopback (default `/`). `local_host` is an optional `.localhost` slug routed by Caddy in addition to `/d/{id}/` — not public DNS. App HTTPS is Caddy `tls internal` on `127.0.0.1:9443` (local CA). `image_name` / `build_log` are worker-written artifacts for history and rollback. Env values are operator metadata in Postgres, sealed at rest with AES-256-GCM (`enc:v1:`) when the process has a data key — encrypted-at-rest, not a vault. Storing a URL does not execute it; the worker fetches only after a deploy job. Rollback copies `image_name` from a prior row and skips fetch/build. List/Get/mutate of a project (and its apps, env, deployments, logs) require a session whose user id equals `projects.owner_id`. A UUID in the URL is not authorization; wrong owner is 404.

### 4.4 Redis

Redis provides infrastructure for asynchronous and transient operations.

Initial responsibilities may include:

- job queues
- worker coordination
- transient state
- caching where justified

Redis should not replace PostgreSQL as the durable source of truth.

**Current use (Phase 6):** each ready worker has its own Redis LIST (`forge:jobs:{node_id}`). `internal/schedule` picks a ready node under the per-node inflight cap; the API `RPUSH`es there; that worker `BLPOP`s only its key. Job *status* still lives in `deployments` (PostgreSQL). A Redis restart drops queued jobs that were never popped; restore of Postgres does not replay the LISTs — re-enqueue those rows. The LIST is not a durable workflow engine. See `docs/DISASTER_RECOVERY.md`.

### 4.5 Worker

Workers execute asynchronous Forge operations.

The worker is responsible for tasks such as:

```text
Deployment Request
       ↓
Fetch Source
       ↓
Detect
       ↓
Build
       ↓
Provision
       ↓
Deploy
       ↓
Health Check
       ↓
Report Result
```

The worker must not execute untrusted application code directly on the control-plane host.

**Current use (Phase 6):** `cmd/worker` is a separate process from `cmd/api`. On start it claims a node (`FORGE_NODE_NAME`, default `local`; named nodes need `FORGE_NODE_TOKEN`), heartbeats every 15s, and consumes only `forge:jobs:{its node id}`. Interrupted non-queued rows are abandoned **for that node only**. Jobs: `example` (log and return) and `deploy` (load application → fetch its repo unless this is a rollback → confine `root_directory` → detect → `docker build` → `docker run` with validated env → health GET on `health_path` → Caddy route, including optional `{slug}.localhost`). Replacing a live container is per application, not per project. Rollback jobs skip fetch/build and `docker run` a prior `image_name`. HTTP handlers do not run the pipeline inline. Stop and log snapshot are API → Docker read/stop only. Unknown types and client `command` fields are rejected. User application code runs only inside Docker, never as a host shell. `https` clones reject loopback/private resolved IPs. `forge://hello` copies an embedded sample. Workload “migration” is Stop + Deploy so placement can pick another ready node — not live container move.

### 4.6 Build System

The build system converts application source code into a deployable artifact.

The initial conceptual pipeline is:

```text
Git Repository
      ↓
Source Retrieval
      ↓
Application Detection
      ↓
Build Configuration
      ↓
Container/Image Build
      ↓
Artifact
```

Build execution is considered untrusted execution and must have an explicit isolation boundary.

**Current use:** detect is a pack registry under the confined `root_directory` (manifests, Dockerfiles, and shallow source layout; never host exec). Images, video, fonts, and other assets are ignored for classification and remain in `COPY . .` when a strategy exists. A valid user `Dockerfile` always wins. Without one, packs match language manifests and then choose a **build strategy** and a **runtime strategy**. `npm run build` is not treated as an HTTP server: Vite/CRA/Vue/Svelte frontends without a production `start` become `runtime=static` (Node build stage + Forge-owned nginx). A root `index.html` with no language manifest is the same static runtime. Server starts still come from `package.json` `start` (not `next dev` / `vite`), `Cargo.toml` / `.csproj` names, Maven/Gradle, unique Django `*/wsgi.py`, unique FastAPI/Flask modules, `config/puma.rb`, a `Procfile` `web:` process, or `forge.json` `start`. Dev servers (`runserver`, `flask run`, `php -S`, `next dev`, `vite preview`) are not chosen automatically. Hardcoded `listen(5000)` in a conventional entry file is used as the container port when the process does not read `$PORT`. `forge.json` may also set `health`, `runtime`, and `output`. If start cannot be determined safely, detect fails early with the detected files and a suggested fix. `runtime_kind` is the pack ID; `runtime_type` is `server`, `static`, or `image`. Rollback does not rebuild.

### 4.7 Runtime

A successful build produces a deployable application runtime.

The initial runtime model is container-based.

A deployed application should conceptually have:

- an isolated container/process environment
- defined resource boundaries
- defined network behavior
- environment configuration
- health checking
- lifecycle management
- logs
- restart behavior

The runtime must not share unrestricted control-plane privileges.

**Current use (Phase 4):** `docker run` publishes `127.0.0.1:{host}:{listen}` where `listen` is the detected application port (not always 8080). Isolation is unchanged: memory/CPU/pids limits, `no-new-privileges`, `--cap-drop ALL`, a `tmpfs` `/tmp`, and `--pull never`. Operator env vars are written into `.env.production.local` and passed as `docker build --build-arg` so Vite/Next/CRA public keys (`VITE_*`, `NEXT_PUBLIC_*`, `REACT_APP_*`) are inlined at compile time; the same keys are passed again as `-e KEY=VALUE` at `docker run` for server processes. `HOST=0.0.0.0` and `PORT=<listen>` are applied last at run. `PORT`, `HOST`, and `FORGE_*` keys are rejected. Values must not be copied into `build_log`. Containers are named `<app-slug>-<short-id>` (legacy rows still resolve as `forge-run-<uuid>`). No Docker socket mount, no `--privileged`. After `docker run` the worker inspects the container: an immediate exit is FAILED at deploying with logs, not a health timeout. The go-live probe classifies connection refused, HTTP status (404 fails immediately), and dead processes. After LIVE, dashboard `/applications/{id}/health` uses `docker inspect` (container running) so polling does not flood the app's access logs. Runtime logs are a `docker logs -t --tail` snapshot plus an authorized SSE follow (`docker logs -f`). Build output includes `[detect]` strategy lines. Phase 5 adds a fetched-workspace disk cap (`FORGE_WORKSPACE_MAX_BYTES`), per-owner project and in-flight deploy quotas, and build-log retention prune. Tenant network policies and autoscaling are not current.

### 4.8 Reverse Proxy

Caddy is the initial reverse-proxy component.

Its responsibility is to route external requests to healthy deployed applications.

Conceptually:

```text
Internet
   ↓
Caddy
   ↓
Application
```

Caddy should not be responsible for application orchestration.

The control plane determines deployment/runtime state; the reverse proxy handles request routing.

**Current use (Phase 6):** Caddy runs in Compose, published on `127.0.0.1:9080` (HTTP), `127.0.0.1:9443` (HTTPS, `tls internal`), and `127.0.0.1:2019` (admin). `auto_https` stays off — ACME needs a public hostname (Phase 7). The worker (and the API on stop) POST a generated Caddyfile to `/load`. Live apps are reached at `http://127.0.0.1:9080/d/{deployment_id}/` (and `https://127.0.0.1:9443/d/{deployment_id}/` if the client trusts Caddy’s local CA) and at `http://{slug}.localhost:9080/`. The operator slug is optional; if empty, the worker derives one from the application name so Host routing still exists. Path URLs strip `/d/{id}` (`handle_path`). Vite/CRA default builds request `/assets/*` from the origin root, so Caddy also proxies those GETs when `Referer` contains `/d/{id}/`, and Forge builds Vite with `--base ./`. Prefer the `.localhost` Host URL for SPAs. The slug is local DX, not a public custom domain. App containers publish only on `127.0.0.1:{port}` on the node that ran them. Caddy reaches same-machine ports via `host.docker.internal` unless `nodes.advertise_host` / `FORGE_NODE_ADVERTISE_HOST` is set. Admin must stay loopback on the host — it can rewrite every route. A second worker process on this machine is the supported lab. A remote worker that reloads Caddy needs a private path to admin (SSH tunnel), not a public `:2019`. Failed health checks Stop/rm the attempt’s container; image and logs stay on the deployment row.

---

## 5. Deployment Lifecycle

The conceptual Forge deployment lifecycle is:

```text
                 Deployment Request
                         │
                         ▼
                     DETECT
                         │
                         ▼
                      BUILD
                         │
                         ▼
                    PROVISION
                         │
                         ▼
                     DEPLOY
                         │
                         ▼
                  HEALTH CHECK
                         │
                    ┌────┴────┐
                    │         │
                  FAIL       PASS
                    │         │
                    ▼         ▼
                  FAILED     LIVE
                              │
                              ▼
                          ROUTE TRAFFIC
```

Each stage should have an explicit state.

The system should be able to determine where a deployment failed rather than reporting only a generic failure.

---

## 6. Control Plane vs Workload Plane

Forge must maintain a conceptual separation between:

**Control plane** — responsible for:

- API
- authentication
- database
- orchestration
- deployment state
- scheduling
- worker coordination
- infrastructure metadata

**Workload plane** — responsible for:

- user builds
- user containers
- user processes
- application networking
- application logs

The workload plane must be treated as untrusted.

A compromise of a user workload must not automatically imply compromise of the control plane.

---

## 7. Security Architecture

Security is a first-class architectural requirement.

Important principles:

**Least privilege** — components should receive only the permissions required for their role.

**Isolation** — user workloads must execute inside an explicit isolation boundary.

**No host execution** — user-provided commands must never simply be passed to a host shell.

**Resource limits** — user workloads must have limits. **Current:** memory, CPU, pids, capability drop, tmpfs `/tmp`, loopback publish, fetched-workspace disk cap, per-owner project and in-flight deploy quotas, per-node inflight cap, build-log retention. **Future (roadmap):** tenant overlay policy and autoscaling (Phase 7+ / 9).

**Network isolation** — user workloads must not automatically receive unrestricted access to internal infrastructure. **Current:** published ports are loopback; Caddy admin is loopback-published on the host; workloads do not get the Docker socket or `--network host`. There is **no overlay mesh** between worker nodes: a container stays on the Docker host that built it. Operators must not publish control-plane Postgres, Redis, or Caddy admin on a worker VM. **Future:** tenant overlay / NetworkPolicy-style enforcement (Phase 7+).

**Secrets** — secrets must never be:

- committed to Git
- printed in logs
- exposed to unrelated workloads
- unnecessarily available to build processes

Postgres env vars are **not** a secret manager. Phase 5 seals cells with AES-256-GCM (`enc:v1:`) using `.forge/data.key` or `FORGE_DATA_KEY`. The API and worker must share that key. Losing the key loses readable env. Do not log the key or dump files.

**Authentication and authorization** — every control-plane operation that affects resources must be authorized. **Current:** loopback bind plus operator sessions (`POST /auth/bootstrap` or `POST /auth/signup`, then `POST /auth/login`). A new session replaces the `forge_session` cookie and revokes the previous token from that browser. Mutating and data-leaking routes require a valid session. Projects are scoped by `owner_id`; another operator's UUID is 404, not 200. `GET /health` stays public for Compose/process checks. Binding every interface requires `FORGE_ALLOW_PUBLIC_BIND=1`; auth is still required and TLS is recommended. Team RBAC is a later phase.

**Auditability** — security-sensitive control-plane actions write `audit_events` (bootstrap, login, project/app/env/deploy/stop/rollback). Metadata must not include env values, passwords, or session tokens.

**Image and dependency review** — `docker build` pulls base images and runs repo `RUN` lines as untrusted execution. Isolation (no socket, no privileged, cap-drop) is the control. A scanner (Trivy, etc.) is an optional operator process, not a Forge product and not a substitute for those flags. Review Dockerfiles and lockfiles before deploying a repo you did not write.

**Tenant isolation** — when more than one operator exists, compromise of one workload must not imply compromise of another tenant or of the control plane. **Current:** projects are owner-scoped; one LIVE container per application; workloads cannot reach control-plane Redis/Postgres/Caddy admin as published (those binds stay loopback). This is not a multi-tenant SaaS.

---

## 8. Networking Model

The initial networking model is conceptually:

```text
                Internet
                   │
                   ▼
                Caddy
                   │
            ┌──────┴──────┐
            ▼             ▼
        App A           App B
        :port           :port
```

Applications should not need to expose their internal ports directly to the public Internet.

The reverse proxy is the public entry point.

**Current:** Caddy is published on `127.0.0.1:9080` (HTTP) and `127.0.0.1:9443` (HTTPS, local CA). “Internet” in this diagram is not the present bind.

Internal service communication should be explicit and controlled.

---

## 9. Data Flow

A typical deployment request should follow:

```text
User
 ↓
Dashboard
 ↓
Go API
 ↓
PostgreSQL
 ↓
Redis / Job Queue
 ↓
Worker
 ↓
Build Environment
 ↓
Container Image
 ↓
Runtime
 ↓
Health Check
 ↓
Caddy
 ↓
Internet
```

The exact mechanics of each step are implementation decisions made during the relevant phase.

---

## 10. Initial Technology Direction

The current technology direction is:

| Area | Technology |
|------|------------|
| Control-plane API | Go |
| Dashboard | Next.js |
| Database | PostgreSQL |
| Queue / transient infrastructure | Redis |
| Application packaging/runtime | Docker / containers |
| Reverse proxy | Caddy |
| Source control | Git / GitHub |
| Development environment | Docker-based where appropriate |
| Eventual operator cloud | AWS, designed to fit the Free Tier first |

These technologies are **architectural direction**, not a permanent lock and not permission to implement all components immediately. They may change through an explicit, documented decision.

AWS must not be added to Phase 0 or any later phase merely because it appears in this table. Cloud hosting is implemented only in an authorized increment.

---

## 11. AWS Free Tier and Cost Architecture

Forge must be **designed from the beginning** so that an operator can deploy and run it efficiently on the **AWS Free Tier**.

This constraint applies to architectural decisions now. It does **not** authorize implementing AWS, creating cloud accounts, or leaving the local Phase 0 topology.

### 11.1 Why this constraint exists

Forge is a learning and operator-owned PaaS. The first cloud deployment must be affordable to run for months without surprise bills.

Cost, Free Tier fit, and leftover-resource cleanup are therefore first-class architecture, equal in weight to security isolation and phase discipline.

### 11.2 What “Free Tier compatible” means

When cloud hosting is later authorized, Forge should:

- Prefer **lightweight, serverless, and managed** AWS services where they fit the existing control-plane/workload split.
- Prefer **pay-per-use or idle-friendly** compute over always-on instances.
- Minimize always-on compute, storage, networking, and operational overhead.
- Avoid infrastructure whose baseline cost blows the Free Tier (for example NAT Gateways, always-on NAT, large multi-AZ fleets, EKS-style control planes, unused Elastic IPs, idle load balancers).
- Avoid large multi-service deployments that exist only because a reference architecture used them.
- Keep data volumes, log retention, image storage, and network egress small by default.
- Tear down unused resources. Cleanup is part of the design, not an afterthought.

A local Compose stack (Postgres, Redis, Caddy, API, worker) is the current Phase 0 shape. Future AWS mappings must preserve those **roles** (API, durable store, transient queue, worker, proxy) without requiring a rewrite into a different product.

### 11.3 Modularity for later scale

The architecture must stay modular so Forge can grow **beyond** the Free Tier without a major rewrite:

```text
Current            Local Compose, loopback, one machine (Phases 0–3)
        ↓
First cloud        Same component boundaries, cheapest AWS that fits Free Tier (Phase 8)
        ↓
Later              More capacity, HA, extra regions — swap or add modules, do not rebuild Forge
```

Interfaces already used locally (HTTP API, `JobQueue`, store, worker, proxy) are the seam. A future AWS queue or database adapter may replace a local implementation; handlers and the deployment state machine must not assume localhost, Docker Desktop, or a NAT Gateway.

Do not implement those adapters until the roadmap increment that authorizes cloud hosting (Phase 8).

### 11.4 What this does not change

- Phase 0–3 remain local. Docker Compose on loopback is correct until a later phase explicitly changes publication.
- User code remains untrusted. Serverless or managed runtimes do not relax isolation.
- PostgreSQL remains durable source of truth; Redis remains transient.
- The dashboard still talks only to the API.
- Distributed or production AWS topology is still a later phase (Phase 8+ in `docs/ROADMAP.md`).

### 11.5 Decision test for future work

Before adding a dependency, service, or always-on process, ask:

1. Can an operator run this on AWS Free Tier without a NAT Gateway or a permanent extra VPC cost?
2. Does it stay idle-cheap when no deployments are running?
3. Can it be removed or replaced later without rewriting the control plane?
4. Who deletes it when it is unused?

If the answer is unclear, prefer the smaller local or managed option and document the trade-off.

---

## 12. Evolution interfaces

Future phases must extend these **roles**, not collapse them into a vendor SDK.

| Role | Current local shape | Must remain replaceable |
|------|---------------------|-------------------------|
| Control plane API | `cmd/api`, `internal/httpapi` | HTTP contract; no Docker in handlers |
| Durable storage | PostgreSQL via store interfaces | Not Redis; not a cloud-only API baked into handlers |
| Transient queue | Redis LIST `JobQueue` | Memory implementation exists for tests; cloud queue is an adapter |
| Worker | `cmd/worker` + `internal/deploy` | Same job types; no user code on host |
| Runtime | Docker CLI from worker | Isolation boundary stays even if the engine changes |
| Reverse proxy | Caddy admin `/load` | Orchestration stays in the control plane |
| Scheduler / node | `internal/schedule` + `nodes` table + per-node Redis LIST | Placement stays in the scheduler; HTTP only calls `Place()` |

**Control plane vs workload plane** stays the primary security cut (section 6).

**Secure build isolation** — `docker build` is untrusted. Future builders must not `exec` repo scripts on the host.

**Runtime isolation** — containers with least privilege; no Docker socket in the workload.

**Reliable deployment state machine** — named statuses, persisted failures, image rollback as a first-class transition. Advanced strategies add states; they do not delete the machine.

**Rollback / recovery** — current: reuse `image_name`; PostgreSQL dump/restore via `cmd/backup` (`docs/DISASTER_RECOVERY.md`). A dead worker is marked `dead` after a stale heartbeat; Postgres is not dropped. Re-place work with Stop + Deploy. Future: automatic failover (Phase 9), multi-region DR (Phase 10).

**Observability** — structured control-plane logs, authorized SSE runtime follow, and session-gated `/metrics`. Tracing and alerts are later. Telemetry must not become a secret leak.

**High availability and disaster recovery** — Postgres remains source of truth; queue loss is survivable and documented. Backup/restore is current. A second worker is placement, not HA of the API or Postgres.

Do not design every future subsystem at implementation detail in this file. Prefer a named interface and a later increment.

---

## 13. Architectural Constraints

The following constraints apply:

- Do not execute user code on the control-plane host.
- Do not treat user repositories as trusted.
- Do not use Redis as durable source of truth.
- Do not allow the dashboard to bypass the API for infrastructure operations.
- Do not allow application containers unrestricted access to control-plane infrastructure.
- Do not introduce infrastructure solely for theoretical future requirements.
- Prefer explicit boundaries over implicit behavior.
- Prefer simple implementations during early phases.
- Every major architectural change must be documented.
- Do not implement future phases prematurely. `PRODUCT_VISION.md` is not a work order.
- Design for AWS Free Tier deployability, cost efficiency, and resource cleanup (section 11). Do not implement AWS until an increment explicitly authorizes it.
- Do not introduce NAT Gateways, always-on multi-service clouds, or other high-baseline AWS cost as a default.

---

## 14. Evolution

Forge is intentionally designed to evolve.

The initial architecture may begin as a single-machine system.

A later architecture may separate:

```text
Control Plane
      │
      ├── API
      ├── Database
      ├── Queue
      └── Scheduler
             │
             ▼
        Worker Nodes
             │
      ┌──────┼──────┐
      ▼      ▼      ▼
     App    App    App
```

Multi-node scheduling, distributed builds, advanced networking, observability, autoscaling, AWS, and other production capabilities must be introduced only in their appropriate roadmap phases.

Do not prematurely implement distributed infrastructure.

The first cloud evolution, when authorized (Phase 8), must stay inside the Free Tier envelope in section 11. Scaling off the Free Tier is a later, explicit choice — not the default shape of the first AWS deploy.

---

## 15. Architecture Decision Rule

When an implementation decision is ambiguous:

- Prefer the smallest correct solution.
- Preserve the control-plane/workload boundary.
- Preserve security isolation.
- Avoid unnecessary dependencies.
- Avoid premature distributed systems.
- Prefer designs that remain cheap on AWS Free Tier and that clean up unused resources.
- Do not copy another PaaS’s topology because it is popular.
- Document significant decisions.
- Verify the implementation before moving to the next increment.

The architecture is allowed to evolve, but changes must be deliberate and documented.
