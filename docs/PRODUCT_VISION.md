# Forge Product Vision

This document describes **long-term direction**.

It does **not** authorize implementation.

Authorized work lives only in the current phase of `docs/ROADMAP.md`, after the developer says `START PHASE N`. Capabilities here that are not marked as current in `docs/ARCHITECTURE.md` must not be built merely because they appear in this file.

Competitive ambition must never override security, correctness, architectural integrity, phase discipline, or maintainability. See `docs/DEVELOPMENT_RULES.md`.

---

## 1. What Forge is for

Forge is a **self-hosted Platform-as-a-Service**. The durable loop is:

```text
Git repository → detect → build → isolated run → health → route → observe
```

The near-term product is a **correct, understandable, loopback-local PaaS** that the operator can explain in an interview and run on one machine.

The long-term product is a **production-capable PaaS** that a small team can self-host, and that can later run on AWS without a rewrite — with a better *engineering and operational* experience than typical git-push platforms, not a larger feature catalog.

“Better” means:

- git to a running app with fewer surprises
- failures that are diagnosable
- workloads that cannot take over the host
- infrastructure that stays understandable as it grows
- cost that stays predictable (including a first AWS deploy that fits the Free Tier)

It does **not** mean copying Vercel, Railway, Render, Fly.io, Coolify, or Netlify. Those products are context, not a specification.

---

## 2. Current vs direction

**Current (Phase 5, local):** one machine, loopback binds (world-bind only with `FORGE_ALLOW_PUBLIC_BIND=1`), operator sessions, Docker workloads with cap-drop, Caddy HTTP `127.0.0.1:9080` and HTTPS `127.0.0.1:9443` (`tls internal`), optional `{slug}.localhost` (not public DNS), explicit deployment state machine, HTTP health at go-live, log snapshots and SSE follow, `/metrics` + `/observe`, env vars sealed at rest, image-based rollback, Postgres backup/restore, project/deploy/workspace quotas. Opt-in API TLS. No ACME. No tracing product. No multi-tenant internet exposure.

**Direction:** the same control-plane / workload-plane split, evolved through roadmap phases into a system an operator can expose, observe, and operate with confidence.

Do not treat this gap as a bug. It is the phase gate.

---

## 3. Differentiation (principles, not a backlog)

Forge should win on **outcomes**, not on checkbox count.

**Understandable infrastructure.** A senior engineer can draw the path from API to worker to container to proxy and name every trust boundary. Magic PaaS layers that hide that path are a non-goal.

**Untrusted by default.** User repositories, Dockerfiles, build scripts, dependencies, and runtime inputs stay hostile. Isolation is the product, not an afterthought.

**Explicit state.** Deployments have named stages and stored failure reasons. Rollback is a real control-plane action against a known artifact, not “hope the last git SHA still builds.”

**Replaceable seams.** API, durable store, transient queue, worker, runtime, and reverse proxy stay distinct so local Compose can later map onto AWS roles without becoming a different product.

**Self-host first, cloud-ready second.** Operators keep the software. Managed hosting, if it ever exists, runs the same architecture — it does not become a proprietary control plane that only works in one vendor’s console.

**Cost as architecture.** Idle-cheap, cleanup-by-design, Free Tier–aware first cloud. Scale is a later choice, not the default topology.

---

## 4. Developer experience

The target experience is: connect a git remote, deploy, open a URL, see why it failed if it failed.

That requires honest status, short time-to-first-success, settings that apply on the next deploy (not silent mutation of a live process), and diagnostics that show *stage* and *log*, not a generic 500.

Git → production **simplicity** is a design goal. It is not permission to skip detection, isolation, health, or authorization.

---

## 5. Build and runtime

Detection should stay **evidence-based** (what is in the tree), not a marketing list of frameworks. Today that is a small set of file markers and Docker; richer detection is a later increment, not a reason to execute host scripts from the repo.

**Containers are the isolation model.** Non-Docker runtimes are only interesting if they preserve the same untrusted-code boundary. They are not a Phase 3 requirement.

**Monorepos** belong as `root_directory`-style confinement (already a local setting) plus later service graphs — not as unbounded host path walks.

**Multi-service apps** are multiple applications (or later explicit services) with explicit networking, not one container that is allowed to be the network.

---

## 6. Delivery and recovery

Preview, staging, and production are **environment semantics** on top of the same deployment machine, added when isolation and auth can tell tenants and environments apart.

Zero-downtime and advanced strategies (rolling, canary) come **after** a reliable single-instance go-live and a working rollback. Phase 2 already reuses a prior image; later work is traffic shifting, not inventing rollback from scratch.

Health checks stay a **gate to LIVE**, not a substitute for logs or metrics.

---

## 7. Observability and operations

Operators should answer: what happened, where, why, and what is happening now.

Logs (including streaming), metrics, tracing, and alerts are **phased**. Snapshots, SSE follow, structured API/worker logs, and session-gated `/metrics` exist now; alert routing and distributed traces do not.

**Deployment intelligence** means using those signals to explain failures — still bounded by verification. **AI-assisted diagnostics** may later summarize logs; they must never become a path that executes model output on the host or inside the control plane.

---

## 8. Data, work, and networks

Environment variables today are operator metadata in Postgres, not a secret manager. Secrets, managed databases, Redis-as-a-service for apps, cron, volumes, and service discovery are **platform capabilities** that must not punch holes from workload to control plane.

The reverse proxy remains the public entry. Application ports stay unpublished to the internet. `.localhost` slugs stay local DX until a later phase introduces real DNS and TLS.

---

## 9. Scale, tenancy, and hosting

Resource limits, quotas, autoscaling, multi-node, HA, and multi-region are **ordered**. A second node is not a substitute for authentication. A second region is not a substitute for backups.

**Tenant isolation** is a security property: one application’s compromise must not become another tenant’s, or the control plane’s.

**Self-hosted Forge** is the default product shape. **Managed Forge** is the same software operated for people — only after the self-hosted path is production-hardened.

**AWS** is the first cloud target, Free Tier first, interfaces preserved. It is not implemented now.

---

## 10. Extensibility

Plugins and integrations should attach at documented seams (detect, build, notify, auth identity) without granting workloads host privileges. Prefer a small number of explicit extension points over a plugin marketplace in early phases.

---

## 11. Security (non-negotiable)

User code remains untrusted. The control plane must not execute it on the host. Builds and runs stay isolated. Least privilege, validation, secret hygiene, network restrictions, resource bounds, and auditability only **tighten** over time.

Security is continuous from Phase 0. Dedicated roadmap phases add depth; they do not postpone the principle.

---

## 12. What this file must not become

This is not a promise to ship every capability named in industry PaaS marketing. Unused features are a liability. If a capability does not improve isolation, reliability, diagnosis, or honest git-to-run, it waits or is rejected.
