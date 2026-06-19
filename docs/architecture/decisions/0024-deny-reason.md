# ADR-0024: Operator deny reason, surfaced to the agent

- **Status:** accepted
- **Date:** 2026-06-19

## Context

When the operator denies an agent's request, the agent only saw a generic refusal ("request not
approved" / a perpetual `pending` on the MCP path). The operator had no way to say *why*, so the
agent could not adjust (e.g. "not on prod — use staging") and the denial left no human-readable
trace. The operator should be able to attach a reason on Deny, and that reason should reach the
agent on both request paths.

## Decision

Thread an optional `reason` through the approval-resolution path and surface it per transport.

- **Approval queue** (`internal/approval`): the waiter channel now carries a `Decision{Approved,
  Reason}` (was a bare `bool`); `Submit` returns `Decision`; `Resolve(id, approve, reason)` delivers
  it (reason ignored on approve) and includes it in the `approval.resolved` event.
- **Blocking data-plane path** (`internal/proxy`): on a denied require-approval request the 403 body
  is `"request not approved: <reason>"` and the audit entry's error carries it — the agent sees the
  reason synchronously.
- **MCP async path** (`internal/mcpsvc` + `internal/access`): `access_requests` gained a `reason`
  column (additive migration, ADR-0023). On an MCP host/operation deny the resolve handler calls
  `access.DenyLatest(agent, upstream, reason)` (marks the latest pending request denied + reason);
  `request_access` now logs an intent so operation denials have a row. `get_access` consults
  `access.Latest`: when no rule grants and the latest request is `denied`, it returns
  `status:"denied"` with `memo:"denied by the operator: <reason>"` — so a polling agent stops and
  learns why.
- **Admin API + UI**: `POST /approvals/{id}/resolve` accepts `reason`; the access-request list
  returns it. The Approvals screen routes every Deny through a small reason modal (one modal shared
  by all card types, via the parent's `onResolve` interceptor), and shows the reason on denied
  access-request rows.

## Alternatives considered

- **A free-text deny that creates an agent-tier deny rule carrying the reason.** Rejected — a
  host-wide deny rule is too broad for an operation-level denial and would permanently block the
  agent; the per-request `access_requests` record is scoped and is superseded when the agent
  re-requests (a new pending row becomes the latest).
- **`window.prompt` for the reason.** Rejected — inconsistent with the app's modal UI and unreliable
  in the WebKit webview.
- **Surface only on the data-plane path.** Rejected — the agent's typical "request access" is the
  MCP path; a reason that never reaches `get_access` would miss the main case.

## Consequences

- Operators can explain denials; agents get the reason on both the blocking 403 and the MCP
  `get_access` poll, and it is recorded (audit error / access-request row / `approval.resolved`).
- `approval.Queue` API changed (`Submit`→`Decision`, `Resolve` gained `reason`) — all in-tree
  callers/tests updated.
- The MCP denial surface relies on the latest `access_requests` row per (agent, upstream); a
  re-request resets it to pending, so a stale denial never sticks after a fresh ask.
