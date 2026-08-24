# Cursor environment

This document describes the Cursor engineering environment for Forge.

It is **not** architecture, roadmap, or development-rules source of truth. Those files are:

- `AGENTS.md`
- `docs/ARCHITECTURE.md`
- `docs/ROADMAP.md`
- `docs/DEVELOPMENT_RULES.md`

If those three `docs/` files are empty, do not invent their contents.

Implementation phases are gated by `START PHASE N` in chat and by `docs/ROADMAP.md`. This file only describes Cursor tooling.

## Layout

```text
AGENTS.md
docs/CURSOR_ENVIRONMENT.md   (this file)
.cursor/rules/               persistent project behavior
.cursor/skills/              reusable workflows
.cursor/agents/              isolated roles
.cursor/commands/            user-invoked entry points
.cursor/hooks.json           safety automation
.cursor/hooks/               Node hook scripts
.cursor/mcp.json             project MCP (empty on purpose)
```

Load detail only when relevant: `AGENTS.md` → rules → skill → references → files.

Code comments for Forge source live in `AGENTS.md` section 7 and `docs/DEVELOPMENT_RULES.md` (Code Quality). Teaching comments on Go idioms and infrastructure are required; this file does not override that.

## Git

This directory was **not** a git repository when the environment was created. `git init` was intentionally **not** run.

Proposed later action (needs explicit authorization): `git init`, then add a GitHub remote. Do not force-push, rewrite history, or put tokens in git.

`.gitignore` and `.cursorignore` already ignore `.env`, keys, `node_modules/`, and `.obsidian/`.

## MCP and plugins

### Principle

Maximum useful capability with minimum extra tools, permissions, and context. Do not build an MCP zoo. Project `.cursor/mcp.json` has `"mcpServers": {}`.

**The agent must not** install, enable, disable, or uninstall user-level Cursor plugins or MCP servers, and must not edit `~/.cursor/mcp.json`.

### Already available at user scope (untouched)

These exist in the operator’s Cursor session. This project does not add or remove them.

| Item | Forge use |
|------|-----------|
| Context7 | In policy: current library/framework docs |
| Playwright | In policy later: isolated UI checks. Never `browser_run_code_unsafe` |
| GitLab | Out of policy unless explicitly requested |
| Sentry | Out of policy; do not authenticate |
| Neon Postgres | Out of policy; do not authenticate |
| Azure, AWS, Render, Vercel | Out of policy |
| Figma, Canva, Hugging Face, Apify, Roboflow, Context.dev | Out of policy |
| Obsidian MCP in `~/.cursor/mcp.json` | Personal; do not copy into this repo |

Optional (operator only, in **Customize**): disable unused user-scope servers while working on Forge. The agent will not do this.

If `~/.cursor/mcp.json` stores a Bearer token in plaintext, rotate that token and prefer `${env:...}` interpolation. Do not paste tokens into chat or into this repository.

### Project-level configuration

| Item | Status |
|------|--------|
| `.cursor/mcp.json` | Empty `mcpServers` |
| Context7 / Playwright | Not duplicated here. Allowed **if already present** |
| GitHub MCP | Not enabled |
| Cloudflare MCP | Not enabled |

### Future consideration (document only)

Enable only after an explicit request and a pass of the evaluation matrix below.

| Integration | Reconsider when |
|-------------|-----------------|
| Official GitHub MCP | A GitHub remote exists. Use OAuth in the Cursor UI. No PAT in git. Start read-only. |
| Sentry | Forge has a running product and an observability phase |
| Self-hosted PostgreSQL tooling | A local/dev Postgres exists in an explicit phase. Tight scope. Not Neon-as-Forge-DB. Not production. |
| Playwright authenticated Forge sessions | A Forge UI exists. Isolated browser profile only — never the operator’s daily profile |
| Cloudflare MCP / Wrangler | Only if Forge later operates a Cloudflare **account** for a named reason. Research below is not that decision. |
| AWS / Azure / Render | Only if a future phase names that provider |

**GitHub MCP later recipe** (do not put a token in the file):

```json
{
  "mcpServers": {
    "github": {
      "url": "https://api.githubcopilot.com/mcp/",
      "headers": {
        "X-MCP-Toolsets": "issues,repos,pull_requests,actions",
        "X-MCP-Readonly": "true"
      }
    }
  }
}
```

Authenticate via Cursor OAuth. Broaden writes only when needed.

### Rejected for this setup

| Item | Reason |
|------|--------|
| Superpowers | Generic workflows fight Forge phase gates; extra context |
| Compound Engineering | Large agent/skill/MCP bundle |
| Duplicate Context7 or Playwright in project MCP | Already at user scope |
| GitHub MCP now | No repository or remote |
| Cloudflare MCP now | Research only; would require account auth |
| Neon / unrestricted DB MCP | Production-shaped access; too early |
| A cloud vendor as “the Forge target” | Forge is a self-hosted PaaS; architecture docs are empty |

### Evaluation matrix (before adding anything)

| Criterion | Question |
|-----------|----------|
| Value | Does Forge genuinely benefit now? |
| Reliability | Maintained and trustworthy? |
| Security | What access does it need? |
| Duplication | Does Cursor already do this? |
| Context cost | Extra tools in every session? |
| Complexity | Harder to understand? |
| Reversibility | Easy to remove? |

## Rules

Always-on, under `.cursor/rules/`:

- `source-of-truth.mdc` — architecture files win; empty docs mean stop; no silent phase start
- `mcp-policy.mdc` — in-policy vs out-of-policy **usage**; never uninstall user plugins
- `git-production-safety.mdc` — no unauthorized `git init`, no history rewrite, no secrets, no production destroy

File-type rules (Go, TypeScript, Docker) wait until those trees exist.

## Skills

| Skill | When |
|-------|------|
| `forge-architecture` | Planning or explaining architecture |
| `forge-security-review` | Security review |
| `forge-code-review` | Diff / PR review |
| `forge-verify` | Explicit `/verify` |
| `forge-prepare-phase` | Explicit `/prepare-phase` |

`forge-verify` and `forge-prepare-phase` are human-invoked (`disable-model-invocation`).

Deferred: backend, frontend, database, Docker, deploy, generic debugging skills.

## Subagents

| Agent | Role |
|-------|------|
| `architect` | Read-only planning against source of truth |
| `security-engineer` | Read-only security review |
| `reviewer` | Independent verification; may run tests; must not edit |

The main agent is the implementer. Backend / frontend / DevOps subagents are deferred.

Cursor custom subagents inherit parent tools. Least privilege here is `readonly: true` plus the prompt.

## Commands

Thin prompts that call the skills/subagents:

- `/prepare-phase`
- `/explain-architecture`
- `/review`
- `/security-review`
- `/verify`

## Hooks

| Hook | Script | Behavior |
|------|--------|----------|
| `beforeShellExecution` | `.cursor/hooks/protect-shell.js` | Deny credential dumps; ask for force-push, hard reset, `git init`, recursive delete, cloud/DB destroy |
| `afterFileEdit` | `.cursor/hooks/scan-secrets.js` | Warn on private keys and common token shapes |

`failClosed` is `false` so a hook crash does not freeze all shells. There is no `stop` hook (loop risk; no test suite yet). No format-on-edit.

Windows: hooks run through `node` (v22+ expected).

## Security boundaries

The Cursor agent is a privileged development tool.

It **may**: read this repo, edit project Cursor config, use Context7 and (later) isolated Playwright if those user-scope servers exist.

It **must not**:

- Change user-level MCP/plugin config
- Authenticate Sentry, Neon, GitHub MCP, Cloudflare, or cloud accounts
- `git init` or add remotes without authorization
- Commit or print secrets
- Execute user-controlled input on the host
- Destroy production infrastructure without an explicit named operation
- Call Playwright `browser_run_code_unsafe`

Prefer human approval for deletes, force-push, production changes, and secret-bearing files.

## How to add an integration later

1. Score it on the evaluation matrix.
2. Prefer a native Cursor feature (rule, skill, command, hook) over a new MCP.
3. If MCP is justified: add it to **project** `.cursor/mcp.json` only, with the least toolsets and no secrets in git.
4. Update this file’s four-way classification.
5. Do not copy credentials from `~/.cursor/mcp.json`.

## Cloudflare research (ideas only)

Public sources: Workers [security model](https://developers.cloudflare.com/workers/reference/security-model/), [workerd](https://github.com/cloudflare/workerd), [workers-sdk / Wrangler](https://github.com/cloudflare/workers-sdk), [Cloudflare OS](https://github.com/cloudflare/cloudflare-os), [Agents](https://developers.cloudflare.com/agents/), [Sandbox SDK](https://developers.cloudflare.com/sandbox/), [mcp-server-cloudflare](https://github.com/cloudflare/mcp-server-cloudflare).

This is **not** a Forge architecture decision. Do not copy Cloudflare into Forge, install Wrangler/workerd as Forge infra, authenticate Cloudflare, create Cloudflare resources, or add Cloudflare MCP.

Ideas relevant to “user code is untrusted code”:

- Isolation is a sandbox **and** a narrow API. No filesystem API means no filesystem access.
- Defense in depth: in-process isolates, process isolation for higher-risk features, outer namespace/`seccomp` jail with no direct disk/network syscalls.
- Mediated I/O: untrusted code talks to a supervisor/proxy; outbound is not ambient internet; identity is attached so abuse is attributable.
- Capability introduction over ambient authority (Cloudflare OS Gatekeepers: wrap OAuth, narrow the resource, log, human-approve side effects).
- Trust tiers: do not colocate low-trust and high-trust tenants.
- Match sandbox to workload: V8 isolates fit JS/Wasm with a tiny API; a real Linux userland (build/run user apps) needs containers. Forge’s `detect → build → provision → deploy` loop is closer to containers than to Workers isolates.
- Load guest code into a fresh sandbox, never `exec` it on the control-plane host.
- Human-in-the-loop can simulate side effects so the agent continues, then batch-approve — an idea for later dangerous ops, not something to build now.
- MCP context cost: fewer, broader tools (search + execute) beat hundreds of tools. That is a lesson for a future Forge MCP, not a reason to install Cloudflare MCP now.

**Non-decision:** Cloudflare is not selected as a Forge runtime, deploy target, or plugin.
