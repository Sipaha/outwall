# module: internal/approval

The blocking approval queue. When `policy.Decide` returns `require-approval`, the proxy calls
`Submit`, which registers an in-memory pending entry and **blocks** until the operator calls
`Resolve`, a timeout elapses (default 5 minutes ⇒ denied), or the request context is canceled.
The queue is intentionally **not persisted** — a pending approval is meaningful only while its
HTTP request is in flight. Resolve uses a buffered channel so it never blocks even if the waiter
has already departed (timeout/cancel).

**K2 (k8s mutating verbs).** A `Pending` also carries the parsed k8s tuple
(`Namespace`/`Resource`/`Verb`, display-only, set by the proxy in K1) and, new in K2,
`RequestBody []byte` — the agent-sent patch/apply body, capped at `audit.BodyCap`, so the
operator sees exactly what will change. The proxy reads the body once, before `Submit`, for a
mutating-verb approval (see `proxy.md` / ADR-0009). The `approval.enqueued` event surfaces the
tuple + a **masked** `request_body` preview (`audit.MaskBody`) — never the injected cluster
credential.

**H2 (MCP control-plane approvals).** A `Pending` carries a `Kind` discriminator: `KindHostAccess`
and `KindOperation` are the MCP host/operation cards (`mcpsvc` enqueues them from a background
goroutine — non-blocking); an **empty** `Kind` is the pre-H2 data-plane new-value / k8s approval,
resolved by the queue alone. MCP approvals add `Host` and, for `KindOperation`, the operation
shape (`OpMethod`/`OpPathTemplate`/`OpQueryTemplate`), the declared `OpVariables` (`Variable{Name,
Type}`), and the requested `OpValues`. The daemon resolve path uses `Get(id)` to inspect a pending
and run its side effects (host credential attach / rule create-extend) **before** unparking the
waiter (see `admin.go` / ADR-0015). The blocking `Submit`/`Resolve` mechanism itself is unchanged.

## Public API

- `DefaultTimeout = 5 * time.Minute`; error `ErrNotFound`.
- `Kind` consts: `KindHostAccess = "host-access"`, `KindOperation = "operation"` (empty = data-plane/k8s).
- `NewValue struct { Var, Value string }`; `Variable struct { Name, Type string }`.
- `Pending struct { ID, AgentID, UpstreamID, Method, Path, Purpose string; CreatedAt time.Time; Kind, Host string; OpMethod, OpPathTemplate string; OpQueryTemplate map[string]string; OpVariables []Variable; OpValues map[string]string; Namespace, Resource, Verb string; RuleID string; NewValues []NewValue; Template string; RequestBody []byte }` (the k8s / op / new-value field groups are empty unless that kind set them).
- `NewQueue() *Queue` (default timeout) / `NewQueueWithTimeout(d time.Duration) *Queue`.
- `(*Queue).SetPublisher(p events.Publisher)` — nil-safe; `Submit` then publishes `approval.enqueued` `{id, agent_id, upstream_id, host, method, path, purpose, namespace, resource, verb[, new_values, template, rule_id][, request_body]}` and `Resolve` publishes `approval.resolved` `{id, approved, reason}` (see ADR-0005 / ADR-0009 / ADR-0024).
- `Decision struct { Approved bool; Reason string }` — the operator's verdict; `Reason` is the optional deny explanation surfaced to the agent (ADR-0024).
- `(*Queue).Submit(ctx context.Context, p Pending) (Decision, error)` — generates the entry ID; a timeout/cancel yields a non-approved `Decision{}` (timeout → `nil` err, cancel → `ctx.Err()`).
- `(*Queue).List() []Pending` — snapshot of waiting entries.
- `(*Queue).Get(id string) (Pending, bool)` — snapshot one entry by id (for the resolve path to inspect its `Kind`).
- `(*Queue).Resolve(id string, approve bool, reason string) error` — `ErrNotFound` for an unknown id; `reason` is delivered on deny (ignored on approve).
