# ADR-0038: Atomic rule fan-out via transactional `CreateMany`

- **Status:** accepted
- **Date:** 2026-06-23

## Context

Two approval paths in the daemon expand a single operator decision into many `policy.Rule` rows:

- **ADR-0037 (preset fan-out):** `approvePreset` iterates over the `[]RuleTemplate` returned by
  `preset.Build` and calls `d.policy.Create(...)` for each template in a loop.
- **ADR-0029 (k8s multi-grant fan-out):** `approveK8sAccess` iterates over the missing
  `(namespace, resource, verb)` tuples and calls `d.policy.Create(...)` for each.

ADR-0037 named this a known limitation: if a `Create` call fails mid-loop (rare DB-level error,
since templates are pre-validated), rules inserted before the failure are already committed and
the partial grant is left in place. The error is surfaced — the approval card stays unresolved and
`GrantLatest` is not called — but the persistent partial state is inconsistent. "Under-grant only,
never over-grant" was accepted as a short-term justification; making fan-out truly atomic was
deferred as a cross-cutting improvement.

A second structural hazard also existed: the 18-column INSERT statement was duplicated across
`Create` and any future fan-out path, creating a column-order-drift hazard (adding a column in
the schema and forgetting to add it in the INSERT is a silent correctness bug that only surfaces
at runtime).

This ADR resolves both issues in one change.

## Decision

### `rowExecutor` interface

A minimal private interface is added to `internal/policy/registry.go`:

```go
// rowExecutor is the subset of *sql.DB / *sql.Tx that insertRule needs, so a single
// insert helper serves both the autocommit Create path and the transactional CreateMany path.
type rowExecutor interface {
    Exec(query string, args ...any) (sql.Result, error)
}
```

Both `*sql.DB` and `*sql.Tx` satisfy this interface without any adapter.

### `insertRule` helper

The validation, marshalling, and 18-column INSERT body that lived inside `Create` are extracted
into a package-private function:

```go
func insertRule(exec rowExecutor, in Rule) (Rule, error)
```

`insertRule` assigns `in.ID` and `in.CreatedAt`, validates the outcome and rate-limit, marshals
the JSON side-fields, and calls `exec.Exec(INSERT ...)`. The 18-column INSERT now lives in
exactly one place, eliminating the column-order-drift hazard.

### `Create` — unchanged behavior, delegates to `insertRule`

```go
func (r *Registry) Create(in Rule) (*Rule, error) {
    out, err := insertRule(r.store.DB(), in)
    if err != nil {
        return nil, err
    }
    return &out, nil
}
```

`Create`'s signature and semantics are unchanged. Callers outside the fan-out paths are unaffected.

### `CreateMany` — single-transaction batch insert

```go
func (r *Registry) CreateMany(ins []Rule) ([]Rule, error)
```

`CreateMany` opens a `*sql.Tx`, calls `insertRule(tx, rule)` for each rule, and either commits
(all rows written) or rolls back on the first per-rule error (no rows written). An empty `ins`
slice is a no-op — no transaction is opened.

On any per-rule validation or DB error the whole batch is rolled back. A partial grant is now
impossible: either all rules from a fan-out are committed together, or none are.

### Fan-out sites

Both fan-out handlers are updated to collect their rules into a `[]policy.Rule` and call
`d.policy.CreateMany(rules)` once:

- **`approvePreset`** (ADR-0037): maps each `RuleTemplate` to a `policy.Rule` (setting
  `SubjectAgentID` and `UpstreamID` as before) and calls `CreateMany` once.
- **`approveK8sAccess`** (ADR-0029): collects the missing `(namespace, resource, verb)` tuples
  into a slice (keeping the existing `exists` idempotency filter) and calls `CreateMany` once. An
  empty missing-set returns early with no DB round-trip.

This supersedes the "Fan-out atomicity (known limitation)" note in ADR-0037.

## Alternatives considered

- **Keep the per-rule `Create` loop, wrap in an application-level rollback (delete rules on
  error).** Rejected: compensation logic is complex, races with concurrent reads, and is harder
  to test than a DB transaction. `database/sql` transactions are the correct primitive.
- **Make `Create` itself transactional (single-row tx).** Rejected: single-row autocommit inserts
  do not need a transaction; wrapping each `Create` in its own transaction would add overhead on
  the single-rule path for no benefit.
- **Add `CreateMany` to the `Policy` interface** used by the daemon. Done: the daemon's `policy`
  field is typed as the concrete `*policy.Registry`, so no separate interface change was needed.
  If an interface is extracted in the future it should include `CreateMany`.

## Consequences

- Fan-out from both `approvePreset` and `approveK8sAccess` is now all-or-nothing: a mid-batch
  DB failure leaves no partial grant. The approval card stays unresolved and the operator can
  retry or reject.
- The 18-column INSERT lives in exactly one place (`insertRule`). Adding a new `Rule` column
  requires changing only `insertRule` (plus the schema migration) — no silent drift risk.
- `Create` behavior is unchanged; existing callers see no difference.
- `CreateMany` is tested at two levels: a unit test in `internal/policy` (DB-level atomic rollback
  on a bad-outcome rule in a batch) and an integration test through `approvePreset` via a fake
  profile whose `Build` returns one valid rule followed by one with an invalid outcome.
- The `rowExecutor` interface is unexported; it is an implementation detail, not a public
  contract.

Links: [ADR-0037](0037-presets-first-class.md) (preset fan-out — known limitation now resolved),
[ADR-0029](0029-request-k8s-access-multi-grant.md) (k8s multi-grant fan-out — the second call site
now made atomic).
