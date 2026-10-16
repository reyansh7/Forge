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
| **CURRENT** | 0, 1, 2, 3, 4, 5, 6, 7.1, 7.2, 7.3, 7.4, 7.5, 7.6, 7.7, 7.8, 7.9, 7.10, 7.11 | Implemented through Phase 6 plus Phase 7 increments 7.1–7.11 (service links, volumes, preview/production, rolling deploy, env-isolated volumes, custom domains + ACME, GitHub OAuth + webhooks + auto deploy, deployment lease and recovery sweep, personal allowlist + platform hostnames, project Deploy All, workspace diagnostics). AWS apply is still operator work (`docs/AWS.md`). |
| **NEXT** | 7 (further increments) | Remaining Phase 7 items only after the developer names the increment (or `START PHASE 7` again with a pick). |
| **FUTURE** | 8–10 | Documented sequence. Not authorized. |

Security requirements apply in every phase. Phases 3 and 5 add **depth**; they do not mark the start of security.

### Why this order (future phases)

```text
0 Local foundation          COMPLETE
1 Core PaaS                 COMPLETE
2 Developer experience      COMPLETE
3 Security hardening        COMPLETE
4 Observability             COMPLETE
5 Production hardening      COMPLETE
6 Multi-node infrastructure   COMPLETE
7 Advanced platform          IN PROGRESS (7.1–7.11)
8 Cloud / AWS readiness
9 High availability / scaling
10 Multi-region / operational intelligence
```

**Reasoning:** do not distribute or publicly expose an unauthenticated, weakly observed control plane. Authentication and deeper isolation (Phase 3) come before leaving loopback. Observability (Phase 4) comes before calling the system production. Production hardening (Phase 5) stays **single-node** so TLS, backups, and quotas exist before a second worker. Multi-node (Phase 6) waits until one node is trustworthy. Advanced PaaS features (Phase 7) wait until operation is honest. AWS (Phase 8) maps existing roles; it is not a rewrite. HA and multi-region come last.

This **reorders unimplemented work**. Former “Phase 5 multi-node before Phase 6 production hardening” is rejected as less safe. Completed Phases 0–5 keep their numbers and status.

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
- All control-plane routes except `/health`, `/auth/status`, `/auth/bootstrap`, `/auth/signup`, `/auth/login`, `/auth/github`, `/auth/logout`, `/integrations/github/callback`, and `POST /webhooks/github/{id}` (HMAC required) require a session
- Projects have `owner_id`. A UUID in the path is not proof of access (wrong owner → 404)
- Dashboard `/login` asks for Sign in first; a “create an account” link opens Sign up (name + password + confirm). **Continue with GitHub** is on the same page when the OAuth app is configured

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

**Status: COMPLETE** (authorized `START PHASE 5` / `START PAHSE 5`). Still **one machine**.

### Objective

Prepare realistic **external** use of a single node: encryption in transit, quotas, backup, recovery, operational limits.

### Prerequisites

Phase 3 (auth) and Phase 4 (enough observability to see abuse and failed deploys).

### Capabilities (implemented)

- **Operator TLS (opt-in):** `go run ./cmd/forge-cert` writes `.forge/tls/`; set `FORGE_TLS_CERT_FILE` / `FORGE_TLS_KEY_FILE`. Default API remains HTTP on loopback. ACME is not enabled (`auto_https off`); public hostname UX is Phase 7
- **App-facing TLS:** Caddy `:443` with `tls internal`, published `127.0.0.1:9443`. HTTP `127.0.0.1:9080` remains. `FORGE_PROXY_PUBLIC_BASE` stays the HTTP URL
- **Quotas:** projects per owner, in-flight deploys per owner, fetched workspace bytes, build-log retention prune
- **Rate limiting:** login/bootstrap (Phase 3) plus per-actor deploy attempts (10 / 10 minutes)
- **Backup/restore:** `go run ./cmd/backup` / `-restore`; Redis-loss documented in `docs/DISASTER_RECOVERY.md`
- **Env at rest:** AES-256-GCM (`enc:v1:`), key in `.forge/data.key` or `FORGE_DATA_KEY`. Not a vault
- **Schema upgrades:** `docs/UPGRADES.md`; `GET /operator/status` lists applied migrations
- **Public bind:** `0.0.0.0` / unspecified rejected unless `FORGE_ALLOW_PUBLIC_BIND=1`

### Security requirements

TLS does not replace authorization. Backups are secrets. Restore drills must not use production credentials in git.

### Verification / exit criteria (met)

Backup dump test when Postgres is reachable. Quota and rate-limit HTTP tests. Workspace disk-cap deploy test. Env cell ≠ plaintext when a crypter is set. Bind-to-world rejected without the flag. TLS pair required together.

### Out of scope (then and still)

Multi-node, ACME on a public hostname, team vault product, `0.0.0.0` as the default bind.

---

## 9. Phase 6 — Multi-Node Infrastructure

**Status: COMPLETE** (authorized `START PHASE 6`)

### Objective

More than one worker/runtime node without rewriting the control plane.

### Prerequisites

Phase 5. A scheduler is useless if the API is still a loopback toy without backups.

### Capabilities (implemented)

Worker registration (`POST /nodes` join token), node heartbeat and stale → `dead`, scheduler placement (`internal/schedule`), per-node Redis LIST + inflight cap, node-scoped abandon of interrupted deploys, Caddy `advertise_host`, Observe node list. Workload move is Stop + Deploy (not live migrate). No AWS, no Kubernetes, no overlay mesh.

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

### Verification / exit criteria (met)

A second worker process (`FORGE_NODE_NAME` + join token) consumes `forge:jobs:{id}` and can run a deploy the API placed. Marking a node `dead` does not drop PostgreSQL. Lab: two `go run ./cmd/worker` processes on one machine. No AWS.

---

## 10. Phase 7 — Advanced Platform Capabilities

**Status: IN PROGRESS** (authorized `START PHASE 7`). Increments 7.1–7.9 complete. 7.8 is the deployment lease and recovery sweep (`docs/DEPLOYMENT_RECOVERY.md`). 7.9 is the personal allowlist and `{slug}.{FORGE_APPS_DOMAIN}` hostnames. 7.10 is project Deploy All: one action queues the existing per-service jobs and holds previous containers until every required service is live. If a required service fails after another was promoted, compensation restores those services in reverse promotion order and records a failure when the previous container is gone, the database restore fails, or Caddy does not switch back. 7.11 is workspace-quota diagnostics and per-stage deploy timings. It does not raise the 256 MiB default. The operator backup drill is still required before any AWS claim. Do not implement the rest of the list until the developer picks the next increment.

### Objective

PaaS product depth **on** a secure, observable, multi-node-capable (or still single-node if 6 is skipped by an explicit later decision) foundation.

### Prerequisites

Phase 5 at minimum. Phase 6 if the feature needs more than one node.

### Increment 7.1 — same-project service links (COMPLETE)

Operator declares that application A receives application B’s Host URL as an env key on the next deploy. Same project only. No overlay mesh. No custom domains.

### Increment 7.2 — persistent volumes (COMPLETE)

Operator declares a Docker **named** volume mounted at a container path. Data survives Stop + Deploy. Host bind mounts are rejected. Volumes are node-local; placement prefers the last node when volumes exist. No size quota, no sharing across apps, no live migrate.

### Increment 7.3 — preview vs production (COMPLETE)

Each application has two first-class environments: `production` and `preview`. One LIVE row per `(application_id, environment)`. Production keeps `{slug}.localhost`. Preview is `{slug}-preview.localhost`. At 7.3, preview did not mount named volumes (isolation by omission). Increment 7.5 gives preview its own Docker volume names. Service links still inject the production Host URL. Chat named this increment “7.2”; persistent volumes already occupied that number, so the repository records it as 7.3.

### Increment 7.4 — rolling deployments (COMPLETE)

A new container starts beside the current LIVE container. Caddy switches the Host only after health checks pass. Then the previous container is stopped. A failed deploy leaves the previous LIVE version running. Preview and production roll independently. Not canary (no split traffic), not live migrate across nodes.

### Increment 7.5 — persistent volumes, environment isolation (COMPLETE)

Operator-declared named volumes already existed (7.2). This increment mounts them on **both** environments with isolated Docker names (`forgevol_<app>_<name>` production, `forgevol_<app>_preview_<name>` preview) so trial data cannot clobber live files. Rolling deploys remount the same env-scoped volume while the old container is still up; `docker rm` never passes `-v`. The worker never deletes volumes. Operator delete is blocked while any environment is LIVE and removes both Docker names. Chat named this increment “Phase 7.5”; 7.2 remains the original volume schema. No new migration.

### Increment 7.6 — custom domains + ACME TLS (COMPLETE)

Operator adds a public hostname to an application environment. The name is unique across every application. Ownership is a DNS TXT at `_forge-challenge.<hostname>` before Caddy routes it. Verified domains become dedicated Caddy sites with `tls { issuer acme }` (Let's Encrypt). `*.localhost` and `/d/{id}/` stay on catch-all `:80` / `:443` with `tls internal` and `auto_https off`. ACME certs persist in the Caddy `/data` volume. Not wildcards, not DNS-01, not GitHub, not publishing `:2019`.

### Increment 7.7 — GitHub OAuth + webhooks + auto deploy (COMPLETE)

Operators connect a GitHub account (OAuth App via `FORGE_GITHUB_CLIENT_ID` / `FORGE_GITHUB_CLIENT_SECRET`), pick owner/repo + branch, and Forge creates a `push` webhook. Tokens and webhook secrets are sealed at rest and never returned to the dashboard. `POST /webhooks/github/{application_id}` is public but HMAC-verified (`X-Hub-Signature-256`). Duplicate `X-GitHub-Delivery` is ignored. A matching branch push creates a queued deployment with commit SHA/message/author and RPUSH `TypeDeploy` — the same rolling pipeline as the Deploy button (preview/production, volumes, domains, TLS). Manual deploy is unchanged. GitHub Actions is not used. Chat named this increment; it was not on the remaining-7 list, so the repository records it as 7.7.

The login page also offers **Continue with GitHub** (`GET /auth/github`, same OAuth app and callback). A `github_oauth_states` row with a NULL `user_id` is a sign-in. The callback requires the `forge_github_state` cookie from the browser that started it. Identity match is `github_user_id`. A new GitHub user gets a Forge user with a random unused password hash. A password account is not taken over because the GitHub login string matches its username. Connecting a repo stays on the application GitHub tab.

### Increment 7.8 — deployment lease and recovery sweep (COMPLETE)

A worker lease and an API/worker sweeper. Lost queued messages and expired leases on `detecting`/`building` are re-queued at most twice. Expired leases on `provisioning` and later fail that attempt and stop its container without replacing the previous live row. Stale job ids and lost leases cannot cut over. Not a durable queue product and not Phase 8. The operator backup/restore drill is still required before any AWS claim.

### Increment 7.9 — personal allowlist and platform hostnames (COMPLETE)

GitHub login and GitHub connect require `FORGE_GITHUB_ALLOWED_USER_IDS` (numeric GitHub user ids). An empty list refuses both. Webhook trust stays the HMAC check. When `FORGE_APPS_DOMAIN` is set, Forge reserves `https://{slug}.{zone}/` as an active `application_domains` row with `origin=platform` (migration 0017). Those names skip the TXT challenge. Custom domains under that zone are rejected. Caddy routes platform hosts and an optional `FORGE_CONTROL_HOST` (dashboard + `/forge-api` webhooks) only when the zone is set, reusing the 7.6 per-hostname ACME issuer. Local Compose stays on loopback. `deploy/aws/` and `docs/AWS.md` are the single-host install package (public 80/443 only). Not DNS-01, not a wildcard certificate, not an applied AWS account.

### Increment 7.10 — project Deploy All (partial)

`POST /projects/{id}/deploy` snapshots `detect.Discover`, ensures one application per selected service (Go `build_package` such as `./cmd/api` stays at the module root), creates a `project_deployments` saga (migration 0019), and queues the existing per-application deploy jobs. Independent services may build together up to `FORGE_MAX_INFLIGHT_PER_NODE`. A service waits until a lower-ranked dependency is live or failed. Obvious one-frontend/one-backend and one-backend/one-ML pairs become service links. Backends, workers, and ML services stay off Caddy unless `local_host` is set; they join a per-project Docker bridge (`forgep-` plus the project id). Link env keys are unchanged. A private target's value is its bridge URL. A public target keeps the Caddy URL.

The previous container is kept until every required service is live (migration 0020). If a required service fails after another was promoted, compensation restores the previous release in reverse promotion order and records a failure when the previous container is gone, the database restore fails, or Caddy does not switch back. That does not reverse database migrations or delete volumes. A Go build error that names exactly one `./cmd/<name>` is repaired by setting `build_package` and rebuilding once. Two commands are not guessed. An unhealthy release is not promoted.

### Increment 7.11 — workspace diagnostics (COMPLETE)

Authorized in chat as the measurement increment after 7.10. A fetched tree that exceeds `FORGE_WORKSPACE_MAX_BYTES` (default `256 << 20`, 256 MiB) fails before build with the configured limit, the measured regular-file total, and the largest few relative paths. Symlinks are not followed. The error is the application quota, not a full host disk. The worker logs `deploy measurements` with fetch, detect, build, container-start, health, and total durations. Stages that did not run are `unavailable`, not 0. `transferred_bytes` is always `unavailable` because shallow `git clone` does not report wire size. `workspace_bytes` is the extracted regular-file sum when the walk finishes. No migration. The default cap is unchanged. Not BuildKit, not archive fetch, not a larger quota.

### Remaining capabilities (do not implement the list at once)

- Zero-downtime **canary** (weighted split) **on top of rolling + rollback**
- Scheduled jobs
- Managed-style databases/Redis for **apps** (not replacing control-plane Postgres/Redis)
- Team permissions (beyond single-operator auth)
- Build caching with cache poisoning treated as a security issue
- Extensibility hooks (detect/notify) without host exec

**Already exists (do not re-list as greenfield):** image rollback, HTTP health gate, env vars, log snapshots, `.localhost` DX, same-project service links (7.1), persistent named volumes (7.2), preview vs production (7.3), rolling cutover (7.4), environment-isolated volume mounts (7.5), custom domains + Caddy ACME (7.6), GitHub OAuth + auto deploy (7.7), deployment lease and recovery sweep (7.8), personal GitHub allowlist and platform hostnames (7.9), project Deploy All with reverse-order compensation (7.10), workspace-quota diagnostics and stage timings (7.11).

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

**Phase 5 — COMPLETE** (authorized `START PHASE 5`)

**Phase 6 — COMPLETE** (authorized `START PHASE 6`)

**Phase 7 — IN PROGRESS** (authorized `START PHASE 7`). Increments 7.1–7.11 done (service links, volumes, preview/production, rolling, env-isolated volumes, custom domains + ACME, GitHub OAuth + webhooks + auto deploy, deployment lease and recovery sweep, personal allowlist and platform hostnames, project Deploy All, workspace diagnostics and stage timings). Cron, team RBAC, canary, managed app datastores, build cache, and hooks are not implemented. The Postgres backup drill is not marked done. AWS is documented in `docs/AWS.md` and is not applied.

Increment 0.1 (done): loopback Postgres + Redis, Go API `GET /health`.

Increment 0.2 (done): `projects` table, migrations, and `POST/GET /projects`.

Increment 0.3 (done): Redis LIST as a transient job queue, `POST /jobs`, and `cmd/worker` consuming an allowlisted `example` job.

Increments 0.4–0.8 (done): `deployments` table and explicit status machine; `POST /projects/{id}/deployments`; worker `deploy` pipeline (fetch, detect, Docker build/run, health check); Caddy on `127.0.0.1:9080`; Next.js dashboard on `127.0.0.1:3000`.

Phase 1 (done): `applications` + `application_env_vars`; deployments belong to an application; worker fetches the app repo and injects env; log snapshot, live health, stop; dashboard application page.

Phase 2 (done): application settings (`root_directory`, `health_path`, `local_host`); persisted `build_log` / `image_name`; rollback from a prior image; bulk env replace; dashboard history.

Phase 3 (done): operator bootstrap/login; project `owner_id`; session cookies/bearer; audit log; login rate limit; tighter `docker run` isolation (`--cap-drop ALL`, tmpfs, `--pull never`). Still loopback. Not TLS, not public bind, not a secret vault, not AWS.

Phase 4 (done): structured API/worker logs; authorized SSE log follow; `/metrics` + `/observe`; deploy `duration_ms`. Not tracing, not alerts, not TLS.

Phase 5 (done): opt-in API TLS; Caddy `tls internal` on `127.0.0.1:9443`; env AES-GCM at rest; project/deploy/workspace/log quotas; deploy rate limit; `cmd/backup`; `docs/DISASTER_RECOVERY.md` + `docs/UPGRADES.md`; `FORGE_ALLOW_PUBLIC_BIND` for `0.0.0.0`. Still one node. Not ACME, not a vault, not multi-node.

Phase 6 (done): `nodes` table; `EnsureLocalNode`; join token; heartbeat / stale → dead; `internal/schedule`; `forge:jobs:{node_id}`; Caddy advertise host; Observe nodes. Not live migrate, not AWS. A queued deploy whose Redis job was lost is re-queued with `POST /deployments/{id}/retry` (`active_job_id` in migration 0014). `FORGE_ALLOW_PUBLIC_BIND=1` also requires API TLS and a non-default Postgres password.

Phase 7.8 (done as a recovery gate, not a new product feature): worker lease (`0015_deployment_lease.sql`), sweeper for expired leases and lost queued messages, bounded automatic re-queue, owned status writes so a stale pipeline cannot cut over. Documented in `docs/DEPLOYMENT_RECOVERY.md`. This is not Phase 8. The backup/restore drill is still an operator step and is not marked done by unit tests. AWS remains Phase 8.

Phase 7.1 (done): `application_links`; worker injects sibling `{slug}.localhost` URLs; dashboard Services tab. Not a mesh, not custom domains.

Phase 7.2 (done): `application_volumes`; worker mounts Forge-owned Docker named volumes; dashboard Storage tab. Not host binds, not a mesh, not size quotas.

Phase 7.3 (done): `deployments.environment` (`preview`|`production`); unique LIVE per app+env; preview Host `{slug}-preview.localhost`; dashboard environment selector. Not custom domains, not staging as a third slot, not canary.

Phase 7.4 (done): rolling cutover — new container + health + Caddy Host switch, then stop previous. Failed health keeps LIVE. Not canary.

Phase 7.5 (done): preview and production mount isolated Docker named volumes; rolling remounts the same env volume without `docker volume rm`. Not size quotas, not host binds, not managed databases.

Phase 7.6 (done): `application_domains`; DNS TXT ownership; Caddy ACME site blocks for verified hostnames; `.localhost` unchanged. Not wildcards, not DNS-01, not GitHub.

Phase 7.7 (done): GitHub OAuth connect/disconnect; repo+branch binding; HMAC webhooks; delivery-id dedupe; auto-deploy reuses `TypeDeploy` with commit metadata. Tokens never in JSON. Not GitHub Actions, not a second pipeline. Sign-in with GitHub uses the same OAuth app (`GET /auth/github`); it matches `github_user_id` and does not take over a password account by name.

Phase 7.8 (done): `lease_until` and `recovery_count`; worker renews the lease; API and worker sweep expired leases and queued rows whose message was never taken. Early stages re-queue at most twice. Later stages fail closed without replacing the previous live row. Not AWS, not a durable Redis, not HA.

Phase 7.9 and 7.10 (done): personal allowlist, platform hostnames, and project Deploy All with reverse-order compensation. The full write-up is in section 10.

Phase 7.11 (done): workspace quota failures name the limit, measured regular-file bytes, and the largest few relative paths. `deploy measurements` logs stage durations. `transferred_bytes` is `unavailable`. Default cap stays 256 MiB. Not build cache, not a larger quota.

---

## 16. Roadmap Rule

The roadmap describes direction.

It does not authorize implementation.

Knowing that a feature exists in Phase 8 does not authorize implementing it during Phase 2.

The current phase and current increment are always authoritative.
