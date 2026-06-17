# module: internal/mcpsvc

The **SDK-free** domain service behind the four MCP control-plane tools. It resolves a
host/upstream, derives an agent's per-upstream status from the policy rules, logs access-request
intents, and builds the plain result structs the adapter marshals. It deliberately does **not**
import the MCP go-sdk — the thin adapter in `internal/mcp` wires these results to the wire
protocol — so this logic stays SDK-version-independent and is fully unit-tested. See ADR-0003.

**Status derivation** (single source of truth, `statusFor`): gather `policy.ForUpstream(up.ID)`,
keep rules whose subject is the agent or `""` (any); an agent-tier `deny` ⇒ `denied`; else any
`allow`/`require-approval` ⇒ `open`; else `needs-request`. Mapped to `AccessResult`:
`open→granted` (with `BasePath = "/"+up.Name` and a memo of the matching `METHOD PATH` rules),
`needs-request→pending-approval`, `denied→denied`.

**Resolution:** `request_access`/`get_access` resolve the upstream by name, else by base-URL
host match (a leading scheme on the input is stripped). An unknown upstream is `denied` and not
logged. `request_access` logs the intent (`access.Create`); `get_access` does not.

## Public API

- `UpstreamInfo struct { Name, BaseURL, Status string }` (Status: `open|needs-request|denied`).
- `AccessResult struct { Status, BasePath, Memo string }` (Status: `granted|pending-approval|denied`).
- `Identity struct { AgentID, Name, Status string; Accesses []string }`.
- `New(a *agent.Registry, u *upstream.Registry, p *policy.Registry, ac *access.Registry) *Service`.
- `(*Service).ListUpstreams(agentID string) ([]UpstreamInfo, error)`.
- `(*Service).RequestAccess(agentID, hostOrUpstream, purpose string) (AccessResult, error)` — logs intent; unknown upstream not logged.
- `(*Service).GetAccess(agentID, upstreamName string) (AccessResult, error)` — no intent logged.
- `(*Service).WhoAmI(agentID string) (Identity, error)` — `Accesses` lists upstreams currently `open` for the agent.
