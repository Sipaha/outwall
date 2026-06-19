# module: internal/daemon

Wires the store, vault, registries, data-plane proxy, and MCP control plane together, and
serves four listeners: the data plane (TCP localhost), the MCP control plane (a separate TCP
localhost listener), a JSON admin API over a `0600` unix socket, and the desktop-UI control
API + SSE over a loopback TCP bind (`UIListen`, default `127.0.0.1:8182`). The only package
that composes the others. The MCP handler (`internal/mcp`) is built in `New` from
`mcpsvc.New(...)` and is passed the vault's `Locked` probe so the control-plane tools answer
clearly when locked.

`New` builds an `events.Bus` and injects it (nil-safe `SetPublisher`) into the approval queue,
the audit recorder, and the MCP service, and holds it on the daemon. The admin handlers publish
domain events on success — `agent.registered`, `upstream.created`, `rule.created`,
`vault.unlocked` — which (together with `approval.*`, `audit.recorded`, `access.requested`) the
SSE endpoint fans out. See ADR-0005 and `events.md`.

## Transports

- **Unix socket** (`AdminHandler`) — CSRF-free, for the local CLI. Serves `apiMux()` at root.
- **UIListen TCP** (`UIHandler`) — serves the embedded web UI plus the control API:
  `/api/**` → `StripPrefix("/api")` → `csrfMiddleware` → `apiMux()` (the shared admin routes +
  `GET /events`); `/**` → the embedded SPA (`staticUI`, see `webui.md` + ADR-0006). Any `/api`
  request lacking a non-empty `X-Outwall-CSRF` header → 403, **except `GET /api/events`** (SSE),
  which is exempt because `EventSource` cannot set headers (read-only, same-origin, loopback —
  ADR-0006). Static assets are not CSRF-gated. The route table is registered once (`apiMux`) and
  shared by both transports. The CSRF gate is a CSRF-not-auth boundary; loopback bind +
  single-tenant host is the trust model (ADR-0005).

The endpoint paths below are written without the `/api` prefix the UI transport adds; the unix
socket serves them at root, the UI transport under `/api`.

Admin endpoints: `POST /vault/init`, `POST /vault/unlock`, `GET /vault/status`,
`POST /upstreams`, `GET /upstreams` (secrets omitted), `POST /agents/register`,
`GET /agents`, `POST /rules`, `GET /rules`, `DELETE /rules/{id}`, `GET /approvals`,
`POST /approvals/{id}/resolve`, `GET /access-requests` (joined with agent + upstream names),
`POST /access-requests/{id}/resolve` (`{status}` ∈ granted/denied/dismissed; 404 if absent),
`GET /audit?limit=N` (journal, newest first, no bodies), `GET /audit/{id}` (entry + masked
headers + bodies; stored text bodies decoded to a `body` string, non-text → metadata only;
404 if absent), `POST /audit/prune {older_than_rfc3339}` → `{deleted:N}`,
`GET /settings/audit-retention` → `{days:N}`, `PUT /settings/audit-retention {days}` (validated
≥0; `0` = keep all).

**Background audit pruner.** `Serve` launches `audit.Recorder.RunPruner(ctx, PruneInterval)` in a
goroutine: every interval (default `DefaultPruneInterval` = 1h) it reads the stored retention and
deletes entries older than it (no-op when retention is 0). The goroutine exits on `ctx.Done()`.
`Config.PruneInterval` overrides the cadence; a negative value disables the pruner (tests).

**Headless / server mode.** `outwall serve` (or `make run-server`) runs the full daemon — data
plane (HTTPS), MCP control plane, UI control-API+SSE listener, and the unix admin socket — with **no
GUI**. The desktop Wails wrapper (`cmd/outwall-desktop`) is optional: it runs the same daemon
in-process and renders the embedded UI, and is the only piece that needs CGO/GTK. In headless mode
`Config.OnFocusRequest` is nil, so `POST /desktop/focus` simply has no window to raise. Unlock the
vault headlessly with `outwall vault unlock --password-stdin` (ADR-0018). The UI is reachable at
`http://127.0.0.1:8182/`.
Resolving an access request only records the operator's decision — granting actual access is
still done by creating rules via `POST /rules`. The daemon builds one `audit.Recorder` over the
store and passes it to the proxy (see `audit.md`, ADR-0004).

## Public API

- `DefaultMCPListen = "127.0.0.1:8181"`; `DefaultUIListen = "127.0.0.1:8182"`.
- `Config struct { DBPath, SocketPath, Listen, MCPListen, UIListen, CADir string; PruneInterval time.Duration; OnFocusRequest func() }` (`MCPListen`/`UIListen` default to their `Default*` consts; `PruneInterval` 0 → `DefaultPruneInterval`, negative disables).
- `DefaultPruneInterval = time.Hour`.
- `New(cfg Config) (*Daemon, error)` — opens the store, builds vault + registries + proxy + MCP handler + event bus (no listeners).
- `(*Daemon).AdminHandler() http.Handler` — the CSRF-free `apiMux` at root (unix socket).
- `(*Daemon).UIHandler() http.Handler` — embedded SPA at `/` + `apiMux` under `/api` (CSRF-gated,
  except `GET /api/events`), for the UIListen TCP bind. See `webui.md`, ADR-0006.
- `(*Daemon).Serve(ctx context.Context) error` — runs all four listeners until ctx is canceled.
- `(*Daemon).Close() error`.
