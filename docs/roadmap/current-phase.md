# Current phase

> Forward-only. What's active now and what's queued next. Finished work lives in git.

## Active phase

**Plan 3 — MCP control plane (streamable HTTP).**

Goal: the single MCP server agents talk to for discovery + dynamic self-registration + token
issuance. Tools: `list_upstreams` (what exists + my per-upstream status), `request_access(host_or_upstream, purpose)` (→ granted+base-path / pending-approval / denied),
`get_access(upstream)` (base path + memo), `whoami`. First MCP contact auto-registers the
agent (status `new`) and issues its bearer token. Wires to the agent registry + policy engine
+ approval queue from Plans 1–2. Not yet started — needs a plan written (decide the Go MCP
transport/SDK first).

## Done

- **Plan 1 — Foundation & data-plane skeleton.** Vault (Argon2id+AES-GCM), SQLite store,
  upstream/agent registries, none/static/basic authenticators, data-plane reverse proxy
  (default-deny + auth injection + 503-when-locked), daemon `serve` + unix-socket admin API +
  CLI. e2e verified against httpbin. ADR-0001.
- **Plan 2 — Policy + approval + OIDC-CC.** `policy` engine (rules subject×upstream×method×
  path-glob×rate-limit → allow/deny/require-approval; agent>any>default-deny, most-restrictive
  wins), in-memory fixed-window rate limiter, blocking `approval` queue (5-min long-poll),
  `oidc-client-credentials` authenticator + per-upstream token-cache Manager. `grant`
  package/table deleted (alpha, no legacy path). rules/approvals admin API + `rule`/`approval`
  CLI. e2e (approval + 429) verified. ADR-0002.

## Queued candidates (Phase 1, later plans)
- **Plan 4** — audit (request journal + body store ≤256 KB, masking, retention).
- **Plan 4** — audit (request journal + body store ≤256 KB, masking, retention).
- **Plan 5** — daemon control API + SSE for the UI.
- **Plan 6** — web UI (React 19 + Vite + Tailwind 4 + Zustand screens).
- **Plan 7** — Wails 3 desktop wrapper.

## Phase 2+ (deferred by design)

OIDC authorization-code (browser login), request body/param filters, ticket/async approval
fallback, audit auto-prune, additional authenticators (mTLS/SigV4/HMAC), headless server mode.
