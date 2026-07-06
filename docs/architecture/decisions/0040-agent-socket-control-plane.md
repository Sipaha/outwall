# ADR-0040: Agent socket control plane (CLI over agent.sock, per-project token)

- **Status:** accepted
- **Date:** 2026-07-06

## Context

The control plane was an MCP server (streamable HTTP; adapter `internal/mcp`). An agent harness binds
the MCP tool set at session start, so the tools never appear if the agent starts before outwall, go
stale if outwall is rebuilt mid-session, and identity was `session = agent` (ADR-0003): every
reconnect minted a new agent record + token, letting a long-lived agent escape a per-agent deny rule
by reconnecting. Agents should reach outwall through independent, stateless calls that do not care
about start order or daemon restarts. (Scope here is R1–R4 of the 2026-07-06 design spec; the
operator-plane sealing R5–R8 is a separate ADR.)

## Decision

- **Remove MCP entirely.** Delete `internal/mcp` and the `github.com/modelcontextprotocol/go-sdk`
  dependency. `mcpsvc.Service` and the registries it uses stay.
- **New agent plane** `internal/agentapi`: a plain net/http HTTP/JSON handler (`NewHandler(Deps)`)
  over the SAME `mcpsvc.Service`, served on a dedicated `0600` unix socket `~/.spk/outwall/agent.sock`.
  Each request authenticates by `Authorization: Bearer <owa-token>` via `agent.Registry.Authenticate`
  — no session cache. It is unprivileged by construction: it exposes only list/whoami/request/get
  routes; it cannot approve, grant, or unlock. Routes: `POST /register`, `GET /upstreams`,
  `GET /whoami`, `POST /access/{host,op,k8s,preset}`, `GET /access/{upstream}` (long-poll inside the
  service), `GET /kubeconfig/{cluster}`.
- **CLI is the agent's face** (`list-upstreams`, `whoami`, `request-host-access`, `request-access`,
  `request-preset`, `request-k8s-access`, `get-access`, `get-kubeconfig`). Each call is independent;
  start order and daemon restarts are irrelevant. The CLI knows the socket path by default
  (`--agent-socket`, defaulting under the data dir). `request-access` additionally takes a repeatable
  `--var name:type` flag so the CLI can declare typed operation variables (e.g. `--var
  project_path:text`) that flow through to the rule's value-scoping, alongside `--query`/`--value`
  for the template and concrete bindings.
- **Per-project accountability token** (`internal/agentid`): the CLI persists the `owa_` token in
  `~/.spk/outwall/agents/<hex-sha256(projectKey)>.token` (`0600`), where `projectKey` is the realpath
  of the git top-level when cwd is inside a repo, else the realpath of cwd. A `cd` into a subdir keeps
  the same identity. First use registers once under an exclusive `flock` on `<path>.lock` (winner
  writes atomically, losers read), so concurrent first-calls mint exactly one agent. The token hash is
  persisted by the registry, so `Authenticate` is valid across daemon restarts.

The token is **accountability-only, not an isolation boundary**: a same-user process can read any
project's token file. The real security boundary (operator approvals gated by the master password) is
addressed by the separate operator-session ADR.

## Alternatives considered

- **Keep MCP alongside the new plane** — rejected: the user wants MCP gone; alpha has no compat to
  preserve, and two control planes double the surface.
- **Per-directory (not per-project) token** — rejected: a `cd` into a subdir would mint a new agent,
  re-introducing the record-accretion problem. Keying on the git top-level fixes identity per project.
- **Serve the agent plane over loopback TCP** — rejected: a unix socket needs no port allocation, is
  `0600` by default, and matches the existing admin-socket pattern.

## Consequences

- Agents call independent CLI commands; no start-order or restart fragility; one stable agent per
  project. The `mcpsvc.Service` and its tests are unchanged (SDK-free all along).
- The go-sdk dependency and its transitive tree are dropped.
- Supersedes ADR-0003 (MCP control plane, session=agent identity).
- A future revisit that wants true cross-process isolation of the token would need a separate OS
  identity for the daemon (recorded as the escalation path in the threat-model ADR).
