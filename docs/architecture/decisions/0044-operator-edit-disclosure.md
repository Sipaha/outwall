# ADR-0044: the agent is told when the operator narrows its access request

- **Status:** accepted
- **Date:** 2026-07-07

## Context

An agent requests a preset with typed slots — e.g.
`request-preset enterprise --preset citeck-readonly --var sourceId=* --var workspace=*`. The
operator, on the Approvals card, may **narrow** those slot values before approving (the common case:
`workspace: * → ECOSENT`). Today that edit is invisible to the agent: `request-preset` returns
`pending`, and the subsequent `get-access` returns `granted` with a memo built only from the
resulting rules' `op_method`/`op_path_template` (empty for preset grants), so the agent never learns
that what it asked for is not what it got. In practice this manifested as the agent's next data-plane
call being denied (a get-atts with `scopeUnknown`, or a query in a workspace it no longer has) with
no explanation, because the agent still believed it held the `*` it requested.

The agent's **requested** slot bindings live only on the in-memory `approval.Pending`; the
**granted** bindings are written into policy rules. At `get-access` time the requested values are
already gone, so there is nothing to diff — the disclosure must be captured at approval time and
persisted.

## Decision

The operator's edit is captured at grant time and surfaced to the agent, symmetric with how a deny
reason is already persisted on the access-request row and surfaced by `get-access`.

- **Data model.** `access.Request` gains an `edits` column (JSON) holding
  `[]access.BindingEdit{Slot, Requested, Granted}` — the slots whose granted value differs from the
  requested value. `access.DiffBindings(requested, granted)` computes it (sorted by slot; unchanged
  slots omitted; empty when the operator approved unchanged). `GrantLatest` persists it.
- **Capture point.** `hApprovalResolve` computes the diff for a `KindPreset` approval —
  `requested = pending.Bindings`, `granted = body.Bindings` (the operator's final map; falls back to
  the requested map when the operator sent none) — and passes it to `GrantLatest`. Non-preset kinds
  record no edits.
- **Disclosure.** `mcpsvc.AccessResult` gains `operator_edits []access.BindingEdit`. On a **granted**
  result `accessStatus` reads the latest request; if it carries edits, it sets `operator_edits` and
  prepends a one-line human summary to `memo` (e.g. `operator narrowed workspace: * → ECOSENT`).
  Both `request-preset` (when it returns granted) and `get-access` surface it, since both return an
  `AccessResult` verbatim.

The persisted disclosure survives a daemon restart and repeated `get-access` calls (the in-memory
approval queue does not).

## Alternatives considered

- **Deliver the diff only through the approval-queue decision channel** (no persistence). Rejected:
  a `get-access` after a restart, or a second `get-access`, would lose it; persistence on the
  existing request row is the same mechanism already used for deny reasons.
- **Reconstruct requested-vs-granted from the policy rules at read time.** Rejected: the rules hold
  only the *granted* values; the *requested* values are not derivable from them.
- **Surface only a human string in `memo`.** Rejected: agents consume the JSON response
  programmatically; a structured `operator_edits` array is more useful, and the human `memo` line is
  kept as well for readability.
- **Also diff operator edits for non-preset kinds** (e.g. operation `trust_any` flips). Deferred:
  those edits have a different shape (per-variable value-set widening, not slot narrowing); the
  concrete need was preset slot narrowing. `DiffBindings` is generic and can be reused if needed.

## Consequences

- An agent whose preset request was narrowed by the operator now learns exactly what changed, from
  both `request-preset` and `get-access`, and can adjust (re-request, or scope its calls to the
  granted workspace) instead of hitting an unexplained data-plane deny.
- One additive schema change (`access_requests.edits`, migration `access_request_edits`); alpha, no
  compat burden.
- `GrantLatest` gains an `edits []BindingEdit` parameter (its single caller, the approval-resolve
  path, is updated).
- Covered by tests: `internal/access` (`DiffBindings`; `GrantLatest` round-trips edits),
  `internal/mcpsvc` (`get-access` surfaces `operator_edits` + the memo note on a granted result),
  `internal/store` (the `access_request_edits` migration upgrades an old DB).

Links: [ADR-0043](0043-getatts-read-workspace-relaxation.md) (the get-atts friction that the missing
disclosure compounded), [ADR-0037](0037-presets-first-class.md) (presets + operator-editable slots),
[ADR-0025](0025-k8s-access-path-single-approval-surface.md) (§C: `GrantLatest`/`DenyLatest` keep the
access-request row in sync with the card decision — the operator-decision log this extends).
</content>
