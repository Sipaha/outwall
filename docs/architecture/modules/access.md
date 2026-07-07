# module: internal/access

The registry of **access-request intents**: the log of which agent asked for which upstream,
with what stated purpose, and the operator's decision. Access itself stays rule-derived (see
`internal/policy`); these records are the operator's queue and audit of intent — created by the
MCP `request_access` tool and resolved from the admin API/CLI. Records live in the
`access_requests` table. See ADR-0003.

`Resolve` validates the target status before touching the row, so an invalid status is rejected
regardless of whether the id exists; a missing id returns `ErrNotFound`.

**Revoke.** `MarkRevoked` sets status `revoked` (a status `Resolve` cannot produce — it is only
reached via the daemon's explicit revoke path, `internal/daemon.hAccessRequestRevoke`), distinct
from `denied` (never granted) and `dismissed` (an operator no-op on a still-pending card). The
revoke handler pairs it with `policy.Registry.DeleteBySubjectUpstream` to actually remove the
granted rules — this registry only tracks the intent/history row.

`MarkRevokedBySubjectUpstream(agentID, upstreamID string) (int64, error)` is the grant-scoped
sibling (ADR-0042): it bulk-marks every currently-`granted` request for the pair `revoked` in one
`UPDATE ... WHERE agent_id=? AND upstream_id=? AND status='granted'`, leaving pending/denied rows
untouched, and returns the count affected. The daemon's `hGrantRevoke` (`POST /grants/revoke`)
pairs it with `policy.Registry.DeleteBySubjectUpstream` the same way `hAccessRequestRevoke` does,
but keyed by the (agent, upstream) pair instead of one request id — this is the only revoke path
the Access UI calls; `hAccessRequestRevoke`/`MarkRevoked` remain for the single-request case.

## Public API

- `ErrNotFound`.
- Status consts: `StatusPending = "pending"`, `StatusGranted = "granted"`, `StatusDenied = "denied"`, `StatusDismissed = "dismissed"`, `StatusRevoked = "revoked"`.
- `Request struct { ID, AgentID, UpstreamID, Purpose, Status, Reason string; CreatedAt time.Time; ResolvedAt string }` (`Reason` = operator deny reason, ADR-0024).
- `NewRegistry(s *store.Store) *Registry`.
- `(*Registry).Create(agentID, upstreamID, purpose string) (*Request, error)` — logs a new intent with status `pending`.
- `(*Registry).GetByID(id string) (*Request, error)` — `ErrNotFound` if absent.
- `(*Registry).List() ([]*Request, error)` — newest first.
- `(*Registry).Pending() ([]*Request, error)` — status `pending`, newest first.
- `(*Registry).Resolve(id, status string) error` — records the decision (`granted`/`denied`/`dismissed`) + `resolved_at=now`; validates status; `ErrNotFound` if absent.
- `(*Registry).MarkRevoked(id string) error` — sets status `revoked` + `resolved_at=now`; `ErrNotFound` if absent.
- `(*Registry).MarkRevokedBySubjectUpstream(agentID, upstreamID string) (int64, error)` — bulk-marks every `granted` request for the pair `revoked` + `resolved_at=now`; returns the number affected (ADR-0042).
- `(*Registry).DenyLatest(agentID, upstreamID, reason string) (bool, error)` — marks the latest *pending* request for the pair denied + reason; reports whether a row matched (ADR-0024).
- `(*Registry).Latest(agentID, upstreamID string) (*Request, bool, error)` — the most recent request for the pair; `get_access` consults it to surface a denial + reason.
