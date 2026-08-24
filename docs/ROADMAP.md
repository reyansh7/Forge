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

---

## 2. Phase 0 — Local Deployment Foundation

### Objective

Build the smallest complete deployment path on a local machine.

The goal is to prove the fundamental Forge loop:

```text
Git Repository
      ↓
Detect
      ↓
Build
      ↓
Container
      ↓
Run
      ↓
Health Check
      ↓
Caddy
      ↓
Accessible Application
```

### Initial components

Phase 0 establishes the minimum viable foundation for:

- Go API
- Next.js dashboard
- PostgreSQL
- Redis
- worker
- Docker-based application execution
- Caddy reverse proxy
- local development environment

### Phase 0 principles

The implementation must remain local.

Do not implement:

- cloud deployment
- multi-node scheduling
- autoscaling
- distributed workers
- production infrastructure
- advanced observability
- billing
- enterprise features

unless explicitly introduced by an approved increment.

### Incremental development

Phase 0 is divided into small increments.

Each increment must:

- Have a clearly defined objective.
- Change only the necessary components.
- Be implemented.
- Be verified.
- Be reviewed.
- Pass security checks where relevant.
- Be documented when architectural behavior changes.

---

## 3. Phase 1 — Complete Core PaaS Workflow

### Objective

Turn the local deployment foundation into a coherent local PaaS workflow.

Potential capabilities include:

- project creation
- application creation
- Git repository configuration
- deployment creation
- build status
- deployment status
- application logs
- environment configuration
- health status
- basic application management

The exact scope is determined after Phase 0 is stable.

---

## 4. Phase 2 — Developer Experience

### Objective

Make Forge practical and pleasant to use.

Potential areas:

- improved dashboard
- deployment history
- better logs
- environment variable management
- build configuration
- application settings
- custom domains
- improved error reporting
- deployment rollback

Only implement features after their underlying infrastructure is reliable.

---

## 5. Phase 3 — Security Hardening

### Objective

Strengthen isolation and security around untrusted workloads.

Areas include:

- container hardening
- filesystem restrictions
- resource limits
- network restrictions
- capability reduction
- secret handling
- authentication/authorization hardening
- audit logging
- abuse prevention
- supply-chain considerations

Security is not deferred entirely until this phase. Security requirements apply from Phase 0 onward.

This phase represents deeper hardening, not the beginning of security.

---

## 6. Phase 4 — Observability

### Objective

Make Forge operationally understandable.

Potential capabilities:

- structured logs
- metrics
- tracing
- deployment telemetry
- worker health
- application health
- build diagnostics
- error tracking

Observability should make it possible to answer:

- What happened?
- Where did it fail?
- Why did it fail?
- What is happening now?

---

## 7. Phase 5 — Multi-Node Infrastructure

### Objective

Move beyond the initial single-machine architecture.

Potential capabilities:

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

Potential areas:

- worker registration
- node health
- scheduling
- workload placement
- capacity tracking
- workload migration
- failure handling

Distributed infrastructure must not be introduced until the single-node system is understood and stable.

---

## 8. Phase 6 — Production Hardening

### Objective

Prepare Forge for realistic external usage.

Potential areas:

- stronger authentication
- rate limiting
- resource quotas
- backup/restore
- disaster recovery
- secure secret management
- TLS automation
- stronger isolation
- operational tooling
- upgrade/migration strategy

---

## 9. Phase 7 — Advanced Platform Capabilities

Potential future areas:

- autoscaling
- deployment strategies
- rollbacks
- preview environments
- custom domains
- persistent volumes
- scheduled jobs
- managed databases
- service discovery
- advanced networking
- team/project permissions
- billing

These are future capabilities, not commitments to implement them immediately.

---

## 10. Phase 8 — Long-Term Platform Evolution

Possible long-term directions include:

- multi-region infrastructure
- high availability
- distributed scheduling
- advanced workload isolation
- sophisticated build caching
- infrastructure-as-code integration
- plugin/extensibility systems
- advanced developer tooling
- agent-assisted operations

These are deliberately deferred.

---

## 11. Phase Gates

A phase should not be considered complete merely because its code exists.

Before moving forward:

**Functional gate** — the intended functionality works.

**Verification gate** — relevant tests and checks pass.

**Security gate** — known security implications have been reviewed.

**Architecture gate** — the implementation remains consistent with `ARCHITECTURE.md`.

**Documentation gate** — important behavior and decisions are documented.

**Review gate** — the changes have been independently reviewed.

---

## 12. Current Status

**Phase 0 — IN PROGRESS** (authorized `START PHASE 0`)

**Current increment: 0.2 — PostgreSQL persistence (Project)**

Increment 0.1 (done): loopback Postgres + Redis, Go API `GET /health`.

Increment 0.2 (this work): `projects` table, migrations, and `POST/GET /projects`. This increment does **not** detect, build, deploy, execute user code, serve a dashboard, run a worker, use Redis as a queue, or configure Caddy.

Later Phase 0 increments will add the rest of the local deployment loop.

---

## 13. Roadmap Rule

The roadmap describes direction.

It does not authorize implementation.

Knowing that a feature exists in Phase 5 does not authorize implementing it during Phase 0.

The current phase and current increment are always authoritative.
