# module: internal/approval

The blocking approval queue. When `policy.Decide` returns `require-approval`, the proxy calls
`Submit`, which registers an in-memory pending entry and **blocks** until the operator calls
`Resolve`, a timeout elapses (default 5 minutes ⇒ denied), or the request context is canceled.
The queue is intentionally **not persisted** — a pending approval is meaningful only while its
HTTP request is in flight. Resolve uses a buffered channel so it never blocks even if the waiter
has already departed (timeout/cancel).

## Public API

- `DefaultTimeout = 5 * time.Minute`; error `ErrNotFound`.
- `Pending struct { ID, AgentID, UpstreamID, Method, Path, Purpose string; CreatedAt time.Time }`
- `NewQueue() *Queue` (default timeout) / `NewQueueWithTimeout(d time.Duration) *Queue`.
- `(*Queue).SetPublisher(p events.Publisher)` — nil-safe; `Submit` then publishes `approval.enqueued` `{id, agent_id, upstream_id, method, path, purpose}` and `Resolve` publishes `approval.resolved` `{id, approved}` (see ADR-0005).
- `(*Queue).Submit(ctx context.Context, p Pending) (approved bool, err error)` — generates the entry ID; `false,nil` on timeout, `false,ctx.Err()` on cancel.
- `(*Queue).List() []Pending` — snapshot of waiting entries.
- `(*Queue).Resolve(id string, approve bool) error` — `ErrNotFound` for an unknown id.
