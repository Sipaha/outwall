# Current phase

> Forward-only. What's active now and what's queued next. Finished work lives in git.

## Active phase

**Plan 7 — Wails 3 desktop wrapper.**

Goal: the desktop app — a Wails v3 thin-wrapper (`cmd/outwall-desktop`, `internal/desktop`) that
supervises the daemon as a child process and renders the embedded UI in a webview pointed at the
`UIListen` bind, with the unlock screen at launch. Mirror citeck-launcher's `internal/desktop`
supervisor pattern (CGO build, separate `cmd/`, `make build-desktop`). This is the only CGO
target; the server binary stays CGO-free. Not yet started — needs a plan written.

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

- **Plan 6B — Web UI screens.** Upstreams (CRUD + conditional auth form), Agents (+ detail), Rules
  editor (name-resolved + delete), Approvals (pending + access-request intents), Audit (journal +
  body viewer + masked headers), Settings (audit prune + vault lock; `POST /vault/lock` added). All
  six routes live. Live browser smoke verified all screens. (Within ADR-0006.)

## Queued candidates (Phase 1 — none remaining after Plan 7)

## Phase 2+ (deferred by design)

OIDC authorization-code (browser login), request body/param filters, ticket/async approval
fallback, audit auto-prune, additional authenticators (mTLS/SigV4/HMAC), headless server mode.
