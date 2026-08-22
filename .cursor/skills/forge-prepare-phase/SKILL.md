---
name: forge-prepare-phase
description: Pre-implementation gate for a named Forge phase. Use when the user invokes /prepare-phase or asks to prepare a phase without writing application code.
disable-model-invocation: true
---

# Prepare a Forge phase

This skill plans. It does not implement Forge.

## Preconditions

- The developer named a phase (for example `START PHASE 0` or "prepare phase 0").
- If they have not named a phase, ask which phase and stop.
- Configuring Cursor is not a phase start.

## Steps

1. Read `AGENTS.md`, `docs/ARCHITECTURE.md`, `docs/ROADMAP.md`, `docs/DEVELOPMENT_RULES.md`.
2. If any of those docs are empty, report that and **do not invent** the missing architecture. Stop after the report unless the developer explicitly wants a plan that waits on those docs.
3. Inspect the repository as it is. Do not assume files exist.
4. Explain what the named phase would build and why, citing the docs when they have content.
5. List files that would be created or modified.
6. List risks (security, data loss, breaking changes).
7. Stop. Do not write application code, schemas, Compose files, APIs, or workers.

Wait for explicit implementation authorization for that phase.
