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

---

## 2. Phase Discipline

Only work on the current authorized phase.

If the current phase is Phase 0, do not implement Phase 1+ functionality unless explicitly authorized.

Do not interpret TODOs, roadmap entries, comments, future architecture, documentation, or user ideas as authorization to implement future functionality.

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

The following documents are authoritative:

- `AGENTS.md`
- `docs/ARCHITECTURE.md`
- `docs/ROADMAP.md`
- `docs/DEVELOPMENT_RULES.md`

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

Production secret management is a later concern, but secret hygiene applies from the first commit.

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

---

## 12. Testing and Verification

Never claim "it works" without verification.

Verification should match the change.

**Go** — run relevant tests, formatting, and static analysis/build checks.

**TypeScript / Next.js** — run relevant type checking, linting, tests, and build verification.

**Docker** — verify image build, container startup, expected ports, and health checks.

**Infrastructure** — verify the smallest meaningful behavior before proceeding.

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
- verification cannot be completed
- the implementation requires a major architectural change

Never hide uncertainty.

---

## 27. Final Rule

Forge is being built to understand and engineer infrastructure, not merely to produce a working demo.

Every major implementation should leave the system:

- understandable
- testable
- secure
- observable
- maintainable

Do not optimize for the shortest possible path to generated code.

Optimize for building a system that we can explain.
