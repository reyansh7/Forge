---
name: forge-code-review
description: Independent review of Forge diffs for correctness, phase scope, tests, and engineering discipline. Use when reviewing pull requests, inspecting a diff, or when the user asks for a code review.
---

# Forge code review

Review as an independent reader. Do not implement fixes unless asked.

## Scope

1. Identify the stated goal and the current phase (`docs/ROADMAP.md`). If the roadmap is empty, say so and review only against `AGENTS.md` plus the request.
2. Inspect the actual diff. Do not review files that did not change unless they are required context.

## Checklist

- Correctness: does the change do what was claimed?
- Phase discipline: any future-phase work sneak in?
- Smallest logical change: any unrelated rewrite?
- Tests: are they present, and do they test the behavior (not just exist)?
- Error handling: no `panic()` for expected failures; errors are explicit
- Dependencies: every new one justified
- Secrets and untrusted input: none committed; none executed on the host
- Docs: architecture/roadmap updated only when behavior actually changed
- Comments: new infrastructure and Go idioms are taught in-file (see AGENTS.md §7)

## Output

- Critical: must fix before merge
- Suggestion: worth changing
- Note: optional

State what you did not run (tests, build, linters). Do not claim the change works without verification.
