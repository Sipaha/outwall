# ADR-0027: Current `schema` for fresh DBs + forward-only migration steps for upgrades

- **Status:** accepted
- **Date:** 2026-06-22
- **Supersedes:** the additive-column reconcile mechanism of ADR-0022 (the versioned runner of
  ADR-0023 is kept and clarified).

## Context

The Agents screen failed with `query access requests: SQL logic error: no such column: reason`.
`reason` was added to `access_requests` for deny-reason (ADR-0024) and listed in `additiveColumns`
(ADR-0022), yet a running database lacked it.

Root cause: the additive-column reconcile (`ensureColumns`) ran **only inside migration step 1, the
baseline**, and the versioned runner (ADR-0023) executes step 1 only while `user_version == 0`. Once
an older build had stamped the DB at version 1, appending a column to `additiveColumns` never reached
it again. So the ADR-0022 "just append a column" contract was silently broken for any DB past v1.

The deeper rot was the **moving baseline**: the baseline migration applied the *current* full
`schema` with `CREATE TABLE IF NOT EXISTS`, so "version 1" meant different table shapes for different
databases depending on when they were first opened. That ambiguity is what forced the
guard/reconcile hybrid in the first place.

A first fix ran the reconcile on every Open; a second made each additive column a guarded
version-gated step. Both worked but kept the guard hybrid alive. The databases here are alpha and
disposable (a "clean slate"), so we fix the model properly instead.

## Decision

Adopt the standard declarative-schema + forward-migrations model:

- **`schema` is always the CURRENT shape.** A brand-new (empty) database is created **directly from
  `schema`** and stamped at the latest version. It therefore **never runs the upgrade steps** — they
  exist only to bring forward databases created by an earlier version. `migrate` detects "empty" via
  `sqlite_master` (no user tables).
- **`migrations` is a forward-only list of upgrade steps.** Version 1 is the baseline (`schema`
  itself); each later entry is an explicit `ALTER`/backfill/rename that advances an old database by
  one version, run once and version-stamped, in its own transaction. Never edit or reorder a
  released step.
- **Changing the schema = two edits:** (1) update `schema` to the new current shape, and (2) append a
  migration step performing the same change via `ALTER` for existing databases. A fresh DB gets the
  change from `schema` (and skips the step); an existing DB gets it from the step. No double-apply,
  no guards.
- The `additiveColumns` list, `ensureColumns`, and the per-column guard are **removed**.

Because the previous baseline was a moving target, pre-existing alpha databases can hold an
inconsistent column set at a given version; those are **reset once** (alpha permits it) rather than
carried forward. From this baseline on, the version → schema mapping is unambiguous.

## Alternatives considered

- **Reconcile additive columns on every Open / guarded version-gated steps.** Both rejected as the
  guard hybrid we are removing: they only existed to paper over the moving baseline. With a current
  `schema` for fresh DBs and forward steps for upgrades, neither is needed.
- **Freeze `schema` as the immutable v1 snapshot and express *everything* (even current shape) only
  through steps.** Rejected — it makes `schema` drift from "what the DB looks like now", losing the
  single readable source of truth. Keeping `schema` current and skipping steps on fresh DBs gives the
  same correctness with a readable schema.
- **Carry the inconsistent legacy DBs forward.** Rejected — these are alpha/disposable; a one-time
  reset is cheaper and cleaner than encoding every historical baseline shape.

## Consequences

- One readable, always-current `schema`; fresh installs build from it and are stamped at the latest
  version. Existing installs upgrade via explicit, run-once, version-gated steps. No per-Open work,
  no guards, no `additiveColumns` indirection.
- The discipline is explicit: a schema change is two edits (schema + a step). Forgetting the step
  only affects already-existing databases — caught the same way any missing migration is.
- Pre-existing alpha DBs are reset once to land on the unambiguous v1 baseline (the column the bug
  removed, `access_requests.reason`, is present in fresh DBs — pinned by `TestFreshDBFromSchema`).
- `TestFreshDBSkipsUpgradeSteps` pins the core property: a fresh DB is built from `schema` and does
  not run the upgrade steps.
