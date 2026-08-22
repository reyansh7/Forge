---
name: forge-architecture
description: Reads Forge source-of-truth documents and refuses invented architecture. Use when planning, explaining architecture, resolving doc/code mismatches, or when ARCHITECTURE.md, ROADMAP.md, or DEVELOPMENT_RULES.md are involved.
---

# Forge architecture

## Source of truth

Read, in order, only as needed:

1. `AGENTS.md`
2. `docs/ARCHITECTURE.md`
3. `docs/ROADMAP.md`
4. `docs/DEVELOPMENT_RULES.md`

Do not use `docs/CURSOR_ENVIRONMENT.md` as architecture. That file describes the Cursor environment only.

## Empty documents

If `ARCHITECTURE.md`, `ROADMAP.md`, or `DEVELOPMENT_RULES.md` are empty or missing:

- Report which file is empty or missing
- Do not invent architecture, phases, schemas, or APIs
- Do not fill those files unless the developer explicitly asks to write them
- Stop instead of guessing

## Conflicts

If code disagrees with the docs, decide which is true before changing anything:

1. Implementation is wrong (fix the code)
2. Documentation is stale (fix the doc)
3. Architecture evolved (fix the doc and say why)

Never paper over a mismatch.

## Phase boundary

Work only on the phase named in `START PHASE N`. Cursor environment setup is not Phase 0. If no phase has been started, limit work to understanding, planning, review, or fixes inside the current request.
