# module: internal/access

The registry of **access-request intents**: the log of which agent asked for which upstream,
with what stated purpose, and the operator's decision. Access itself stays rule-derived (see
`internal/policy`); these records are the operator's queue and audit of intent — created by the
MCP `request_access` tool and resolved from the admin API/CLI. Records live in the
`access_requests` table. See ADR-0003.

`Resolve` validates the target status before touching the row, so an invalid status is rejected
regardless of whether the id exists; a missing id returns `ErrNotFound`.

## Public API

- `ErrNotFound`.
- Status consts: `StatusPending = "pending"`, `StatusGranted = "granted"`, `StatusDenied = "denied"`, `StatusDismissed = "dismissed"`.
- `Request struct { ID, AgentID, UpstreamID, Purpose, Status, Reason string; CreatedAt time.Time; ResolvedAt string }` (`Reason` = operator deny reason, ADR-0024).
- `NewRegistry(s *store.Store) *Registry`.
- `(*Registry).Create(agentID, upstreamID, purpose string) (*Request, error)` — logs a new intent with status `pending`.
- `(*Registry).List() ([]*Request, error)` — newest first.
- `(*Registry).Pending() ([]*Request, error)` — status `pending`, newest first.
- `(*Registry).Resolve(id, status string) error` — records the decision (`granted`/`denied`/`dismissed`) + `resolved_at=now`; validates status; `ErrNotFound` if absent.
- `(*Registry).DenyLatest(agentID, upstreamID, reason string) (bool, error)` — marks the latest *pending* request for the pair denied + reason; reports whether a row matched (ADR-0024).
- `(*Registry).Latest(agentID, upstreamID string) (*Request, bool, error)` — the most recent request for the pair; `get_access` consults it to surface a denial + reason.
