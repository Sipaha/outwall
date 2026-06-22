# ADR-0027: Additive columns are version-gated migration steps

- **Status:** accepted
- **Date:** 2026-06-22

## Context

The Agents screen (and any access-requests read) failed with
`query access requests: SQL logic error: no such column: reason`. The `reason` column was added to
`access_requests` for the deny-reason feature (ADR-0024) and correctly listed in `additiveColumns`
(ADR-0022), yet a running database lacked it.

Root cause, from the code: ADR-0023 introduced a versioned migration runner. The additive-column
reconcile (`ensureColumns`) was invoked **only inside migration step 1, the baseline**. The runner
executes a step only while `version > user_version`, so step 1 runs exactly once — when
`user_version` is 0. Sequence that breaks:

1. An older build (versioned runner present, but `additiveColumns` did not yet list `reason`) opens
   the DB → runs the baseline step → stamps `user_version = 1`.
2. `reason` is later appended to `additiveColumns` (ADR-0024).
3. A newer build opens the DB → `user_version` is already 1 → the baseline step never re-runs →
   the reconcile never runs again → `reason` is never added.

So appending to `additiveColumns` had no effect on any database already past version 1 — silently
breaking the ADR-0022 "just append a column" contract.

Why a naive single version-gated `ALTER … ADD COLUMN reason` step is not enough on its own: the
baseline migration applies the **current** `schema` (`CREATE TABLE IF NOT EXISTS`), so a *fresh* DB
already has `reason` after step 1 — an unguarded later step would fail with "duplicate column".
Historically the baseline has also been a *moving* snapshot (always current), so different old DBs
hold different column subsets at the same `user_version`; an unguarded step can't assume a uniform
starting shape.

## Decision

Each additive column is its **own run-once, version-gated migration step**, generated from
`additiveColumns` by `buildMigrations`. The step does a **guarded** `ALTER TABLE … ADD COLUMN`
(`addColumnIfMissing`): it adds the column only when absent, so it is a no-op on a fresh DB (whose
baseline already created it) and on an old DB that already carries it, and it *adds* the column on a
DB that is missing it. Because it is a normal step, the runner applies it once (when
`version > user_version`) and stamps the new version — no work on subsequent opens.

- `additiveColumns` becomes **append-only**: an entry's position is its migration version, so entries
  are never reordered or deleted (a new column goes at the end). `TestSchemaCoversAdditiveColumns`
  still enforces every listed column exists in `schema`.
- The baseline step no longer calls the reconcile; the per-column steps own that.
- Structural changes (renames/backfills/drops) remain explicit steps appended after the generated
  additive steps. The "never edit or reorder a released step" rule now covers `additiveColumns` too.

## Alternatives considered

- **Run the reconcile on every Open, independent of version.** Rejected — it works and is idempotent,
  but it sidesteps the version mechanism ADR-0023 exists for (re-scanning `PRAGMA table_info` for
  every additive column on every Open) and reads as a workaround rather than a migration. The
  version-gated step runs once and is the idiomatic shape.
- **Unguarded `ADD COLUMN` steps with a frozen (original) baseline.** Rejected for now — it is the
  textbook form, but it requires reconstructing the original baseline schema and re-homing the
  current `schema` const, a larger change; and the historically-moving baseline means some old DBs
  already hold a column an unguarded step would re-add. The guard makes the step safe against every
  existing DB shape with far less churn. (Freezing the baseline remains a possible later cleanup.)
- **One-time DB reset (alpha).** Rejected — a purely-additive column must not require wiping operator
  data; avoiding that is the whole point of ADR-0022.

## Consequences

- Appending to `additiveColumns` now reliably reaches every database on its next Open, fresh or
  long-lived, via a one-time version-gated step — the ADR-0022 contract holds again. `reason` (and
  any future additive column) self-heals on restart; no manual `ALTER` / reset.
- No per-Open reconcile work; each column-add runs exactly once and bumps `user_version`.
- `TestMigrationAddsColumnToDBStampedAtEarlierVersion` pins the regression: a DB stamped at an
  earlier version but missing a later additive column gets it (and is stamped current) on Open.
