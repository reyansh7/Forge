# Forge Roadmap

## 1. Roadmap Philosophy

Forge is built incrementally.

The objective is not to generate the entire platform in one pass.

Development follows:

```text
PLAN
  ↓
IMPLEMENT
  ↓
VERIFY
  ↓
REVIEW
  ↓
SECURITY REVIEW
  ↓
DOCUMENT
  ↓
NEXT INCREMENT
```

Each phase must produce a working, understandable system before the next phase begins.

Future-phase work must not be implemented merely because it is known to be required later.

`docs/PRODUCT_VISION.md` is long-term direction. It does **not** authorize implementation. This file is the implementation sequence. `START PHASE N` in chat is the only authorization to begin a phase.

**Competitive goals must never override security, correctness, architectural integrity, phase discipline, or maintainability.**

---

## 2. Current, next, and future

| Kind | Phases | Meaning |
|------|--------|---------|
| **CURRENT** | 0, 1, 2, 3, 4 | Implemented. Local loopback PaaS with operator auth and observability. |
| **NEXT** | 5 | Next authorized work only after `START PHASE 5`. |
| **FUTURE** | 6–10 | Documented sequence. Not authorized. |

Security requirements apply in every phase. Phases 3 and 5 add **depth**; they do not mark the start of security.

### Why this order (future phases)

```text
0 Local foundation          COMPLETE
1 Core PaaS                 COMPLETE
2 Developer experience      COMPLETE
3 Security hardening        COMPLETE
4 Observability             COMPLETE
5 Production hardening      NEXT (single node: TLS, quotas, backup, rate limits)
6 Multi-node infrastructure
7 Advanced platform
8 Cloud / AWS readiness
9 High availability / scaling
10 Multi-region / operational intelligence
```

**Reasoning:** do not distribute or publicly expose an unauthenticated, weakly observed control plane. Authentication and deeper isolation (Phase 3) come before leaving loopback. Observability (Phase 4) comes before calling the system production. Production hardening (Phase 5) stays **single-node** so TLS, backups, and quotas exist before a second worker. Multi-node (Phase 6) waits until one node is trustworthy. Advanced PaaS features (Phase 7) wait until operation is honest. AWS (Phase 8) maps existing roles; it is not a rewrite. HA and multi-region come last.

This **reorders unimplemented work**. Former “Phase 5 multi-node before Phase 6 production hardening” is rejected as less safe. Completed Phases 0–4 keep their numbers and status.

Phase 2 already ships **image rollback**. Later phases may add traffic-shifting strategies; they must not describe rollback as unimplemented.

---

## 3. Phase 0 — Local Deployment Foundation

**Status: COMPLETE** (authorized `START PHASE 0`)

### Objective

Build the smallest complete deployment path on a local machine.

```text
Git Repository → Detect → Build → Container → Run → Health Check → Caddy → Accessible Application
```

### Capabilities (implemented)

- Go API, Next.js dashboard, PostgreSQL, Redis, worker, Docker runtime, Caddy, local Compose
- Loopback-only publication (`127.0.0.1`)
- Explicit deployment state machine through LIVE

### Prerequisites

None (first phase).

### Security requirements

User code is untrusted. No host-shell execution of user commands. Loopback binds. Docker isolation for workloads. No AWS.

### Verification / exit criteria (met)

Health, projects, queue, deploy pipeline, Caddy on `127.0.0.1:9080`, dashboard on `127.0.0.1:3000` verified in-phase.

### Out of scope (then and still)

Cloud, multi-node, autoscaling, billing, public DNS, authentication UI.

---

## 4. Phase 1 — Complete Core PaaS Workflow

**Status: COMPLETE** (authorized by implement-Phase-1 request)

### Objective

Turn the local foundation into a coherent local PaaS workflow.

### Capabilities (implemented)

- Applications as the deployable unit (`projects` → `applications` → `deployments`)
- Git URL on the application
- `POST /applications/{id}/deployments` and convenience `POST /projects/{id}/deployments` when the project has exactly one app
- Status machine including `stopped`
- Log snapshot `GET /applications/{id}/logs`
- Environment CRUD injected at `docker run`
- Live-app health `GET /applications/{id}/health`
- Create/update/delete application, `POST …/stop`

### Prerequisites

Phase 0 complete.

### Security requirements

Env key/value validation; reserved `PORT` / `FORGE_*`; loopback health probes; no client-supplied shell.

### Verification / exit criteria (met)

Application-scoped deploy, env, logs, health, stop.

### Historical note

Phase 1 did **not** implement custom domains, rollback, log streaming, authentication, AWS, or multi-node. Rollback arrived in Phase 2.

---

## 5. Phase 2 — Developer Experience

**Status: COMPLETE** (authorized `START PHASE 2`)

### Objective

Make the local PaaS practical to operate.

### Capabilities (implemented)

- Dashboard with deployment history, timestamps, persisted `build_log` and `image_name` (rollback artifact; UI need not advertise Docker internals)
- `docker logs -t` snapshots (polling, not streaming)
- Env hide-in-UI and bulk `PUT /applications/{id}/env/bulk`
- Settings: `root_directory`, `health_path`, `local_host` (`.localhost` Host route — not public DNS, not TLS)
- Rollback: `POST /deployments/{id}/rollback` reuses a prior `image_name` (no client-supplied image or command)
- `failed_stage` plus stored build output

### Prerequisites

Phase 1 complete.

### Security requirements

Path confinement for `root_directory`; health path cannot become SSRF; rollback cannot supply an arbitrary image; dashboard still loopback-only.

### Verification / exit criteria (met)

Settings apply on next deploy; rollback skips fetch/build; dashboard uses existing APIs.

### Out of scope (still future)

Public custom domains, TLS automation, log websockets, authentication, AWS, multi-node.

---

## 6. Phase 3 — Security Hardening

**Status: COMPLETE** (authorized `START PHASE 3`).

### Objective

Deepen isolation and introduce **control-plane identity**. Security already applies; this phase added capabilities the loopback era deferred.

### What shipped

**3.a Control-plane authentication and authorization**

- First operator via `POST /auth/bootstrap` (empty `users` table only) or `POST /auth/signup`
- `POST /auth/signup` (name + password + password_confirm) creates another operator with their own `owner_id`
- `POST /auth/login`, `POST /auth/logout`, `GET /auth/me`, `GET /auth/status`
- bcrypt password hashes; sessions store SHA-256 of the token, not the token
- Bearer `Authorization` and `forge_session` cookie. A new login/signup replaces the cookie and revokes the previous session presented by that browser. Cookie wins if both are present.
- All control-plane routes except `/health`, `/auth/status`, `/auth/bootstrap`, `/auth/signup`, `/auth/login`, `/auth/logout` require a session
- Projects have `owner_id`. A UUID in the path is not proof of access (wrong owner → 404)
- Dashboard `/login` asks for Sign in first; a “create an account” link opens Sign up (name + password + confirm)

**3.b Workload and build isolation**

- `docker run`: `--cap-drop ALL`, tmpfs `/tmp`, `--pull never`, existing memory/CPU/pids/`no-new-privileges`/loopback publish
- Still forbidden: Docker socket in workloads, `--privileged`, host network
- Build isolation documented: no host network, no socket; public image pulls remain a supply-chain review item

**3.c Secrets, audit, abuse, supply chain**

- Env in Postgres remains operator metadata, not a vault (comments + architecture)
- `audit_events` for bootstrap, login, project/app/env/deploy/stop/rollback (keys, not values)
- Login/bootstrap rate limit (10 / 10 minutes per client IP; no `X-Forwarded-For`)
- Image/dependency review documented as an operator process, not a scanner product

### Security requirements (held)

Loopback binds unchanged. Caddy was **not** published on `0.0.0.0`.

### Verification / exit criteria

- Unauthenticated mutating calls return 401
- Authorization negatives: another operator cannot list or GET a project (404)
- Workload argv still cannot include Docker socket or `--privileged`
- Env values are not written to audit metadata
- Tests cover the above

### Intentionally deferred

TLS, public bind, secret manager, team RBAC, AWS. Observability is Phase 4 (done). Say `START PHASE 5` for production hardening.

---

## 7. Phase 4 — Observability

**Status: COMPLETE** (authorized `implement phase 4`).

### Objective

Make failures and live behavior diagnosable without SSH folklore.

### What shipped

- Structured JSON `slog` on API (`http` lines: request_id, method, path without query, status, duration_ms) and worker (`deploy stage`, `job finished`, `deployment live` with elapsed_ms). No request bodies, cookies, Authorization, or env values.
- Runtime log streaming: `GET /applications/{id}/logs/stream` (SSE, `docker logs -f`). Snapshot `GET /applications/{id}/logs` remains. Same owner check as other app routes (wrong owner → 404).
- Metrics: `GET /metrics` (session required). Process HTTP counters plus this operator’s deploy totals, last deploy duration, and `docker inspect` running count for their LIVE rows.
- Dashboard `/observe` and live Logs tab (EventSource). Deployment rows expose `duration_ms` and `failed_stage`.
- No tracing. No alerts. No Prometheus/Grafana sidecar. No privileged host agent.

### Security requirements (held)

Streaming and `/metrics` are not public. Telemetry must not include secrets. Loopback binds unchanged.

### Verification / exit criteria

- Unauthenticated `/metrics` and `/logs/stream` return 401
- Another operator’s stream is 404
- Operator can see failed stage + duration on a deployment and live-tail a LIVE app from the dashboard

### Intentionally deferred

Distributed tracing, alert routing, log retention quotas (Phase 5), AI summaries (Phase 10).

---

## 8. Phase 5 — Production Hardening

**Status: FUTURE.** Still **one machine**. Do not implement until `START PHASE 5`.

### Objective

Prepare realistic **external** use of a single node: encryption in transit, quotas, backup, recovery, operational limits.

### Prerequisites

Phase 3 (auth) and Phase 4 (enough observability to see abuse and failed deploys).

### Capabilities (increments)

- TLS for operator-facing and app-facing entry (automation such as ACME is in scope here; **custom DNS product UX** may wait for Phase 7)
- Resource quotas beyond per-container flags (disk, deploy concurrency, log retention)
- Rate limiting
- Backup/restore of PostgreSQL (and documented Redis-loss behavior)
- Disaster-recovery **runbook** and restore test; multi-region DR is Phase 10
- Secure secret management (dedicated secret store or encrypted-at-rest design — not plaintext-equivalent logging)
- Upgrade/migration strategy for control-plane schema

### Security requirements

TLS does not replace authorization. Backups are secrets. Restore drills must not use production credentials in git.

### Verification / exit criteria

Restore tested. TLS verified. Quotas enforced with tests. No “bind to the world” without auth.

---

## 9. Phase 6 — Multi-Node Infrastructure

**Status: FUTURE.** Do not implement until `START PHASE 6`.

### Objective

More than one worker/runtime node without rewriting the control plane.

### Prerequisites

Phase 5. A scheduler is useless if the API is still a loopback toy without backups.

### Capabilities

Worker registration, node health, placement, capacity, failure handling, workload migration **concepts** implemented incrementally.

```text
Control Plane
      │
      ▼
Scheduler
      │
 ┌────┼────┐
 ▼    ▼    ▼
Node Node Node
```

### Security requirements

Node join must be authenticated. Workloads still cannot reach other tenants or the control plane. Network policies between nodes are explicit.

### Verification / exit criteria

A second node can run a deploy the API requested. Failure of one node does not require deleting PostgreSQL. No AWS required for this phase (local or lab VMs are enough).

---

## 10. Phase 7 — Advanced Platform Capabilities

**Status: FUTURE.** Do not implement until `START PHASE 7`.

### Objective

PaaS product depth **on** a secure, observable, multi-node-capable (or still single-node if 6 is skipped by an explicit later decision) foundation.

### Prerequisites

Phase 5 at minimum. Phase 6 if the feature needs more than one node.

### Capabilities (pick increments; do not implement the list at once)

- Preview / staging vs production **semantics**
- Zero-downtime / rolling / canary **on top of existing rollback**
- Public custom domains (DNS), building on Phase 5 TLS
- Persistent volumes
- Scheduled jobs
- Managed-style databases/Redis for **apps** (not replacing control-plane Postgres/Redis)
- Service discovery and explicit app-to-app networking
- Team permissions (beyond single-operator auth)
- Build caching with cache poisoning treated as a security issue
- Extensibility hooks (detect/notify) without host exec

**Already exists (do not re-list as greenfield):** image rollback, HTTP health gate, env vars, log snapshots, `.localhost` DX.

**Not in this phase:** billing as a reason to weaken isolation; AWS account creation.

### Security requirements

Multi-tenant features require tenant isolation tests. Volumes must not mount host secrets. Custom domains must not steal Host routing from other apps (the local slug uniqueness rule is the seed of this).

### Verification / exit criteria

Each increment has tests and a security review. Rollback + new strategy coexist.

---

## 11. Phase 8 — Cloud / AWS Readiness and Deployment

**Status: FUTURE.** Do not implement until `START PHASE 8`.

### Objective

Run the **same roles** on AWS inside the Free Tier envelope (`docs/ARCHITECTURE.md` section 11). No rewrite into a different product.

### Prerequisites

Phase 5. Prefer Phase 6 if more than one worker is in scope. Explicit operator authorization for AWS accounts and spend.

### Capabilities

- Documented mapping: API, PostgreSQL, queue, worker, runtime, proxy
- Adapters behind existing interfaces (`JobQueue`, store, runtime, proxy)
- Idle-cheap, no NAT Gateway as default, cleanup of unused resources
- Self-hosted Compose remains valid

### Security requirements

IAM least privilege. No long-lived keys in git. Workloads still untrusted. Cloud does not justify `--privileged` or Docker socket mounts.

### Verification / exit criteria

A named, authorized deploy that an operator can tear down. Cost assumptions documented. Local path still works.

**Do not start this phase as a side effect of documenting it.**

---

## 12. Phase 9 — High Availability and Scaling

**Status: FUTURE.** Do not implement until `START PHASE 9`.

### Objective

Survive node loss and grow capacity **after** the control plane is already production-shaped.

### Prerequisites

Phase 6 and Phase 8 (or a documented self-hosted HA lab that is not AWS). Autoscaling is not a substitute for quotas (Phase 5).

### Capabilities

HA for control-plane data, worker redundancy, autoscaling **with** resource enforcement.

### Security requirements

Scale-out must not scale privileges. Autoscaling policies cannot bypass isolation.

### Verification / exit criteria

Documented failover test. Cost of extra capacity is explicit.

---

## 13. Phase 10 — Multi-Region and Operational Intelligence

**Status: FUTURE.** Do not implement until `START PHASE 10`.

### Objective

Multi-region operation and better diagnosis — including optional AI **summaries** of signals Forge already collects.

### Prerequisites

Phase 9 or a written exception. Tracing and metrics from Phase 4 must exist before “intelligence” that claims to explain them.

### Capabilities

- Multi-region data and routing (with latency and consistency trade-offs documented)
- Cross-region DR beyond Phase 5 backups
- Deployment intelligence: explain failed stages using stored logs/metrics
- AI-assisted diagnostics **read-only** unless a later increment defines a tool sandbox

### Security requirements

Models do not get cloud credentials or Docker sockets. Prompt injection is treated as untrusted input. No host execution of model-emitted commands.

### Verification / exit criteria

Intelligence features cite evidence from Forge data. Multi-region runbook exists. No silent production AWS expansion.

---

## 14. Phase Gates

A phase should not be considered complete merely because its code exists.

Before moving forward:

**Functional gate** — the intended functionality works.

**Verification gate** — relevant tests and checks pass.

**Security gate** — known security implications have been reviewed.

**Architecture gate** — the implementation remains consistent with `ARCHITECTURE.md`.

**Documentation gate** — important behavior and decisions are documented.

**Review gate** — the changes have been independently reviewed.

**Vision gate** — work matches `PRODUCT_VISION.md` principles without pulling future features forward.

---

## 15. Current Status

**Phase 0 — COMPLETE** (authorized `START PHASE 0`)

**Phase 1 — COMPLETE** (authorized by implement-Phase-1 request)

**Phase 2 — COMPLETE** (authorized `START PHASE 2`)

**Phase 3 — COMPLETE** (authorized `START PHASE 3`)

**Phase 4 — COMPLETE** (authorized `implement phase 4`)

**Phase 5 — FUTURE.** Do not start until the developer says `START PHASE 5`.

Increment 0.1 (done): loopback Postgres + Redis, Go API `GET /health`.

Increment 0.2 (done): `projects` table, migrations, and `POST/GET /projects`.

Increment 0.3 (done): Redis LIST as a transient job queue, `POST /jobs`, and `cmd/worker` consuming an allowlisted `example` job.

Increments 0.4–0.8 (done): `deployments` table and explicit status machine; `POST /projects/{id}/deployments`; worker `deploy` pipeline (fetch, detect, Docker build/run, health check); Caddy on `127.0.0.1:9080`; Next.js dashboard on `127.0.0.1:3000`.

Phase 1 (done): `applications` + `application_env_vars`; deployments belong to an application; worker fetches the app repo and injects env; log snapshot, live health, stop; dashboard application page.

Phase 2 (done): application settings (`root_directory`, `health_path`, `local_host`); persisted `build_log` / `image_name`; rollback from a prior image; bulk env replace; dashboard history.

Phase 3 (done): operator bootstrap/login; project `owner_id`; session cookies/bearer; audit log; login rate limit; tighter `docker run` isolation (`--cap-drop ALL`, tmpfs, `--pull never`). Still loopback. Not TLS, not public bind, not a secret vault, not AWS.

Phase 4 (done): structured API/worker logs; authorized SSE log follow; `/metrics` + `/observe`; deploy `duration_ms`. Not tracing, not alerts, not TLS.

---

## 16. Roadmap Rule

The roadmap describes direction.

It does not authorize implementation.

Knowing that a feature exists in Phase 8 does not authorize implementing it during Phase 2.

The current phase and current increment are always authoritative.
