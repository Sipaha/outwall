# module: internal/daemon

Wires the store, vault, registries, data-plane proxy, and agent-plane adapter together, and
serves four listeners: the data plane (TCP localhost), the agent plane (`internal/agentapi`
over a `0600` unix socket, `agent.sock`), a JSON admin API over a separate `0600` unix socket,
and the desktop-UI control API + SSE over a loopback TCP bind (`UIListen`, default
`127.0.0.1:8182`). The only package that composes the others. The MCP control plane (ADR-0003,
`internal/mcp`, TCP `:8181`) is gone (ADR-0040); the agent-plane handler is built in `New` from
`agentapi.NewHandler(agentapi.Deps{Svc: mcpsvc.New(...), Agents: ag, Locked: v.Locked})` so the
agent-facing routes answer clearly when the vault is locked.

`New` builds an `events.Bus` and injects it (nil-safe `SetPublisher`) into the approval queue,
the audit recorder, and the `mcpsvc.Service`, and holds it on the daemon. The admin handlers publish
domain events on success — `agent.registered`, `upstream.created`, `rule.created`,
`vault.unlocked` — which (together with `approval.*`, `audit.recorded`, `access.requested`) the
SSE endpoint fans out. See ADR-0005 and `events.md`.

## Transports

- **Admin unix socket** (`AdminHandler`) — for the local CLI. Serves `apiMux()` at root.
- **Agent unix socket** (`agentPlane`, `agent.sock`) — serves `agentapi.NewHandler(...)` at root;
  bearer-token auth, no session cache (see `agentapi.md`).
- **UIListen TCP** (`UIHandler`) — serves the embedded web UI plus the control API:
  `/api/**` → `StripPrefix("/api")` → `apiMux()` (the shared admin routes + `GET /events`);
  `/**` → the embedded SPA (`staticUI`, see `webui.md` + ADR-0006); `/oauth/callback` top-level
  (see OIDC below). The route table (`apiMux`) is registered once and shared by both the admin
  unix socket and the UI TCP bind.

**Operator-session gate, not CSRF (ADR-0041).** `apiMux` splits routes into an **ungated** group
— read-only GETs, `GET /events` (SSE), `POST /presets/preview` (dry-run), `POST
/desktop/focus`, `POST /vault/init` (the bootstrap that establishes the master password), and
`POST/GET /operator/session/{open,lock,status}` (the master-password entry point itself) — and
an **operator-gated** group wrapping every privileged mutation (`vault unlock/lock`, upstream/
rule/agent create-or-delete, cluster import, approval/access-request resolve-or-revoke, audit
prune, retention set) in `operatorGate`. `operatorGate` checks `d.opsession.Authorized()`
(`internal/opsession`, idle-TTL sliding window) and answers 403
`{"error":"operator session required"}` when the session isn't open — on **both** transports, so
a same-user process can no longer self-approve or self-grant just by holding the unix socket.
This replaced the ADR-0005 static `X-Outwall-CSRF` header model, which is retired.

The endpoint paths below are written without the `/api` prefix the UI transport adds; the unix
socket serves them at root, the UI transport under `/api`.

Admin endpoints: `POST /vault/init`, `POST /vault/unlock`, `GET /vault/status`,
`POST /upstreams`, `GET /upstreams` (secrets omitted), `POST /agents/register`,
`GET /agents` (newest first; each row has `created_at`/`last_seen_at` RFC3339Nano strings,
`last_seen_at` `""` if the agent never authenticated), `DELETE /agents/{id}` (cascades:
deletes the agent's policy rules first, then the agent; publishes `agent.deleted`),
`POST /rules` (body takes a `ttl_seconds int`; `0` = never — the manual-grant TTL, ADR-0045),
`GET /rules` (newest first; each row includes `expires_at`, `""` if permanent),
`DELETE /rules/{id}`, `POST /rules/{id}/renew` (`hRuleRenew`, `{ttl_seconds}` → recomputes and
persists the rule's `expires_at` via `policy.Registry.Renew`; `ttl_seconds:0` makes it permanent
— ADR-0045), `GET /approvals`,
`POST /approvals/{id}/resolve` (body also takes `ttl_seconds int`, stamped onto every rule the
approval creates or extends via `expiryFromTTL` — ADR-0045), `GET /access-requests` (joined with agent + upstream names;
newest first; `resolved_at` RFC3339Nano or `""` if unresolved),
`POST /access-requests/{id}/resolve` (`{status}` ∈ granted/denied/dismissed; 404 if absent),
`POST /access-requests/{id}/revoke` (removes the granted policy rules for that
agent+upstream, then marks the request `revoked`; publishes `access.revoked`; 404 if absent),
`POST /grants/revoke {agent_id, upstream_id}` (the grant-scoped successor used by the Access UI,
ADR-0042: `DeleteBySubjectUpstream` + `access.MarkRevokedBySubjectUpstream` — removes every rule
for the pair and marks every currently-`granted` request for it `revoked`, leaving
pending/denied rows untouched; returns `{ok, rules_removed}`; publishes `access.revoked`),
`GET /audit?limit=N` (journal, newest first, no bodies), `GET /audit/{id}` (entry + masked
headers + bodies; stored text bodies decoded to a `body` string, non-text → metadata only;
404 if absent), `POST /audit/prune {older_than_rfc3339}` → `{deleted:N}`,
`GET /settings/audit-retention` → `{days:N}`, `PUT /settings/audit-retention {days}` (validated
≥0; `0` = keep all).

**Background audit pruner.** `Serve` launches `audit.Recorder.RunPruner(ctx, PruneInterval)` in a
goroutine: every interval (default `DefaultPruneInterval` = 1h) it reads the stored retention and
deletes entries older than it (no-op when retention is 0). The goroutine exits on `ctx.Done()`.
`Config.PruneInterval` overrides the cadence; a negative value disables the pruner (tests).

**OIDC browser login (ADR-0021, ADR-0031).** `POST /upstreams/{name}/oauth/login` starts an
authorization-code login (mints a CSRF `state` + PKCE verifier, returns the IdP authorize URL). The
callback (`/callback`) is served on a **dedicated, on-demand loopback listener** (`Config.CallbackListen`,
default `127.0.0.1:23312`) so the redirect URI is a fixed, registerable value independent of the UI
port; the listener (`callbackServer`) is brought up by `hOAuthLogin` and released after the callback
or login TTL, so the port is bound only during a login. It is CSRF-exempt (a browser redirect; the
random state is the binding) and exchanges the code
for tokens, persisting them encrypted on the upstream. `GET /oidc/redirect-uri` returns that URI for
the UI to show; `POST /oidc/discover {url}` fetches the provider's well-known document to auto-fill
the endpoints (ADR-0030). The daemon wires `authn.Manager.SetOAuthPersister` so refreshed tokens are
written back. (The same handler is also still mounted at `/oauth/callback` on the UI listener for a
custom RedirectURL.)

**Headless / server mode.** `outwall serve` (or `make run-server`) runs the full daemon — data
plane (HTTPS), agent socket, UI control-API+SSE listener, and the unix admin socket — with **no
GUI**. The desktop Wails wrapper (`cmd/outwall-desktop`) is optional: it runs the same daemon
in-process and renders the embedded UI, and is the only piece that needs CGO/GTK. In headless mode
`Config.OnFocusRequest` is nil, so `POST /desktop/focus` simply has no window to raise. Unlock the
vault headlessly with `outwall vault unlock --password-stdin` (ADR-0018). The UI is reachable at
`http://127.0.0.1:8182/`.
Resolving an access request only records the operator's decision — granting actual access is
still done by creating rules via `POST /rules`. The daemon builds one `audit.Recorder` over the
store and passes it to the proxy (see `audit.md`, ADR-0004).

## Public API

- `DefaultListen = "127.0.0.1:8080"`; `DefaultUIListen = "127.0.0.1:8182"`; `DefaultCallbackListen = "127.0.0.1:23312"`; `DefaultBrowseDomain = "outwall.localhost"`.
- `Config struct { DBPath, SocketPath, Listen, UIListen, AgentSocketPath, CallbackListen, CADir, BrowseDomain string; PruneInterval time.Duration; OnFocusRequest func(); OpenURL func(string) error }` (`UIListen`/`CallbackListen`/`BrowseDomain` default to their `Default*` consts; `AgentSocketPath` empty ⇒ `<dir(DBPath)>/agent.sock`; `PruneInterval` 0 → `DefaultPruneInterval`, negative disables).
- `DefaultPruneInterval = time.Hour`.
- `New(cfg Config) (*Daemon, error)` — opens the store, builds vault + registries + proxy + agent-plane handler + event bus (no listeners).
- `(*Daemon).AdminHandler() http.Handler` — the `apiMux` at root (unix socket).
- `(*Daemon).UIHandler() http.Handler` — embedded SPA at `/` + `apiMux` under `/api` (privileged
  routes operator-session-gated, see ADR-0041), for the UIListen TCP bind. See `webui.md`, ADR-0006.
- `(*Daemon).Serve(ctx context.Context) error` — runs the data-plane, agent-socket, admin-socket,
  and UIListen listeners until ctx is canceled.
- `(*Daemon).Close() error`.
