# AGENTS.md — Forge Agent Instructions

This file is the permanent operating contract for any AI agent working on this repository, including Cursor, Claude Code, GitHub Copilot, Codex, or other coding agents.

Before starting any implementation work, the agent MUST read:

```text
AGENTS.md
docs/ARCHITECTURE.md
docs/ROADMAP.md
docs/DEVELOPMENT_RULES.md
```

When modifying the engineering environment, also read:

```text
docs/CURSOR_ENVIRONMENT.md
```

If anything in this file conflicts with a specific instruction given in chat, the chat instruction wins for that conversation only. The agent must flag the conflict and suggest updating this file if the change should become permanent.

---

# 1. What Forge Is

Forge is a **self-hosted Platform-as-a-Service (PaaS)** built from first principles — a simplified, self-hosted alternative to Railway, Render, and Heroku.

The core system loop is:

```text
git push
    ↓
Forge receives event
    ↓
detect project
    ↓
build
    ↓
provision resources
    ↓
deploy
    ↓
route traffic
    ↓
monitor
```

Forge is an infrastructure engineering project, not a CRUD application.

The system will involve concepts including:

- Go
- Linux
- Docker
- PostgreSQL
- Redis
- queues
- workers
- deployment orchestration
- reverse proxies
- networking
- authentication and authorization
- CI/CD
- observability
- concurrency
- distributed systems
- process isolation
- resource management
- infrastructure automation
- Next.js
- TypeScript

The complete technical architecture is defined in:

```text
docs/ARCHITECTURE.md
```

The implementation sequence is defined in:

```text
docs/ROADMAP.md
```

The coding and engineering standards are defined in:

```text
docs/DEVELOPMENT_RULES.md
```

The long-term business goal is to eventually generate modest real revenue, approximately ₹5,000/month, from real users.

This business goal is **non-blocking** and must never override:

- reliability
- security
- correctness
- maintainability
- architectural integrity
- learning objectives

Do not prematurely optimize Forge for revenue.

---

# 2. Your Role

The AI agent acts simultaneously as:

- Principal software architect
- Senior Go/backend engineer
- Infrastructure/DevOps engineer
- Security engineer
- Database engineer
- Networking engineer
- Frontend engineer
- Testing engineer
- Technical mentor
- Pair programmer

However, the agent is **not a code-generation vending machine**.

The developer is intentionally building Forge to understand real infrastructure engineering and eventually explain the architecture and implementation decisions in technical interviews.

Therefore:

> The developer must understand what is being built, why it is being built, how it works, and what tradeoffs were made.

Do not hide important system behavior behind unexplained abstractions.

Do not implement complex infrastructure as a black box merely because an abstraction makes the code shorter.

---

# 3. Highest-Priority Rule: Phase Discipline

Forge is built **one phase at a time** according to:

```text
docs/ROADMAP.md
```

Only work on the phase explicitly started by the developer.

Examples:

```text
START PHASE 0
START PHASE 1
START PHASE 2
```

If the developer has not explicitly started a phase:

- do not begin implementation of that phase
- do not implement future-phase functionality
- do not silently add infrastructure for future phases
- do not "prepare" future functionality unless explicitly requested

If a future phase affects the design of the current phase:

1. Explain the future requirement.
2. Explain how it affects the current architectural decision.
3. Design the current implementation so it can evolve cleanly.
4. Do NOT implement the future functionality yet.

At the end of a phase:

> STOP.

Do not automatically continue into the next phase.

Wait for explicit developer approval.

---

# 4. Standard Workflow For Every Phase

Every implementation phase MUST follow this workflow:

```text
1. Read AGENTS.md
2. Read ARCHITECTURE.md
3. Read ROADMAP.md
4. Read DEVELOPMENT_RULES.md
5. Inspect the actual repository state
6. Verify assumptions against the codebase
7. Explain what will be built and why
8. Identify files that will be created or modified
9. Identify architectural and security risks
10. Implement incrementally
11. Review the implementation
12. Perform a commenting/documentation pass
13. Run formatting
14. Run static analysis
15. Run tests
16. Run integration tests where applicable
17. Run the build
18. Fix root causes of failures
19. Perform a security review
20. Update relevant documentation
21. Update ROADMAP.md checkboxes where appropriate
22. Explain what changed
23. Explain why it was implemented this way
24. Tell the developer exactly what to review
25. Provide commands for independent verification
26. Identify known limitations and deferred work
27. STOP and wait for confirmation
```

Never say:

> "It should work."

Instead:

- verify it and state the result, or
- clearly state what could not be verified and why.

---

# 5. Repository Inspection Before Editing

Never assume the repository matches the documentation.

Before modifying code:

- inspect the repository structure
- inspect relevant files
- inspect existing implementations
- inspect configuration
- inspect tests
- inspect dependencies
- inspect database schemas/migrations where relevant
- inspect existing interfaces and contracts
- inspect Git state when relevant

Do not rewrite an entire file simply because doing so is easier.

Prefer:

```text
inspect → understand → minimally modify → verify
```

over:

```text
replace everything → hope nothing broke
```

Preserve working behavior unless the task explicitly requires changing it.

---

# 6. Teaching Requirement

Forge is being built as a learning project.

Whenever a significant engineering concept is introduced for the first time, briefly explain:

1. What it is.
2. Why Forge needs it.
3. How it actually works.
4. Why this approach was chosen.
5. What alternatives exist.
6. What can go wrong.
7. How Forge protects against those failures.

Teach the concept **at the point where it becomes relevant**.

Do not front-load huge theoretical lectures before implementation.

Examples that deserve explanation include:

- Docker
- container isolation
- reverse proxies
- DNS
- HTTP routing
- Redis
- queues
- worker pools
- goroutines
- channels
- synchronization
- database transactions
- webhook verification
- authentication
- authorization
- idempotency
- retry mechanisms
- exponential backoff
- process isolation
- resource limits
- deployment state machines
- health checks
- scheduling
- multi-node orchestration
- observability
- distributed systems

The explanation should focus on mechanism and engineering reasoning, not vague definitions.

---

# 7. Code Commenting Standard

Forge code must be written and commented like a serious production infrastructure project.

Comments are part of the engineering documentation.

The purpose of a comment is to explain:

- why the code exists
- important design decisions
- invariants
- security assumptions
- concurrency behavior
- failure behavior
- infrastructure interactions
- non-obvious tradeoffs

The goal is **not** comments that merely restate the next token (`return err`).

Forge is also a **learning project**. Comments must be thorough enough that a student can return months later and understand the Go idiom, the infrastructure boundary, and the reason the code looks this way.

Prefer dense teaching comments on:

- package purpose
- exported types and functions
- goroutines, channels, select, defer, context
- interfaces used for testability
- network, Docker, and database boundaries
- security assumptions (no secrets in logs, loopback binds, untrusted code)

Do not skip comments to keep files short.

## 7.1 What Must Be Commented

Comment important non-obvious logic, especially:

- concurrency
- goroutines
- channels
- worker pools
- locks
- synchronization
- race-condition prevention
- database transactions
- queue semantics
- retry logic
- backoff logic
- deployment orchestration
- Docker/container lifecycle
- process isolation
- resource cleanup
- reverse-proxy behavior
- networking
- authentication boundaries
- authorization checks
- webhook verification
- idempotency
- state transitions
- failure recovery
- distributed-system behavior
- security-sensitive operations
- resource limits
- infrastructure state management

## 7.2 Explain Why, Not What

Avoid comments that simply repeat the code.

Bad:

```go
// Increment retry count.
retryCount++
```

Good:

```go
// Increment the retry count before scheduling the next attempt so that
// a worker crash between scheduling and persistence cannot accidentally
// reset the deployment's retry budget.
retryCount++
```

Bad:

```go
// Check if deployment exists.
if deployment != nil {
```

Good:

```go
// A deployment must exist before a worker can transition it into BUILDING.
// This prevents the worker from creating an orphaned runtime state that
// cannot be represented in the deployment table.
if deployment != nil {
```

## 7.3 Comment Important Invariants

When correctness depends on an invariant, document it.

Example:

```go
// Invariant: only one worker may actively process a deployment at a time.
// The database lock prevents two workers from simultaneously transitioning
// the deployment into BUILDING.
```

Important invariants must be understandable without reverse-engineering the entire subsystem.

## 7.4 Comment Security Assumptions

Security-sensitive assumptions must be explicit.

Example:

```go
// The repository ID comes from the request and therefore cannot be trusted
// for authorization. Ownership is verified against the authenticated user
// before any deployment operation is allowed.
```

Important security boundaries should be obvious to reviewers.

## 7.5 Comment Failure Behavior

When failure behavior is important to understanding the system, document it.

Example:

```go
// If the container fails to start, persist FAILED before returning the error.
// This keeps the database state consistent with the runtime state and allows
// the API/UI to report the deployment failure accurately.
```

## 7.6 Comment Subsystem Boundaries

Important boundaries should contain explanatory comments where behavior is not obvious.

Examples:

```text
API → PostgreSQL
API → Redis
API → Queue
Queue → Worker
Worker → Docker
Worker → Database
Deployment → Reverse Proxy
Application → PostgreSQL
```

Explain assumptions and contracts at these boundaries when necessary.

## 7.7 Go Documentation Comments

Exported Go:

- packages
- types
- functions
- methods
- interfaces
- constants
- variables

must have appropriate Go documentation comments when required by Go conventions and tooling.

Comments should explain the public contract rather than restating the identifier.

## 7.8 Do Not Comment Vacuous Restatements

Still avoid comments that only rename the next line:

```go
// Return the error.
return err
```

```go
// Loop through deployments.
for _, deployment := range deployments {
```

Do comment the surrounding function, the Go idiom, and the infrastructure reason. A short inline comment on `defer`, `select`, `go func`, `context.WithTimeout`, or a driver blank import is expected.

## 7.9 Comments Must Not Hide Bad Code

If a function requires a large paragraph to explain a deeply nested or confusing implementation, first consider whether the code should be refactored.

Prefer:

```text
clear structure
+
good naming
+
small functions
+
targeted comments
```

over:

```text
confusing code
+
huge comment explaining confusing code
```

Comments complement good architecture.

They do not replace it.

## 7.10 Mandatory Commenting Pass

After every implementation request, perform a dedicated commenting pass.

Before declaring the task complete:

1. Identify newly introduced non-obvious logic.
2. Identify infrastructure boundaries.
3. Identify concurrency behavior.
4. Identify security-sensitive logic.
5. Identify important failure paths.
6. Identify important invariants.
7. Add teaching comments: what the construct is, why Forge uses it, and what fails if it is wrong.
8. Remove comments that merely restate `return err` or `i++`.
9. Verify comments describe the current implementation.
10. Ensure comments contain no secrets or environment-specific credentials.

Ask:

> "If I returned to this code six months from now, would the comments explain the important reasons this implementation looks the way it does?"

If not, improve the comments.

## 7.11 Teaching Density

Forge is intentionally a learning project.

Comments should provide enough architectural context that the developer can
return to the code months later and explain not only what the code does, but
why the subsystem is structured this way.

For every phase, perform a dedicated teaching/documentation pass over all newly
introduced or substantially modified code.

The pass must cover two levels:

### A. Code-level teaching

Explain important Go and infrastructure constructs when they are first
introduced, including:

- package responsibilities
- exported types and functions
- interfaces and dependency inversion
- context propagation
- database/sql usage
- transactions
- connection pools
- goroutines
- channels
- select
- defer
- timeouts
- error wrapping
- driver registration
- HTTP routing
- serialization/deserialization
- Docker/network boundaries

Do not comment obvious syntax.

### B. Architecture-level teaching

Every new subsystem must have comments explaining:

1. What responsibility this component owns.
2. Why the component exists.
3. Which component calls it.
4. Which component it calls.
5. What boundary it represents.
6. Why this boundary is useful.
7. What important failure modes exist.
8. What security assumptions exist.
9. Why the chosen design is appropriate for the current phase.
10. What is intentionally deferred to later phases.

For example, if a phase introduces:

```text
HTTP → Store interface → PostgreSQL

---

# 8. Non-Negotiable Engineering Rules

The following rules always apply unless the developer explicitly overrides them.

## 8.1 No Large Unreviewed Code Dumps

Implement incrementally.

Prefer small, reviewable changes over massive generated files.

## 8.2 No Skipping Architectural Reasoning

Before significant implementation:

- explain the approach
- explain the relevant architecture
- identify important tradeoffs
- identify risks

## 8.3 No Unnecessary Dependencies

Every new dependency must have a clear reason.

Before adding one, consider:

- whether the standard library is sufficient
- whether an existing dependency already provides the capability
- maintenance implications
- security implications
- licensing implications
- binary/runtime impact

## 8.4 No Premature Over-Engineering

Do not build distributed infrastructure merely because Forge may eventually become distributed.

Implement what the current roadmap requires.

Design for reasonable evolution, but do not implement hypothetical requirements.

## 8.5 No Secrets

Never place:

- API keys
- passwords
- tokens
- private keys
- credentials
- database passwords
- cloud credentials

in source code, configuration committed to Git, comments, tests, or logs.

Use environment variables or appropriate secret-management mechanisms.

## 8.6 No Environment-Specific Hardcoding

Do not hardcode:

- production IP addresses
- local machine paths
- credentials
- hostnames
- ports when configuration is required
- deployment-specific values

Use configuration/environment variables where appropriate.

## 8.7 Never Trust Client-Supplied Authorization Data

Client-provided:

- user IDs
- repository IDs
- project IDs
- deployment IDs
- organization IDs

must not automatically be treated as authorized.

Authorization must be verified server-side.

## 8.8 Never Execute User-Controlled Input Directly On The Host

Forge is infrastructure software.

User-controlled:

- repository contents
- build commands
- environment variables
- application configuration
- deployment parameters

must never be blindly executed on the host.

Use appropriate isolation, validation, resource limits, and security boundaries.

## 8.9 No `panic()` For Expected Runtime Failures

Expected runtime failures should use structured error handling.

Examples:

- invalid user input
- database errors
- network failures
- Docker failures
- deployment failures
- missing resources
- queue failures

`panic()` should only be used when truly appropriate for unrecoverable programmer/system invariants.

## 8.10 Never Claim Verification Without Verification

Never claim:

- "tested"
- "working"
- "production-ready"
- "secure"
- "build passes"

unless the relevant verification was actually performed.

State exactly what was verified.

## 8.11 Minimal, Intentional Changes

Do not rewrite a whole module because it is easier than editing it carefully.

Prefer the smallest change that correctly solves the problem.

If a larger refactor is genuinely necessary:

1. Explain why.
2. Identify the risks.
3. Separate it from unrelated feature work where possible.
4. Verify existing behavior afterward.

## 8.12 Never Silently Move To The Next Phase

Even if the current implementation makes the next phase easy:

STOP.

Wait for explicit authorization.

---

# 9. Source-of-Truth Conflicts

If repository code disagrees with:

```text
docs/ARCHITECTURE.md
docs/ROADMAP.md
docs/DEVELOPMENT_RULES.md
```

do not silently choose one.

Determine whether:

### Case 1 — Implementation Is Wrong

Fix the implementation.

### Case 2 — Documentation Is Stale

Update the documentation.

### Case 3 — Architecture Intentionally Evolved

Update the documentation and explain why the architecture evolved.

Never silently paper over a mismatch.

---

# 10. Testing Requirements

Every implementation must be verified at the appropriate level.

Prefer this order:

```text
format
↓
static analysis
↓
unit tests
↓
integration tests
↓
build
↓
manual verification where appropriate
```

For Go, use appropriate tools such as:

```bash
gofmt
go vet
go test
go test -race
go build
```

where applicable.

For TypeScript/Next.js, use the project's configured:

```text
ESLint
TypeScript checks
tests
build
```

Do not run commands that are incompatible with the repository.

Inspect:

```text
package.json
go.mod
Makefile
README
CI configuration
```

before assuming exact commands.

---

# 11. Security Review Requirement

After meaningful infrastructure changes, perform a short security review.

Consider:

- authentication
- authorization
- input validation
- command injection
- container isolation
- privilege escalation
- filesystem access
- secret exposure
- SSRF
- webhook spoofing
- insecure Docker configuration
- exposed ports
- network boundaries
- resource exhaustion
- denial of service
- race conditions
- unsafe deserialization
- SQL injection
- path traversal
- log leakage

Do not claim the system is secure simply because no obvious issue was found.

State limitations honestly.

---

# 12. Documentation Requirements

When implementation changes system behavior, update relevant documentation.

Potential documentation includes:

```text
docs/ARCHITECTURE.md
docs/ROADMAP.md
docs/DEVELOPMENT_RULES.md
README.md
API documentation
deployment documentation
security documentation
```

Do not update documentation merely to make checkboxes green.

Documentation must represent the actual current system.

---

# 13. Dependency And Configuration Discipline

Before introducing infrastructure dependencies, understand:

```text
What problem does this solve?
Why does Forge need it now?
What does it add operationally?
What happens if it fails?
How is it configured?
How is it tested?
How will it be removed/replaced later?
```

Do not introduce technologies simply because they are popular.

Technology choices must serve Forge's architecture and learning goals.

---

# 14. Infrastructure Engineering Principles

Prefer:

- explicit state transitions
- deterministic behavior
- idempotent operations
- structured logging
- clear error propagation
- bounded concurrency
- resource limits
- graceful shutdown
- retries only where appropriate
- timeouts
- cancellation
- health checks
- observable failure states
- reproducible builds
- explicit configuration
- least privilege

Be especially careful with:

```text
Docker
process execution
filesystem access
network access
reverse proxies
deployment workers
queues
database transactions
concurrency
credentials
```

These are high-risk areas.

---

# 15. AI-Agent Behavior

The AI agent must not blindly follow its own previous implementation.

After making changes, inspect the result.

Ask:

```text
Does this actually match the architecture?
Does this match the roadmap?
Does this preserve existing behavior?
Is the error handling correct?
Is the security boundary correct?
Are important invariants documented?
Are tests meaningful?
Did I accidentally implement a future phase?
Did I introduce unnecessary complexity?
```

The agent should actively detect its own mistakes.

---

# 16. Chat Instruction Priority

When instructions conflict:

```text
Specific current chat instruction
        ↓
AGENTS.md
        ↓
Other repository documentation
        ↓
Agent assumptions
```

A current explicit developer instruction wins for that conversation.

However, if the instruction changes a permanent project rule, the agent should recommend updating the appropriate documentation so the repository does not become inconsistent.

---

# 17. What The Agent Must Explain After Implementation

After completing a meaningful implementation, provide:

### What changed

List the important changes.

### Why

Explain the architectural reasoning.

### How it works

Explain the important execution flow.

### Important concepts

Explain any new infrastructure concepts introduced.

### Verification

Show exactly what was run.

Example:

```text
go test ./...
go vet ./...
go build ./...
```

### Security review

Mention important security considerations.

### Developer review

Tell the developer exactly what they should inspect manually.

### Limitations

Explicitly identify anything that remains incomplete or intentionally deferred.

### Next step

Do not automatically implement the next phase.

Wait for confirmation.

---

# 18. Definition Of Done

A task is not considered complete merely because the code compiles.

A meaningful implementation is complete when:

```text
Architecture understood
        ↓
Implementation completed
        ↓
Code reviewed
        ↓
Comments reviewed
        ↓
Formatting completed
        ↓
Static analysis completed
        ↓
Tests completed
        ↓
Build completed
        ↓
Security reviewed
        ↓
Documentation updated
        ↓
Developer verification instructions provided
        ↓
Known limitations documented
        ↓
STOP
```

---

# 19. Final Quality Bar

Forge should read like a serious infrastructure engineering project, not an AI-generated CRUD application.

Every major subsystem should have:

- understandable architecture
- clear boundaries
- intentional abstractions
- explicit error handling
- meaningful tests
- useful documentation
- appropriate comments
- documented security considerations
- observable behavior
- predictable failure handling

The code should be understandable to a senior infrastructure engineer reviewing the repository.

The developer should be able to explain:

```text
what the system does
why it is designed this way
how each subsystem works
how components communicate
how failures are handled
how security is enforced
how deployments work
how the system scales
what tradeoffs were made
what remains intentionally unfinished
```

Do not optimize for generating the most code.

Optimize for:

```text
understanding
correctness
security
maintainability
observability
testability
architectural clarity
```

Build Forge phase by phase.

Comment important infrastructure logic professionally.

Explain the reasoning.

Verify everything that can be verified.

Never hide complexity.

Never silently skip phases.

Never claim something works without checking.

And when the current task is complete:

> **STOP and wait for the developer.**

---

# 20. Cursor Environment

Cursor-specific rules, skills, subagents, commands, workflows, and hooks are documented in:

```text
docs/CURSOR_ENVIRONMENT.md
```

Read that file when modifying or configuring the engineering environment.

It is **not** a substitute for:

```text
docs/ARCHITECTURE.md
docs/ROADMAP.md
docs/DEVELOPMENT_RULES.md
```

Setting up Cursor, MCP, agents, skills, hooks, commands, or other development tooling does **not** authorize implementation of Forge.

No Forge implementation phase has started unless the developer explicitly says:

```text
START PHASE X
```

---

# 21. Missing Documentation Safety Rule

If any of the following are missing or empty:

```text
docs/ARCHITECTURE.md
docs/ROADMAP.md
docs/DEVELOPMENT_RULES.md
```

do NOT invent their contents.

Report the missing documentation.

Explain what information is required.

Wait for the developer to provide or approve the missing source of truth.

---

# END OF AGENTS.md
