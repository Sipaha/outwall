# ADR-0017: `number` and `enum` operation-variable types

- **Status:** accepted
- **Date:** 2026-06-19

## Context

The operation-access model (ADR-0014) gave HTTP operation templates two typed placeholder kinds:
`text` (gated by an allowed-set that GROWS via require-approval) and `date` (auto-allowed, but the
extracted value must parse as a date in `Match`). Real APIs also expose **bounded numeric** path/query
parameters (`?limit=`, `/items/{id}`) and **fixed-domain enumerations** (`?sort=asc|desc`,
`?state=open|closed`). Modelling both as `text` is wrong on two counts: a numeric bound cannot be
expressed as an allowed-set, and an enumeration is a CLOSED domain — an out-of-domain value is an
error to reject outright, not a new value to queue for the operator to approve.

## Decision

Add two placeholder types to `internal/optemplate` and two matching value-policy behaviours to
`internal/policy`. Enforcement is unchanged in spirit: outwall parses the real request and gates the
extracted values; it never trusts a declaration.

- **`optemplate`** — new `VarType` constants `Number` (`"number"`) and `Enum` (`"enum"`). `Match`
  type-validates structurally via `typeValid`: a `number` segment/param must satisfy the new
  `IsNumber` (int or float via `strconv.ParseFloat`), exactly as `date` must satisfy `IsDate`; an
  `enum` extracts any value (its domain check is policy's job). A type-invalid value makes the request
  **not match** the template — it then falls through to default-deny, never a silent grant.
- **`policy.ValuePolicy`** — two new behaviours, keyed on `Type`:
  - `number`: `Mode "any"` (any number) or `Mode "range"` gating against `[Min,Max]` inclusive
    (`Min`/`Max` are `*float64`; a nil bound = unbounded on that side). An out-of-range value is a
    **hard deny**.
  - `enum`: `Mode "set"` gating against a CLOSED `Values` set. A value not in the set is a **hard
    deny** — the set does NOT auto-grow.
  `evalHTTPRule` returns the candidate with `Outcome = Deny` immediately on an enum/number violation,
  so most-restrictive tier resolution denies it. `text` is unchanged (unknown value →
  require-approval, set grows).
- **MCP / approval** — `request_access` already validates the template via `optemplate.Parse`, which
  now accepts the new types, so no MCP code change is needed. The approval-resolve seeding
  (`daemon.approveOperation`) seeds a `number` var as `any` (the operator tightens it to a range in
  the UI) and seeds `enum`/`text` as a `set` from the requested value; on extend it adds approved
  values to text/enum sets but leaves number ranges operator-managed.
- **UI** — the Operations screen (`Rules.tsx`) gains a `NumberRangeEditor` (min/max or "any number")
  and an `EnumSetEditor` (closed-set chips with an explicit "out-of-set → denied" note, and no
  trust-any toggle).

## Alternatives considered

- **Model enum as text + a closed flag.** Rejected: the grow-on-approval vs hard-deny distinction is
  fundamental, and overloading `text` would force every call site to special-case the flag. A
  distinct type makes the closed semantics legible in the template, the MCP declaration, and the UI.
- **Validate enum membership in `optemplate.Match`.** Rejected: `optemplate` checks STRUCTURE and
  TYPE only; the allowed-set is mutable policy state and belongs in `policy` (same split as `text`).
- **Number range as a min/max pair of `text` values.** Rejected: cannot express inequality gating,
  and a string compare would mis-order numbers.

## Consequences

- HTTP operations can now express bounded numerics and closed enumerations precisely; an
  out-of-bounds or out-of-domain value is denied at the gateway without bothering the operator.
- The hard-deny-vs-approval asymmetry is now a property of the variable TYPE, recorded here and in the
  `policy` module doc — future variable types must declare which side they fall on.
- No storage migration: `ValuePolicy` gained optional `min`/`max` (omitempty); existing text/date
  policies deserialize unchanged. (Alpha — no released data regardless.)
- A future `regex`/`cidr`/etc. type follows the same recipe: add the `VarType`, a `typeValid` branch,
  and an `evalHTTPRule` gating branch deciding deny-vs-approval.
