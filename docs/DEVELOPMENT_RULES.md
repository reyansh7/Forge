# Forge Development Rules

## 1. Core Principle

Forge is an infrastructure project.

Correctness, security, understanding, and verification are more important than implementation speed.

Follow:

```text
CORRECTNESS > SPEED
SECURITY > CONVENIENCE
UNDERSTANDING > MAGIC
VERIFICATION > CLAIMS
SMALL CHANGES > HUGE GENERATION
SIMPLE SYSTEMS > TOOL SPRAWL
PHASE DISCIPLINE > FEATURE VELOCITY
```

**Competitive goals must never override security, correctness, architectural integrity, phase discipline, or maintainability.**

Wanting a better experience than Vercel, Railway, Render, Fly.io, Coolify, Netlify, or similar platforms does not authorize skipping isolation, validation, or phase gates.

---

## 2. Phase Discipline

Only work on the current authorized phase.

If the current phase is Phase 0, do not implement Phase 1+ functionality unless explicitly authorized.

Do not interpret TODOs, roadmap entries, comments, future architecture, `docs/PRODUCT_VISION.md`, documentation, or user ideas as authorization to implement future functionality.

`docs/PRODUCT_VISION.md` describes strategic direction. It does **not** authorize implementation.

---

## 3. Incremental Development

Never attempt to build a large subsystem in one uncontrolled change.

Prefer:

```text
Small Increment
    ↓
Implement
    ↓
Run Verification
    ↓
Review
    ↓
Continue
```

Each increment should have one clear objective.

Avoid unrelated refactoring during feature work.

---

## 4. Understand Before Editing

Before modifying code:

- Understand the relevant architecture.
- Inspect the existing implementation.
- Identify dependencies.
- Identify security implications.
- Determine the smallest appropriate change.

Do not modify files merely because they appear related.

---

## 5. Source of Truth

Documents have different jobs. Do not collapse them:

```text
docs/PRODUCT_VISION.md       long-term direction; does not authorize implementation
docs/ROADMAP.md              phased implementation; START PHASE N authorizes work
docs/ARCHITECTURE.md         technical architecture and constraints
docs/DEVELOPMENT_RULES.md    engineering and security rules (this file)
docs/CURSOR_ENVIRONMENT.md   Cursor tooling only
AGENTS.md                    agent operating contract
```

If implementation conflicts with architecture documentation:

1. STOP.
2. Explain the conflict.
3. Do not silently choose an implementation.

---

## 6. User Code Is Untrusted

This is a non-negotiable rule.

Any application code, repository content, build configuration, command, dependency, or runtime input originating from a user must be treated as untrusted.

Never:

- execute user code directly on the control-plane host
- trust a repository's build script
- trust Dockerfiles
- trust package scripts
- expose control-plane secrets to workloads
- give workloads unnecessary privileges
- treat model/LLM output as a trusted shell command

---

## 7. Security Boundaries

Security boundaries must be explicit.

Important boundaries include:

```text
User
 ↓
API
 ↓
Control Plane
 ↓
Worker
 ↓
Isolated Build
 ↓
Isolated Runtime
```

Do not bypass these boundaries for convenience.

If an implementation requires bypassing a security boundary, stop and request review.

## 7.1 Threat modeling

Infrastructure, Docker, proxy, queue, auth, and network changes require a short threat model before implementation: what is untrusted, what is the blast radius, what fails closed.

Do not add a listener, bind, capability, or mount “to make DX nicer” without that review.

## 7.2 Tenant isolation

Until multi-tenant identity exists, assume a single local operator. When tenancy exists, one application must not read another’s env, logs, volumes, or routing. IDs in URLs are not authorization.

---

## 8. Secrets

Never:

- hardcode secrets
- commit secrets
- print secrets
- include credentials in logs
- copy tokens into documentation
- paste credentials into chat
- expose secrets to unrelated services

Use environment/configuration mechanisms appropriate to the current development phase.

Production secret management is a later roadmap phase (not current). Secret **hygiene** applies from the first commit.

---

## 9. Git Safety

Do not perform destructive Git operations without explicit authorization.

Examples include:

- `git reset --hard`
- force push
- rewriting history
- deleting branches
- destructive repository cleanup

Prefer small commits with understandable changes.

Never commit:

- `.env`
- credentials
- private keys
- tokens
- production configuration containing secrets

---

## 10. Shell Safety

Do not execute commands blindly.

Before running a potentially destructive command, understand:

- what it modifies
- what it deletes
- what permissions it requires
- whether it affects the host
- whether it affects production infrastructure

Particular caution is required around:

- `rm`
- `rmdir`
- `del`
- format
- disk operations
- `docker system prune`
- docker volume deletion
- database deletion
- cloud destroy operations

---

## 11. Dependencies

Do not add dependencies without justification.

Before adding a dependency consider:

- Is it actually necessary?
- Does the standard library solve the problem?
- Is it maintained?
- Is the license appropriate?
- Does it introduce unnecessary security risk?
- Does it duplicate an existing dependency?

Prefer a smaller dependency graph.

Review new dependencies for maintenance, license, and supply-chain risk. Pin versions where the project already pins. Do not add AWS SDKs or cloud client libraries until Phase 8 is authorized.

Container **base images** and Dockerfiles from user repos are untrusted input, not a trusted supply chain. Forge-written Dockerfiles must stay minimal and not pull secrets at build time.

---

## 12. Testing and Verification

Never claim "it works" without verification.

Verification should match the change.

**Go** — run relevant tests, formatting, and static analysis/build checks.

**TypeScript / Next.js** — run relevant type checking, linting, tests, and build verification.

**Docker** — verify image build, container startup, expected ports, and health checks.

**Infrastructure / security** — verify isolation assumptions, binds, and authorization negatives where the change touches them.

Do not run massive test suites unnecessarily when a focused check is sufficient.

---

## 13. Failure Handling

When something fails, do not immediately patch randomly.

Instead:

```text
Failure
  ↓
Observe
  ↓
Reproduce
  ↓
Identify root cause
  ↓
Make smallest fix
  ↓
Verify
```

Do not hide failures by weakening tests or suppressing errors.

---

## 14. Logging

Logs should help diagnose systems.

Avoid:

- logging secrets
- logging unnecessary sensitive data
- noisy debug output in production paths
- swallowing important errors

Prefer structured, meaningful logs where appropriate.

---

## 15. API Design

APIs should be:

- explicit
- predictable
- validated
- authenticated where required
- authorized
- versionable where appropriate

Do not expose internal infrastructure details unnecessarily.

Validate external input at system boundaries.

---

## 16. Database Rules

PostgreSQL is the durable source of truth for Forge state.

Do not use Redis as a replacement for durable state.

Database changes must:

- be deliberate
- be migration-safe
- preserve existing data where applicable
- be reviewed before destructive changes
- remain backwards compatible or ship an explicit migration with a rollback story

Never casually delete or reset databases.

---

## 17. Docker / Container Rules

Containers are security boundaries, but a container should not automatically be considered a complete security solution.

For user workloads:

- minimize privileges
- minimize capabilities
- restrict resources
- restrict filesystem access
- restrict networking where appropriate
- avoid exposing host resources
- avoid mounting sensitive host paths

Never mount the host Docker socket into an untrusted workload unless the architecture explicitly requires it and the security implications have been reviewed.

---

## 18. Networking Rules

Do not expose internal services unnecessarily.

Prefer:

```text
Internet
   ↓
Reverse Proxy
   ↓
Application
```

rather than exposing every application/container port directly.

Internal control-plane services should not automatically be reachable from user workloads.

---

## 19. Observability

When building infrastructure, design for diagnosis.

Important operations should eventually make it possible to determine:

- what operation occurred
- when it occurred
- which resource it affected
- whether it succeeded
- why it failed

Observability should be added incrementally rather than through premature infrastructure.

---

## 20. Code Quality

Prefer:

- clear names
- small functions
- explicit control flow
- understandable abstractions
- minimal magic
- appropriate error handling
- consistent formatting
- **thorough teaching comments** on infrastructure, Go idioms, and package boundaries (see AGENTS.md section 7)

Avoid:

- premature abstraction
- giant files
- unnecessary design patterns
- clever code that is difficult to debug
- uncommented control-plane, Docker, network, or concurrency code
- comments that only restate `return err`

### Comments (learning project)

Forge is built so the developer can later explain every subsystem. Source comments are part of that teaching surface, not optional polish.

Every new or edited Go file must include:

- a package comment stating what the package owns and what it must never do (especially: never execute user code)
- comments on exported types, functions, and interfaces that describe the contract, not just the name
- inline comments on concurrency (`go`, channels, `select`), `context` timeouts, `defer` cleanup, network protocols, and database driver registration
- a short comment on each test describing the behavior it locks in

Do not leave control-plane, Docker, Redis, PostgreSQL, or HTTP code uncommented to keep the file short. Do not log or comment secret values.

The full commenting contract lives in `AGENTS.md` section 7.

---

## 21. Documentation

When an implementation introduces an important architectural decision:

- Document the decision.
- Explain why it exists.
- Explain relevant trade-offs.

Do not allow important architecture to exist only inside source code or agent context.

---

## 22. AI Agent Behavior

AI agents working on Forge must:

- Read the relevant source-of-truth documents.
- Understand the current phase.
- Plan before making significant changes.
- Make small changes.
- Verify their work.
- Report failures honestly.
- Never invent missing architecture.
- Never treat `PRODUCT_VISION.md` as a phase authorization.
- Never silently modify architectural decisions.
- Never implement future phases without authorization.
- Ask for clarification when requirements conflict.

Agents must not treat generated code as automatically correct.

---

## 23. MCP and Tooling Rules

Prefer:

```text
Native Cursor feature
        ↓
Rule / Skill / Command / Hook
        ↓
MCP only when justified
```

Do not install tools simply because they exist.

Every MCP should have:

- a clear purpose
- minimum required permissions
- known security implications
- a reason it cannot be replaced by a simpler mechanism

Avoid MCP tool sprawl.

---

## 24. Review Process

Significant changes should follow:

```text
Implementation
     ↓
Verification
     ↓
Code Review
     ↓
Security Review
     ↓
Documentation
```

A reviewer should be capable of identifying problems in the implementation rather than simply confirming that files changed.

---

## 25. Definition of Done

A task is complete only when:

- requested functionality is implemented
- relevant tests/checks pass
- architecture remains consistent
- security implications are considered
- no secrets were introduced
- unnecessary files/dependencies were not added
- documentation is updated when necessary
- the final diff is understood

---

## 26. Stop Conditions

STOP and request review if:

- requirements conflict
- architecture is unclear
- a security boundary must be bypassed
- production infrastructure may be affected
- destructive operations are required
- credentials are required unexpectedly
- a future-phase feature appears necessary to continue
- `PRODUCT_VISION.md` is being used as a substitute for `START PHASE N`
- verification cannot be completed
- the implementation requires a major architectural change

Never hide uncertainty.

---

## 27. Secure by default

New endpoints, binds, and containers default to **least privilege and loopback** until a named increment changes publication.

Validate all untrusted input at the boundary (HTTP, git URLs, paths, env keys, health paths, Host slugs). Fail closed.

## 28. AWS cost awareness

Do not add always-on cloud-shaped dependencies “for later.” When cloud work is authorized, apply `ARCHITECTURE.md` section 11 (Free Tier, no NAT-as-default, cleanup). Cost surprise is an architecture defect.

## 29. Avoid unnecessary vendor lock-in

Prefer interfaces (store, queue, runtime, proxy) over embedding a single vendor’s API in handlers. Using Docker and Caddy locally is a current choice, not a requirement to call only one cloud API from the control plane.

## 30. Failure recovery

Control-plane code should persist explicit failure states. Do not leave LIVE rows that do not match runtime. Rollback is an API against a known image, not an undocumented `docker` ritual.

## 31. Final Rule

Forge is being built to understand and engineer infrastructure, not merely to produce a working demo.

Every major implementation should leave the system:

- understandable
- testable
- secure
- observable
- maintainable

Do not optimize for the shortest possible path to generated code.

Optimize for building a system that we can explain.
