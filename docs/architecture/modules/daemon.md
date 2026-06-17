# module: internal/daemon

Wires the store, vault, registries, data-plane proxy, and MCP control plane together, and
serves three listeners: the data plane (TCP localhost), the MCP control plane (a separate TCP
localhost listener), and a JSON admin API over a `0600` unix socket. The only package that
composes the others. The MCP handler (`internal/mcp`) is built in `New` from `mcpsvc.New(...)`
and is passed the vault's `Locked` probe so the control-plane tools answer clearly when locked.

Admin endpoints: `POST /vault/init`, `POST /vault/unlock`, `GET /vault/status`,
`POST /upstreams`, `GET /upstreams` (secrets omitted), `POST /agents/register`,
`GET /agents`, `POST /rules`, `GET /rules`, `DELETE /rules/{id}`, `GET /approvals`,
`POST /approvals/{id}/resolve`, `GET /access-requests` (joined with agent + upstream names),
`POST /access-requests/{id}/resolve` (`{status}` ∈ granted/denied/dismissed; 404 if absent).
Resolving an access request only records the operator's decision — granting actual access is
still done by creating rules via `POST /rules`.

## Public API

- `DefaultMCPListen = "127.0.0.1:8181"`.
- `Config struct { DBPath, SocketPath, Listen, MCPListen string }` (`MCPListen` defaults to `DefaultMCPListen`).
- `New(cfg Config) (*Daemon, error)` — opens the store, builds vault + registries + proxy + MCP handler (no listeners).
- `(*Daemon).AdminHandler() http.Handler` — the admin API mux.
- `(*Daemon).Serve(ctx context.Context) error` — runs all three listeners until ctx is canceled.
- `(*Daemon).Close() error`.
