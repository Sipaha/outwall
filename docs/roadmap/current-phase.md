# Current phase

> Forward-only. What's active now and what's queued next. Finished work lives in git.

## Active phase

**Plan 5 — Daemon control API + SSE for the UI.**

Goal: consolidate the UI-facing surface and add a live event stream. A read/control HTTP API
(over the same admin transport, plus a localhost TCP bind for the desktop webview) exposing
agents / upstreams / rules / approvals / access-requests / audit, and an **SSE** endpoint that
pushes live events: new agent registered, approval enqueued/resolved, access-request logged,
audit entry recorded, vault lock/unlock. This is the seam the web UI (Plan 6) and Wails wrapper
(Plan 7) consume. Not yet started — needs a plan written; decide the API shape + event taxonomy
+ how the approval queue / audit recorder publish events (an in-process event bus).

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
- **Plan 4 — Audit.** Data-plane request journal (`audit_log` + separate `audit_bodies` tables)
  with capped streaming body capture (≤256 KB, truncation + total size; non-text → metadata-only
  + sha256), masked request headers, decision/rule recorded; entry written on response-body
  close; deny/429/approval-denied/502 early outcomes recorded. Admin API + `audit list|show|prune`
  CLI. Nil-safe (prior behavior unchanged when absent). e2e verified. ADR-0004.

## Queued candidates (Phase 1, later plans)
- **Plan 6** — web UI (React 19 + Vite + Tailwind 4 + Zustand screens).
- **Plan 7** — Wails 3 desktop wrapper.

## Phase 2+ (deferred by design)

OIDC authorization-code (browser login), request body/param filters, ticket/async approval
fallback, audit auto-prune, additional authenticators (mTLS/SigV4/HMAC), headless server mode.
