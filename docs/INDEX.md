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
  - [`0004-audit.md`](architecture/decisions/0004-audit.md) — capped streaming body capture, text/binary classification, masking, record-on-close, data-plane-only scope.
  - [`0005-control-api-sse.md`](architecture/decisions/0005-control-api-sse.md) — non-blocking event bus, SSE, UIListen loopback bind + X-Outwall-CSRF gate.
  - [`0006-web-ui-foundation.md`](architecture/decisions/0006-web-ui-foundation.md) — Vite→webdist go:embed, /api prefix + SPA serve, SSE CSRF exemption, dark theme tokens.
  - [`0007-desktop-wrapper.md`](architecture/decisions/0007-desktop-wrapper.md) — Wails 3 desktop wrapper runs the daemon in-process; server stays CGO-free via build tag.
  - [`0008-k8s-gateway-read.md`](architecture/decisions/0008-k8s-gateway-read.md) — k8s reverse-proxy; cluster=k8s-kind upstream; transport seam; (namespace,resource,verb) policy + namespace safety; token/client-cert/exec auth; local CA + kubeconfig.
  - [`0009-k8s-mutate-approval.md`](architecture/decisions/0009-k8s-mutate-approval.md) — mutating k8s verbs gated by the blocking approval queue; request body captured once (full forwarded, masked preview on the approval); tuple + masked body on approvals API/SSE.
  - [`0010-k8s-exec-attach.md`](architecture/decisions/0010-k8s-exec-attach.md) — exec/attach/cp/port-forward via ReverseProxy's native Upgrade; gated before the 101; ModifyResponse skipped on 101; metadata-only audit; stdlib-only (no websocket/client-go).
- `modules/` — per-package API docs: `secret`, `store`, `upstream`, `agent`, `authn`,
  `policy`, `approval`, `access`, `mcpsvc`, `mcp`, `audit`, `events`, `proxy`, `daemon`, `client`, `cli`, `version`, `k8s`, `tlsca`, plus `webui` (the `web/` app).

## workflow/ — how we work

- [`agent-loop.md`](workflow/agent-loop.md) — the six-phase routine for each task.
- [`supervisor-mode.md`](workflow/supervisor-mode.md) — autonomous phase development (supervisor only).
- [`doc-discipline.md`](workflow/doc-discipline.md) — what doc to write when; ADRs vs git.
- [`adr-template.md`](workflow/adr-template.md) — the ADR format.

## superpowers/ — specs & plans

- `specs/` — brainstormed design specs.
  - [`2026-06-17-outwall-design.md`](superpowers/specs/2026-06-17-outwall-design.md) — Phase 1 (HTTP egress gateway).
  - [`2026-06-18-outwall-k8s-gateway-design.md`](superpowers/specs/2026-06-18-outwall-k8s-gateway-design.md) — Kubernetes gateway (cluster targets, namespace-scoped policy, exec auth, kubeconfig, streaming/exec).
- `plans/` — bite-sized implementation plans.
  - Phase 1: the `2026-06-17-outwall-*` plans (foundation … wails-desktop), all shipped.
  - Phase 2 (k8s): [`…-k8s-k1-read.md`](superpowers/plans/2026-06-18-outwall-k8s-k1-read.md) (active),
    [`…-k8s-k2-mutate.md`](superpowers/plans/2026-06-18-outwall-k8s-k2-mutate.md),
    [`…-k8s-k3-exec.md`](superpowers/plans/2026-06-18-outwall-k8s-k3-exec.md).

## findings/ — non-obvious discoveries

- [`2026-06-vault-cli-needs-tty.md`](findings/2026-06-vault-cli-needs-tty.md) — `vault init`/`unlock` need a real TTY; add `--password-stdin` later.
