# Current phase

> Forward-only. What's active now and what's queued next. Finished work lives in git.

## Active phase

**Plan 6B — Remaining web UI screens.**

Goal: build the rest of the dark-console UI on the 6A foundation — Upstreams (CRUD + auth method
+ secrets), Rules editor, Approvals page (with purpose, allow/deny), Audit (journal + body
viewer), Agent detail, Settings. All wire to the existing `api.ts`/`types.ts`/events store and
the dark theme. Not yet started — needs a plan written.

UI direction chosen: **dark "developer console"** (Darcula/Lens palette reused from
citeck-launcher; no citeck branding). Foundation + Unlock + Dashboard shipped in 6A; aesthetic
verified via live browser smoke.

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
- **Plan 5 — Control API + SSE.** In-process non-blocking event `Bus` (drop-on-full), publishers
  in approval/audit/mcpsvc + admin handlers (`agent.registered`, `upstream.created`, `rule.created`,
  `vault.unlocked`, `approval.enqueued/resolved`, `audit.recorded`, `access.requested`), `GET /events`
  SSE handler (heartbeat), and a `UIListen` loopback TCP bind serving the admin mux behind an
  `X-Outwall-CSRF` gate. e2e SSE verified. ADR-0005.
- **Plan 6A — Web UI foundation.** Embedded React 19 / Vite 8 / Tailwind 4 / Zustand UI built
  into `internal/daemon/webdist` (`go:embed`), served on `UIListen` with `/api` prefix + SPA
  static + SSE CSRF-exempt. Dark Darcula/Lens theme, typed `api.ts` (`X-Outwall-CSRF`), SSE store,
  app shell (sidebar), **Unlock** + **Dashboard** (agents + live approval queue). Live browser
  smoke verified. ADR-0006.

## Queued candidates (Phase 1, later plans)
- **Plan 7** — Wails 3 desktop wrapper (supervises the daemon, renders the embedded UI, unlock screen at launch).

## Phase 2+ (deferred by design)

OIDC authorization-code (browser login), request body/param filters, ticket/async approval
fallback, audit auto-prune, additional authenticators (mTLS/SigV4/HMAC), headless server mode.
