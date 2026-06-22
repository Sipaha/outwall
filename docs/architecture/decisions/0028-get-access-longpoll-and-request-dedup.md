# ADR-0028: `get_access` long-poll + request de-duplication

- **Status:** accepted
- **Date:** 2026-06-22

## Context

Watching a real agent use the control plane surfaced two annoyances:

1. **Busy-polling.** `request_*` returns `pending` immediately (non-blocking by design, ADR-0005) and
   the agent then calls `get_access` in a tight loop — many calls per second — until the operator
   decides. Wasteful and noisy.
2. **Duplicate cards.** The agent called `request_k8s_access` twice (a retry / double-call), and each
   call logged an intent row and enqueued an approval, so the operator saw **two identical pending
   cards** for one need.

## Decision

**`get_access` long-polls.** When the calling agent has an outstanding *pending* access request for
the upstream (and an event subscription is wired), `get_access` blocks until the operator decides or
`getAccessWait` (25s, well under typical MCP client timeouts) elapses, then returns the current
status. It wakes on the approval event bus (`events.Bus.Subscribe`, injected via `Service.SetEvents`)
and re-checks; a 2s safety tick guards against a missed event, and it re-checks once up front to
close the subscribe race. The agent therefore calls `get_access` once and waits, instead of
spinning; the tool description says so explicitly. Status is still rule-derived — only the *waiting*
is added.

**Requests de-duplicate.** Before logging an intent and enqueuing, `request_host_access`,
`request_access`, and `request_k8s_access` check the approval queue for an equivalent pending entry
(same agent + upstream, plus the discriminating fields: k8s tuple, or HTTP method+path-template).
If one already awaits a decision, the call returns `pending` without raising a second card or logging
a second `access_requests` row.

## Alternatives considered

- **Make `request_*` block until decided** (return granted/denied directly). Rejected — it reverses
  the deliberate non-blocking request design (ADR-0005) and a slow operator would hold the request
  tool call open; long-polling `get_access` keeps request submission instant while still removing the
  spin. (The blocking lever lives on the data plane, which must wait to forward.)
- **Client-side backoff guidance only** (tell the agent to sleep between polls). Rejected — relies on
  every agent obeying; server-side long-poll makes the good behaviour automatic.
- **Dedupe by writing a uniqueness constraint on `access_requests`.** Rejected — the actionable
  duplicate is the approval *card* (the queue), not the log row; checking the queue covers both and
  needs no schema change. A re-request after a card is resolved/timed-out correctly creates a fresh
  one.

## Consequences

- The agent makes ~one `get_access` call per decision instead of hundreds; the operator sees one card
  per distinct request.
- `get_access` can now block up to ~25s — bounded, and only when the agent actually has a pending
  request. With no subscription wired (`SetEvents` not called) it answers immediately as before.
- New seam `Service.SetEvents(bus.Subscribe)`; the daemon wires it. Covered by
  `TestGetAccessLongPollReturnsOnGrant` and `TestRequestK8sAccessDedupesPending`.
