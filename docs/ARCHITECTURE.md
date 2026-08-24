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

**Current schema (Phase 0 increment 0.2):** `schema_migrations` (version tracking) and `projects` (`id`, `name`, `repository_url`, `created_at`, `updated_at`). `repository_url` is stored as text only — the control plane does not clone or execute it.

### 4.4 Redis

Redis provides infrastructure for asynchronous and transient operations.

Initial responsibilities may include:

- job queues
- worker coordination
- transient state
- caching where justified

Redis should not replace PostgreSQL as the durable source of truth.

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

These technologies are **architectural direction**, not a permanent lock and not permission to implement all components immediately. They may change through an explicit, documented decision.

---

## 11. Architectural Constraints

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

---

## 12. Evolution

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

---

## 13. Architecture Decision Rule

When an implementation decision is ambiguous:

- Prefer the smallest correct solution.
- Preserve the control-plane/workload boundary.
- Preserve security isolation.
- Avoid unnecessary dependencies.
- Avoid premature distributed systems.
- Document significant decisions.
- Verify the implementation before moving to the next increment.

The architecture is allowed to evolve, but changes must be deliberate and documented.
