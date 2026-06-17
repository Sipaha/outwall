# docs/INDEX.md — documentation map

> The project's bookshelf: what docs exist and where to find them. A development session
> starts here (after `AGENTS.md`/`CLAUDE.md`). When you add a new doc, link it here.
> This index is a **map, not a changelog** — phase-by-phase narrative lives in the ADRs + git.

## Orientation

outwall is a Go desktop daemon (Wails 3 + React UI) acting as an authenticating, filtering,
auditing egress gateway for AI agents calling external HTTP APIs. Sipaha project; no citeck.
Stage: alpha (pre-Plan-1).

**Where to start:**
- New session / what's being built now → [`roadmap/current-phase.md`](roadmap/current-phase.md)
- The architecture → [`architecture/overview.md`](architecture/overview.md)
- The design contract → [`superpowers/specs/2026-06-17-outwall-design.md`](superpowers/specs/2026-06-17-outwall-design.md)
- The active plan → [`superpowers/plans/2026-06-17-outwall-foundation.md`](superpowers/plans/2026-06-17-outwall-foundation.md)
- How we work autonomously → [`workflow/supervisor-mode.md`](workflow/supervisor-mode.md), [`workflow/agent-loop.md`](workflow/agent-loop.md)

## roadmap/ — what is being done, and next

- [`current-phase.md`](roadmap/current-phase.md) — the active phase + queued candidates (forward-only).

## architecture/ — what is built and how

- [`overview.md`](architecture/overview.md) — the overall system picture.
- `decisions/` — ADRs (one architectural decision each, `NNNN-slug.md`):
  - [`0001-stack-and-architecture.md`](architecture/decisions/0001-stack-and-architecture.md) — stack + two-plane gateway architecture.
  - [`0002-policy-and-approval.md`](architecture/decisions/0002-policy-and-approval.md) — rule precedence, blocking approval, rate limiter, OIDC-CC token cache.
  - [`0003-mcp-control-plane.md`](architecture/decisions/0003-mcp-control-plane.md) — MCP server, session=agent identity, SDK-free mcpsvc / thin adapter split, access-request intent log.
- `modules/` — per-package API docs: `secret`, `store`, `upstream`, `agent`, `authn`,
  `policy`, `approval`, `access`, `mcpsvc`, `mcp`, `proxy`, `daemon`, `client`, `cli`, `version`.

## workflow/ — how we work

- [`agent-loop.md`](workflow/agent-loop.md) — the six-phase routine for each task.
- [`supervisor-mode.md`](workflow/supervisor-mode.md) — autonomous phase development (supervisor only).
- [`doc-discipline.md`](workflow/doc-discipline.md) — what doc to write when; ADRs vs git.
- [`adr-template.md`](workflow/adr-template.md) — the ADR format.

## superpowers/ — specs & plans

- `specs/` — brainstormed design specs.
- `plans/` — bite-sized implementation plans.

## findings/ — non-obvious discoveries

- [`2026-06-vault-cli-needs-tty.md`](findings/2026-06-vault-cli-needs-tty.md) — `vault init`/`unlock` need a real TTY; add `--password-stdin` later.
