# module: internal/mcp

The thin adapter that exposes the four control-plane tools over the official MCP go-sdk
(`github.com/modelcontextprotocol/go-sdk`, v1.6.1) as a streamable-HTTP `http.Handler`. It is
the **only** package that imports the go-sdk; all domain logic lives in the SDK-free
`internal/mcpsvc`. See ADR-0003.

**Tools** (registered via `mcp.AddTool`, schemas inferred from the I/O structs):
- `list_upstreams` — `{} → { upstreams: []UpstreamInfo }`.
- `request_access` — `{ host, purpose } → AccessResult`. Empty/whitespace `purpose` ⇒ a tool error ("purpose is required") before anything is logged.
- `get_access` — `{ upstream } → AccessResult`.
- `whoami` — `{} → Identity + { token }` (the minted bearer token, from the in-memory cache).

**Session = agent presence.** Each MCP session (keyed by `ServerSession.ID()`) is bound, on its
first tool call, to a get-or-created agent. The client name comes from
`ServerSession.InitializeParams().ClientInfo.Name` (fallback `"mcp-agent"`) plus a short slice
of the session id. The bearer token is minted once at `agent.Registry.Register` and cached in a
mutex-guarded map `sessionID → {agentID, token}`; since the registry stores only the token hash,
`whoami` returns the token from this map. A reconnect is a new session ⇒ a new agent record — a
known limitation (ADR-0003).

**Vault-locked messaging.** `Deps.Locked func() bool` (the vault's `Locked`) — when the vault is
locked, `list_upstreams`/`request_access`/`get_access` answer with a clear "vault locked — ask
the operator to unlock" tool result rather than erroring opaquely.

## Public API

- `Deps struct { Svc *mcpsvc.Service; Agents *agent.Registry; Logger *slog.Logger; Locked func() bool }` (`Svc` and `Agents` required; `Logger` defaults to `slog.Default()`; `Locked` nil ⇒ treated as unlocked).
- `NewHandler(deps Deps) (http.Handler, error)` — returns the configured `StreamableHTTPHandler`.
