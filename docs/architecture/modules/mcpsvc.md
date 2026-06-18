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
`needs-request→pending`, `denied→denied`.

**Resolution:** `get_access` resolves the upstream by name, else by base-URL host match (a
leading scheme on the input is stripped). An unknown upstream is `denied`.

**H2 (host + operation MCP channels).** Two enqueueing tools feed the blocking `approval.Queue`
(wired via `SetApprovals`); both are **non-blocking** — the service parks the `Pending` from a
background goroutine and returns `granted|pending|denied` while the agent polls `get_access`:
- `RequestHostAccess(agentID, host, purpose)` — `GetOrCreateByHost` registers the host lazily,
  `access.Create` logs the intent, and (unless already `granted`) a `KindHostAccess` pending is
  parked carrying the host + purpose.
- `RequestAccess(agentID, RequestAccessInput)` — validates the proposed operation template with
  `optemplate.Parse` (**a malformed template returns an error → a tool error, not a pending**),
  then parks a `KindOperation` pending carrying the parsed shape (`OpMethod`/`OpPathTemplate`/
  `OpQueryTemplate`), the declared `OpVariables`, the requested `OpValues`, and the purpose. The
  rule create/extend happens on the daemon resolve path (ADR-0015), not here.

**K1 (k8s clusters).** `ListUpstreams` reports each target's `Kind` (`http`/`k8s`). `Kubeconfig`
assembles an agent kubeconfig for a k8s cluster using the calling agent's own token (via
`k8s.Kubeconfig`, the data-plane URL + local CA injected by `SetKubeconfigParams`); the cluster's
real credentials are never included. The `internal/mcp` adapter exposes it as the
`get_kubeconfig` tool.

## Public API

- `UpstreamInfo struct { Name, BaseURL, Kind, Status string }` (Status: `open|needs-request|denied`).
- `AccessResult struct { Status, BasePath, Memo string }` (Status: `granted|pending|denied`).
- `Variable struct { Name, Type string }` (`Type`: `text|date`).
- `RequestAccessInput struct { Host, Method, PathTemplate string; QueryTemplate, Values map[string]string; Variables []Variable; Purpose string }`.
- `Identity struct { AgentID, Name, Status string; Accesses []string }`.
- `New(a *agent.Registry, u *upstream.Registry, p *policy.Registry, ac *access.Registry) *Service`.
- `(*Service).SetApprovals(q *approval.Queue)` — wires the queue the host/operation tools enqueue into.
- `(*Service).SetKubeconfigParams(dataPlaneURL, caPEM string)`.
- `(*Service).Kubeconfig(cluster, agentToken string) ([]byte, error)` — k8s cluster only.
- `(*Service).ListUpstreams(agentID string) ([]UpstreamInfo, error)`.
- `(*Service).RequestHostAccess(agentID, host, purpose string) (AccessResult, error)` — lazy host + logs intent + enqueues a host approval (unless already granted).
- `(*Service).RequestAccess(agentID string, in RequestAccessInput) (AccessResult, error)` — validates the template (bad → error), enqueues an operation approval.
- `(*Service).GetAccess(agentID, upstreamName string) (AccessResult, error)` — no intent logged.
- `(*Service).WhoAmI(agentID string) (Identity, error)` — `Accesses` lists upstreams currently `open` for the agent.
