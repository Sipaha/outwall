# ADR-0003: MCP control plane (streamable HTTP)

- **Status:** accepted
- **Date:** 2026-06-17

## Context

outwall's control plane is the single MCP server agents talk to: it exposes four tools
(`list_upstreams`, `request_access(host, purpose)`, `get_access(upstream)`, `whoami`),
auto-registers an agent on first contact, mints that agent's data-plane bearer token, and
records each access-request intent (with the agent's stated purpose) for the operator. The
design spec mandates: one MCP server over streamable HTTP on localhost; dynamic agent
registration + token mint on first contact; a mandatory non-empty `purpose`; the operator's
queue of intents; and clear messaging when the vault is locked.

Two forces shaped the structure. First, the official Go MCP SDK
(`github.com/modelcontextprotocol/go-sdk`) is an external dependency whose API is still
evolving — we do not want the gateway's domain logic coupled to a specific SDK version.
Second, MCP's streamable-HTTP transport gives us a per-connection **session**, but the
protocol has no built-in notion of a persistent agent identity across reconnects; we must
decide how an MCP session maps to an outwall agent record and its bearer token.

## Decision

**SDK-free domain service + thin adapter.** All control-plane logic lives in
`internal/mcpsvc` (the `Service`), which resolves a host/upstream, derives the agent's
per-upstream status from the policy rules (single source of truth: `statusFor`), logs intents,
and builds the tool result structs (`UpstreamInfo`, `AccessResult`, `Identity`). `mcpsvc` does
**not** import the go-sdk and is fully unit-tested. `internal/mcp` is the **only** package that
imports the SDK: it registers the four tools via `mcp.AddTool`, serves a
`StreamableHTTPHandler`, and translates between SDK request/response shapes and `mcpsvc`. This
keeps the logic SDK-version-independent; swapping or upgrading the SDK touches one package.

**Session = agent presence.** Each MCP session (keyed by the SDK's stable per-session
`ServerSession.ID()`) is bound, on its first tool call, to a get-or-created agent. The client
name comes from the session's initialize params (`ServerSession.InitializeParams().ClientInfo.Name`,
falling back to `"mcp-agent"`) suffixed with a short slice of the session ID. The agent's bearer
token is minted **once** at `agent.Registry.Register` and cached in an in-memory,
mutex-guarded map `sessionID → {agentID, token}`. The registry persists only the token's
SHA-256 hash, so `whoami` returns the token from this in-memory map — it is the only place the
plaintext token survives after registration.

**Access-requests are an intent log; access stays rule-derived.** `request_access` logs the
agent's intent (`internal/access`, the `access_requests` table) and reports the *current*
rule-derived status. It never itself grants access — granting is still done by the operator
creating policy rules (`POST /rules`). Resolving an access request (`granted`/`denied`/
`dismissed`) only clears the operator's queue and records the decision. An unknown upstream is
denied and **not** logged.

**Separate listener.** The MCP handler is served on its own localhost listener
(`Config.MCPListen`, default `127.0.0.1:8181`), distinct from the data-plane proxy port and the
admin unix socket, so the privileged control plane is not co-mingled with the data plane.

**Vault-locked messaging.** `NewHandler` takes a `Locked func() bool` (the vault's). When the
vault is locked, `list_upstreams` / `request_access` / `get_access` answer with a clear
"vault locked — ask the operator to unlock" tool result rather than erroring opaquely.

**Mandatory purpose.** `request_access` with an empty/whitespace `purpose` returns a tool
error ("purpose is required") before any record is logged.

**Resolved SDK version:** `github.com/modelcontextprotocol/go-sdk v1.6.1` (latest released as
of this date; added with `go get …@latest`).

## Alternatives considered

- **Adapter directly holds the logic (no `mcpsvc`)** — rejected: it would couple the domain
  rules to the SDK's request/response types and make the four tools' behaviour testable only
  through a live MCP client, slowing iteration and tying us to one SDK version.
- **Persistent agent identity via a handshake / token claim** — rejected *for now*: there is no
  standard MCP mechanism for an agent to assert "I am the same agent as last session". A future
  "claim existing token" handshake (the client presents a previously minted token at connect and
  we rebind the session to that agent) is the planned improvement; until then, session = agent.
- **One shared listener for control + data plane** — rejected: the control plane is privileged
  (mints tokens, exposes upstreams) and benefits from a distinct port and, later, distinct
  binding/firewall treatment.

## Consequences

- **Easier:** the SDK can be upgraded or replaced by editing only `internal/mcp`; the domain
  logic is exercised by fast unit tests in `internal/mcpsvc`.
- **Known limitation (deliberate):** a reconnect is a new session and therefore a **new agent
  record** with a **new token**. Long-lived agents accrete records across reconnects. This is
  acceptable in alpha; the "claim existing token" handshake above is the migration path, and it
  would not change the on-disk schema (agents already key on token hash).
- **In-memory token cache:** minted tokens live only in the adapter's map for the lifetime of
  the process. A daemon restart drops the cache; a reconnecting agent gets a fresh registration
  and token (consistent with the session=agent model). No token plaintext is ever persisted.
- **Follow-up (later plans):** audit of MCP calls (Plan 4); a control API + SSE streaming the
  access-request/approval queue to the UI (Plan 5); the web UI (Plan 6); Wails wrapper (Plan 7).
