---
name: forge-verify
description: Runs relevant verification and reports what actually passed. Use when the user invokes /verify, asks to confirm work, or asks whether something works.
disable-model-invocation: true
---

# Forge verify

Never claim something works without running the relevant check.

## Procedure

1. State what was claimed complete.
2. List the smallest verification that would confirm it (format, vet, tests, build, manual command).
3. Run only those checks. Do not invent a full CI suite that does not exist yet.
4. If a toolchain is missing (no `go.mod`, no tests, not a git repo), say so and skip that check.

## Report

- Ran: command + outcome
- Skipped: command + why
- Failed: command + root cause, not a symptom patch
- Not verified: anything you could not run

If this directory is not a git repository, do not run `git init`. Report that git verification is unavailable until the developer authorizes initialization.

Do not start Phase 0.
