# Current phase

> Forward-only. What's active now and what's queued next. Finished work lives in git.

## Active phase

**Plan 1 — Foundation & data-plane skeleton.**

Goal: a working vertical slice — agent bearer token, vault-encrypted upstream credential, and
a localhost reverse proxy that injects upstream auth, enforces default-deny via grants, and
returns 503 while the vault is locked — all driven by a CLI over a Unix socket. No MCP, no
web UI, no policy engine yet.

Spec: `docs/superpowers/specs/2026-06-17-outwall-design.md`
Plan: `docs/superpowers/plans/2026-06-17-outwall-foundation.md` (9 tasks, TDD).

Tasks: scaffold → SQLite store → vault (Argon2id+AES-GCM) → upstream registry → agent
registry → authenticators (none/static/basic) → grant registry → data-plane proxy → daemon
assembly + unix-socket admin API + CLI.

## Queued candidates (Phase 1, later plans)

- **Plan 2** — policy engine (rules: subject×upstream×method×path-glob×rate-limit →
  allow/deny/require-approval) + blocking approval queue + OIDC client-credentials authenticator.
- **Plan 3** — MCP control plane (streamable HTTP; `list_upstreams`, `request_access`,
  `get_access`, `whoami`; dynamic agent self-registration).
- **Plan 4** — audit (request journal + body store ≤256 KB, masking, retention).
- **Plan 5** — daemon control API + SSE for the UI.
- **Plan 6** — web UI (React 19 + Vite + Tailwind 4 + Zustand screens).
- **Plan 7** — Wails 3 desktop wrapper.

## Phase 2+ (deferred by design)

OIDC authorization-code (browser login), request body/param filters, ticket/async approval
fallback, audit auto-prune, additional authenticators (mTLS/SigV4/HMAC), headless server mode.
