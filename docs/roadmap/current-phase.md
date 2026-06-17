# Current phase

> Forward-only. What's active now and what's queued next. Finished work lives in git.

## Active phase

**Plan 4 — Audit (request journal + body store).**

Goal: record every data-plane request/response — timestamp, agent, upstream, method, path+query,
status, duration, sizes, policy decision (+ who/when approved) — with request and response
**bodies up to 256 KB** (truncate larger; non-text by Content-Type → metadata only: type, size,
sha256), credentials injected by outwall **masked** in the log, stored in SQLite (bodies in a
separate table so the journal stays light to list), keep-all + manual purge. Wire as data-plane
middleware around the proxy. Admin API + CLI to list/inspect entries. Not yet started — needs a
plan written.

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
- **Plan 3 — MCP control plane.** Single streamable-HTTP MCP server (go-sdk v1.6.1) on its own
  localhost listener: tools `list_upstreams`/`request_access(host,purpose)`/`get_access`/`whoami`;
  session=agent auto-registration + bearer-token mint on first contact; access-request intent
  log (captures purpose); SDK-free `mcpsvc` + thin `mcp` adapter. e2e via live SDK client. ADR-0003.

## Queued candidates (Phase 1, later plans)
- **Plan 5** — daemon control API + SSE for the UI (stream agents / approvals / access requests / audit tail).
- **Plan 6** — web UI (React 19 + Vite + Tailwind 4 + Zustand screens).
- **Plan 7** — Wails 3 desktop wrapper.

## Phase 2+ (deferred by design)

OIDC authorization-code (browser login), request body/param filters, ticket/async approval
fallback, audit auto-prune, additional authenticators (mTLS/SigV4/HMAC), headless server mode.
