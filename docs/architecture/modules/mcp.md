# module: internal/mcp

The thin adapter that exposes the control-plane tools over the official MCP go-sdk
(`github.com/modelcontextprotocol/go-sdk`, v1.6.1) as a streamable-HTTP `http.Handler`. It is
the **only** package that imports the go-sdk; all domain logic lives in the SDK-free
`internal/mcpsvc`. See ADR-0003.

**Tools** (registered via `mcp.AddTool`, schemas inferred from the I/O structs):
- `list_upstreams` — `{} → { upstreams: []UpstreamInfo }`.
- `request_host_access` — `{ host, purpose } → AccessResult`. Tier-1 for HTTP hosts (register + attach a credential). For a k8s cluster or an already-credentialed HTTP host it short-circuits to `granted` with guidance — no card (ADR-0025).
- `request_access` — `{ host, method, path_template, query_template, variables, values, purpose } → AccessResult`. Tier-2 HTTP operation. A malformed template ⇒ a tool error.
- `request_k8s_access` — `{ cluster, namespace, resource, verb, purpose } → AccessResult`. The k8s operation channel; on approve creates an agent-scoped allow k8s rule (ADR-0025). Then call `get_kubeconfig` + use kubectl.
- `get_access` — `{ upstream } → AccessResult`.
- `get_kubeconfig` — `{ cluster } → { kubeconfig }`. Emits a kubeconfig pointing at the data plane, carrying the agent's own token (never the cluster's real credentials).
- `whoami` — `{} → Identity + { token }` (the minted bearer token, from the in-memory cache).

Empty/whitespace `purpose` on any request tool ⇒ a tool error ("purpose is required") before anything is logged.

**Session = agent presence.** Each MCP session (keyed by `ServerSession.ID()`) is bound, on its
first tool call, to a get-or-created agent. The client name comes from
`ServerSession.InitializeParams().ClientInfo.Name` (fallback `"mcp-agent"`) plus a short slice
of the session id. The bearer token is minted once at `agent.Registry.Register` and cached in a
mutex-guarded map `sessionID → {agentID, token}`; since the registry stores only the token hash,
`whoami` returns the token from this map. A reconnect is a new session ⇒ a new agent record — a
known limitation (ADR-0003).

**Vault-locked messaging.** `Deps.Locked func() bool` (the vault's `Locked`) — when the vault is
locked, the tools answer with a clear "vault locked — ask the operator to unlock" tool result
rather than erroring opaquely.

## Public API

- `Deps struct { Svc *mcpsvc.Service; Agents *agent.Registry; Logger *slog.Logger; Locked func() bool }` (`Svc` and `Agents` required; `Logger` defaults to `slog.Default()`; `Locked` nil ⇒ treated as unlocked).
- `NewHandler(deps Deps) (http.Handler, error)` — returns the configured `StreamableHTTPHandler`.
