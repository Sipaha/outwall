# ADR-0027: Additive-column reconcile runs on every Open, not only at baseline

- **Status:** accepted
- **Date:** 2026-06-22

## Context

The Agents screen (and any access-requests read) failed with
`query access requests: SQL logic error: no such column: reason`. The `reason` column was added to
`access_requests` for the deny-reason feature (ADR-0024) and correctly listed in `additiveColumns`
(ADR-0022), yet a running database lacked it.

Root cause, from the code: ADR-0023 introduced a versioned migration runner. `ensureColumns` (the
idempotent additive-column reconcile from ADR-0022) was invoked **only inside migration step 1, the
baseline**. The runner executes a step only while `version > user_version`, so step 1 runs exactly
once — when `user_version` is 0. Sequence that breaks:

1. An older build (versioned runner present, but `additiveColumns` did not yet list `reason`) opens
   the DB → runs the baseline step → stamps `user_version = 1`.
2. `reason` is later appended to `additiveColumns` (ADR-0024).
3. A newer build opens the DB → `user_version` is already 1 → the baseline step never re-runs →
   `ensureColumns` never runs again → `reason` is never added.

This silently broke the documented contract "a purely-additive column just gets appended to
`additiveColumns`" (ADR-0022): appending has no effect on any database already past version 1.

## Decision

`migrate` runs `ensureColumns(db)` **unconditionally after the version-stamped step loop**, on every
Open, independent of `user_version`. `ensureColumns` is idempotent (it checks `columnExists` before
each `ALTER TABLE … ADD COLUMN`), so once every additive column is present it is a cheap no-op.

The baseline step still calls `ensureColumns` too (a released step is never edited — ADR-0023); that
call is now redundant but harmless. Structural migrations (renames, backfills, drops) remain
version-stamped steps that run once; only the **additive** reconcile is version-independent, matching
how additive columns are declared (a list, not a numbered step).

## Alternatives considered

- **Add a new version-stamped migration step per added column.** Rejected — defeats the point of the
  `additiveColumns` list (ADR-0022); every additive change would need a bespoke step, and the list
  would drift from the steps. The additive list exists precisely to avoid that ceremony.
- **Bump a single "additive epoch" version whenever the list changes.** Rejected — brittle (easy to
  forget the bump; that is exactly how `reason` was lost) and offers nothing over an idempotent
  reconcile that simply always runs.
- **One-time DB reset (alpha).** Rejected — a reset is acceptable for a model-breaking change, but a
  purely-additive column must not require wiping operator data; that is the whole promise of ADR-0022.

## Consequences

- Appending to `additiveColumns` now reliably reaches every database on its next Open, fresh or long-
  lived — the ADR-0022 contract holds again. The `reason` column (and any future additive column)
  self-heals on restart; no manual `ALTER`/reset.
- One extra set of cheap `PRAGMA table_info` checks per Open (one per additive column) — negligible.
- `TestEnsureColumnsReconcilesAtCurrentVersion` pins the regression: a DB stamped at the baseline but
  missing a later additive column gets it on Open.
