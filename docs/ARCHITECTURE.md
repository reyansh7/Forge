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

Forge is intended to provide a practical understanding of the infrastructure behind platforms such as Railway, Render, and similar PaaS products.

Forge is not initially intended to compete with these platforms in scale. The initial objective is to build a correct, understandable, self-hostable system and progressively evolve it toward a production-capable architecture.

When Forge is later hosted off the local machine, the first cloud target is **AWS within the Free Tier**. That is a design constraint from this point forward, not permission to implement AWS now. See section 11.

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

Responsibilities include:

- authentication and authorization
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

**Current use (Phase 2):** `web/` is a Next.js App Router UI on `127.0.0.1:3000` with a black/red theme. It lists projects and applications, manages env vars (including bulk replace), application settings (`root_directory`, `health_path`, `local_host`), queues deployments and rollbacks, polls status, shows `docker logs -t` snapshots and persisted `build_log`, and can stop a live app. Browser calls go to `/forge-api/*`, which Next.js rewrites to the Go API. The dashboard does not talk to Docker, Redis, or Caddy. There is no authentication UI (loopback-only). Log streaming remains Phase 4.

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

**Current schema (Phase 2):** `schema_migrations`; `projects`; `applications` (`id`, `project_id`, `name`, `repository_url`, `root_directory`, `health_path`, `local_host`, timestamps, unique `(project_id, name)`, unique non-empty `local_host`); `application_env_vars`; `deployments` (`id`, `project_id`, `application_id`, `status`, `failed_stage`, `error_message`, `runtime_kind`, `host_port`, `container_id`, `public_url`, `image_name`, `build_log`, `rollback_of`, timestamps). A project is a folder. An application is the deployable unit. Creating a project also inserts a default application named `app` with the same repository URL. Status values include `stopped`. Two apps in one project may both be LIVE; **one application has at most one LIVE deployment** (unique index). The API refuses Deploy or Rollback while that container is still running. `root_directory` is a relative path inside the clone (default `.`); the worker confines it so `..` cannot escape the fetch tree. `health_path` is an HTTP path on loopback (default `/`). `local_host` is an optional `.localhost` slug routed by Caddy in addition to `/d/{id}/` — not public DNS and not TLS. `image_name` / `build_log` are worker-written artifacts for history and rollback. Env values are operator metadata in Postgres, not a secret manager (Phase 6). Storing a URL does not execute it; the worker fetches only after a deploy job. Rollback copies `image_name` from a prior row and skips fetch/build.

### 4.4 Redis

Redis provides infrastructure for asynchronous and transient operations.

Initial responsibilities may include:

- job queues
- worker coordination
- transient state
- caching where justified

Redis should not replace PostgreSQL as the durable source of truth.

**Current use (Phase 1):** a Redis LIST (`forge:jobs`) is transient job transport. The API `RPUSH`es `example` or `deploy` jobs; `cmd/worker` `BLPOP`s them. Job *status* lives in `deployments` (PostgreSQL). A Redis restart can still drop queued jobs that were never popped. The LIST is not a durable workflow engine.

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

**Current use (Phase 2):** `cmd/worker` is a separate process from `cmd/api`. It consumes `example` (log and return) and `deploy` (load application → fetch its repo unless this is a rollback → confine `root_directory` → detect → `docker build` → `docker run` with validated env → health GET on `health_path` → Caddy route, including optional `{slug}.localhost`). Replacing a live container is per application, not per project. Rollback jobs skip fetch/build and `docker run` a prior `image_name`. HTTP handlers do not run the pipeline inline. Stop and log snapshot are API → Docker read/stop only. Unknown types and client `command` fields are rejected. User application code runs only inside Docker, never as a host shell. `https` clones reject loopback/private resolved IPs. `forge://hello` copies an embedded sample.

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

**Current use (Phase 2):** detect is file presence (`Dockerfile`, `go.mod`, `package.json`) under the confined `root_directory`. `docker build` runs on the worker host; npm/go from the repo run only inside that build. Forge writes a Dockerfile for node/go when the repo has none. Combined build output is stored on the deployment as `build_log` (capped). Rollback does not rebuild.

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

**Current use (Phase 2):** `docker run` publishes `127.0.0.1:{port}:8080` with memory/CPU/pids limits and `no-new-privileges`. Operator env vars are passed as `-e KEY=VALUE` after key/value validation; `PORT=8080` is applied last so Forge owns the listen port. `PORT` and `FORGE_*` keys are rejected. No Docker socket mount, no `--privileged`. The worker HTTP-probes `GET http://127.0.0.1:{port}{health_path}` once at go-live. After LIVE, dashboard `/applications/{id}/health` uses `docker inspect` (container running) so polling does not flood the app's access logs. Runtime logs are a `docker logs -t --tail` snapshot (not a websocket stream; that is Phase 4). Build output is on the deployment row.

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

**Current use (Phase 2):** Caddy runs in Compose, published on `127.0.0.1:9080` (HTTP) and `127.0.0.1:2019` (admin). The worker (and the API on stop) POST a generated Caddyfile to `/load`. Live apps are reached at `http://127.0.0.1:9080/d/{deployment_id}/` and, if `local_host` is set, `http://{slug}.localhost:9080/`. That slug is local DX, not a public custom domain. App containers publish only on `127.0.0.1:{port}`; Caddy reaches them via `host.docker.internal`. Admin must stay loopback — it can rewrite every route.

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

**Resource limits** — user workloads must eventually have limits for resources such as:

- CPU
- memory
- processes
- disk
- execution time
- networking

**Network isolation** — user workloads must not automatically receive unrestricted access to internal infrastructure.

**Secrets** — secrets must never be:

- committed to Git
- printed in logs
- exposed to unrelated workloads
- unnecessarily available to build processes

**Authentication and authorization** — every control-plane operation that affects resources must be authorized.

**Auditability** — security-sensitive operations should eventually produce useful audit information.

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
Phase 0 (now)     Local Compose, loopback, one machine
        ↓
First cloud       Same component boundaries, cheapest AWS that fits Free Tier
        ↓
Later             More capacity, HA, extra regions — swap or add modules, do not rebuild Forge
```

Interfaces already used locally (HTTP API, `JobQueue`, store, worker, proxy) are the seam. A future AWS queue or database adapter may replace a local implementation; handlers and the deployment state machine must not assume localhost, Docker Desktop, or a NAT Gateway.

Do not implement those adapters until the roadmap increment that authorizes cloud hosting.

### 11.4 What this does not change

- Phase 0 remains local. Docker Compose on loopback is correct.
- User code remains untrusted. Serverless or managed runtimes do not relax isolation.
- PostgreSQL remains durable source of truth; Redis remains transient.
- The dashboard still talks only to the API.
- Distributed or production AWS topology is still a later phase (see `docs/ROADMAP.md`).

### 11.5 Decision test for future work

Before adding a dependency, service, or always-on process, ask:

1. Can an operator run this on AWS Free Tier without a NAT Gateway or a permanent extra VPC cost?
2. Does it stay idle-cheap when no deployments are running?
3. Can it be removed or replaced later without rewriting the control plane?
4. Who deletes it when it is unused?

If the answer is unclear, prefer the smaller local or managed option and document the trade-off.

---

## 12. Architectural Constraints

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
- Do not implement future phases prematurely.
- Design for AWS Free Tier deployability, cost efficiency, and resource cleanup (section 11). Do not implement AWS until an increment explicitly authorizes it.
- Do not introduce NAT Gateways, always-on multi-service clouds, or other high-baseline AWS cost as a default.

---

## 13. Evolution

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

Multi-node scheduling, distributed builds, advanced networking, observability, autoscaling, and other production capabilities must be introduced only in their appropriate roadmap phases.

Do not prematurely implement distributed infrastructure.

The first cloud evolution, when authorized, must stay inside the Free Tier envelope in section 11. Scaling off the Free Tier is a later, explicit choice — not the default shape of the first AWS deploy.

---

## 14. Architecture Decision Rule

When an implementation decision is ambiguous:

- Prefer the smallest correct solution.
- Preserve the control-plane/workload boundary.
- Preserve security isolation.
- Avoid unnecessary dependencies.
- Avoid premature distributed systems.
- Prefer designs that remain cheap on AWS Free Tier and that clean up unused resources.
- Document significant decisions.
- Verify the implementation before moving to the next increment.

The architecture is allowed to evolve, but changes must be deliberate and documented.
