# ADR-0043: get-atts reads are workspace-relaxed under a concrete-workspace read grant

- **Status:** accepted
- **Date:** 2026-07-07

## Context

Using outwall to read a specific Citeck issue by its exact ref
(`emodel/ept-issue@ECOSENT-3588`) exposed a friction point in the workspace-gating model.

A Citeck **get-atts** read — a `POST /records/query` whose body carries `records: ["<ref>"]`
(load specific records by ref) rather than a `query` predicate — has **no derivable workspace**:
the ref is `appName/sourceId@localId`, which encodes no workspace. `classify` therefore tags each
such resource with `scopeUnknown` (records.go).

`matchWorkspace` treated a concrete rule workspace against `scopeUnknown` as **deny** ("cannot be
proven to be within a specific workspace", ADR-0039 kept get-atts "all-or-nothing"). The practical
result: an agent granted the very common `citeck-readonly` preset with a **concrete** workspace
(e.g. the operator narrows `workspace: * → ECOSENT` before approving) could **not load the exact
record it was pointed at** — `outwall request` with `records:[ref]` returned `403 access denied`.
The agent had to fall back to a predicate `query` with an explicit `workspaces:["ECOSENT"]` filter,
which is awkward when all it holds is the ref (the whole point of get-atts).

This is pure friction: to issue a get-atts you must already know the exact ref (itself a gated
capability), and a read is low-risk. The source gate (`source_id` glob) is the meaningful
protection; the workspace gate is unenforceable here because the workspace is not in the request.

## Decision

For a `scopeUnknown` resource, `matchWorkspace` is **workspace-relaxed for reads only**:

- **read** (`op.Kind == "read"`, i.e. get-atts by ref) → the concrete-workspace read grant
  authorizes it; only the `source_id` gate applies.
- **write** (`op.Kind == "write"`, i.e. mutate/delete by ref) → unchanged: a concrete-workspace
  write rule still does **not** match an unprovable workspace (deleting/mutating a record whose
  workspace cannot be proven stays conservative).

`scopeAll` (a predicate query with no `workspaces` filter — genuinely "all workspaces") is
**unchanged**: a concrete rule never matches it, for reads or writes. A `*`/empty rule workspace
still matches anything, as before.

Mechanically, `matchWorkspace(ruleWs, scope)` gains an `opKind` parameter; the `scopeUnknown` case
returns `opKind == "read"`. The two callers (`ruleMatches`, `wsAllowedForRead`) pass the operation
kind / `"read"` (the filter path only ever sees concrete workspaces, so the added parameter is
behaviour-preserving there).

## Alternatives considered

- **Relax `scopeUnknown` for writes too** (align with the RuleSchema label "not enforced for
  update/delete"). Rejected: a concrete-workspace write grant that authorized mutate/delete of *any*
  record by ref is a real cross-workspace write widening; the user asked only for get-atts. The
  label is clarified to reflect that concrete-workspace writes-by-ref stay denied.
- **A dedicated `citeck-getatts` preset / separate grant.** Rejected: over-engineered for what is a
  natural part of read access; the existing read grant should cover reading a record it was given.
- **Relax `scopeAll` for reads.** Rejected: `scopeAll` means "span every workspace", which a
  concrete grant must not silently authorize — that is exactly what ADR-0039's browser narrowing and
  the API `403` protect.
- **Keep the strict deny; make the agent always query with explicit `workspaces`.** Rejected: the
  agent frequently holds only a ref (from a link the user pasted) and cannot know the workspace; the
  strict deny turned a legitimate read into a dead end.

## Consequences

- An agent with a concrete-workspace `citeck-readonly` (or `citeck-readwrite`) grant can load any
  record **by exact ref** whose source matches, regardless of that record's workspace. This is a
  deliberate, source-gated cross-workspace **read** widening for get-atts. Writes are unaffected.
- Supersedes the "get-atts reads are unchanged (all-or-nothing)" note in ADR-0039 for the
  concrete-workspace case; predicate-query narrowing (ADR-0039) is untouched.
- Covered by `internal/serverprofile/citeck` tests: get-atts read allowed by a concrete-workspace
  read rule (source gate still enforced); write-by-ref still denied by a concrete-workspace write
  rule; existing scopeAll / wildcard / predicate-query cases unchanged.

Links: [ADR-0039](0039-workspace-filtering-and-allow-any.md) (mode-aware read filtering; the
get-atts "all-or-nothing" note this refines), [ADR-0034](0034-server-profiles-and-citeck-plugin.md)
(server-profile plugin + Records classification; core-stays-citeck-free boundary),
[ADR-0037](0037-presets-first-class.md) (the citeck read/write presets whose concrete-workspace
grants this affects).
</content>
</invoke>
