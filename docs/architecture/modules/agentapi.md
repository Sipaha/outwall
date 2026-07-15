# module: internal/agentapi

The plain `net/http` HTTP/JSON adapter that exposes the control-plane domain service
(`internal/mcpsvc`) over the agent unix socket (`agent.sock`, `0600`). Replaces the retired
MCP go-sdk adapter (`internal/mcp`, ADR-0003) — see ADR-0040. **Unprivileged by construction**:
its routes can only read status and enqueue approval requests; they cannot express
approve/grant/unlock. No dependency on the MCP go-sdk.

**Auth = bearer token, no session cache.** Each request authenticates its `Authorization: Bearer
<token>` header via `agent.Registry.Authenticate` — unlike the retired MCP adapter there is no
`sessionID → agent` cache; every call is authenticated independently, so daemon restarts and
agent-process restarts don't invalidate identity (the token is minted once per project by
`internal/agentid` and persisted to disk). A missing/unknown token ⇒ 401
`{"error":"missing or invalid agent token"}`.

**Routes** (all under the agent socket, no `/api` prefix):
- `POST /register` — unprivileged self-registration (default-deny new agent); mints a token.
  An absent/empty body is a valid anonymous registration (name defaults to `"agent"`); only
  malformed JSON is rejected.
- `GET /upstreams` — `ListUpstreams`.
- `GET /whoami` — `WhoAmI` plus the presented bearer token (the registry stores only its hash,
  so this is the only way an agent recovers it, mirroring the retired `whoami` MCP tool).
- `GET /instructions` — the caller's identity (`WhoAmI`) plus `Deps.Info` (`EnvInfo`): the live
  data-plane URL/port, browser origin domain + cookie name, and CA path. Generated from the running
  daemon so the mechanical usage facts (ports, origin pattern, cookie) never drift; the CLI
  (`outwall instructions`) renders it into a Markdown agent playbook.
- `POST /access/host` — `RequestHostAccess {host, purpose}`.
- `POST /access/op` — `RequestAccess {host, method, path_template, query_template, body_template,
  variables, values, purpose}`.
- `POST /access/k8s` — `RequestK8sAccess {cluster, namespace, grants: [{resource, verbs[]}],
  purpose}`.
- `POST /access/preset` — `RequestPreset {upstream, preset, vars, purpose}`.
- `GET /access/{upstream}` — `GetAccess`. Long-polls (~25s) internally when a decision is
  pending, same behavior as the retired MCP `get_access` tool (ADR-0028).
- `GET /kubeconfig/{cluster}` — `Kubeconfig`, carrying the caller's own bearer token.

Empty/whitespace `purpose` (and, for `/access/k8s`, empty `cluster` or no `grants`; for
`/access/preset`, empty `preset`) ⇒ 400 before anything is logged.

**Vault-locked messaging.** `Deps.Locked func() bool` (the vault's `Locked`) — when the vault is
locked, the access/upstreams routes answer 409 with a clear "vault locked — ask the operator to
unlock" message rather than erroring opaquely.

## Public API

- `Deps struct { Svc *mcpsvc.Service; Agents *agent.Registry; Locked func() bool }` (`Locked` nil
  ⇒ treated as unlocked).
- `NewHandler(deps Deps) http.Handler` — an `*http.ServeMux` with the routes above, served over
  `agent.sock` by the daemon (see `daemon.md`).
