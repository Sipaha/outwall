# ADR-0023: Versioned migration runner

- **Status:** accepted
- **Date:** 2026-06-19

## Context

ADR-0022 added an idempotent *additive* migration (`ensureColumns` → `ALTER TABLE ADD COLUMN`),
which covers new columns but not structural changes — renames, data backfills, table restructures,
drops. As the schema keeps evolving past alpha we need a general, run-once, ordered migration
mechanism that can express those, without re-running steps or losing operator data.

## Decision

Drive schema evolution from SQLite's built-in `PRAGMA user_version` and an ordered list of
run-once steps.

- `migrations []migration` where `migration{ name string; up func(*sql.Tx) error }`; the 1-based
  index is the version. `migrate(db)` reads `PRAGMA user_version`, then for every step with version
  greater than it: runs `up` in a transaction, sets `PRAGMA user_version = N`, and commits. A failed
  step rolls back (DDL is transactional in SQLite) and leaves the version unchanged, so the next
  start retries from the same point. Each step therefore runs **exactly once** per database.
- **Step 1 is the baseline**: it execs the full current `schema` (idempotent
  `CREATE TABLE IF NOT EXISTS`) and runs the ADR-0022 `ensureColumns` reconcile. On a fresh DB it
  builds everything and stamps version 1; on a pre-versioning DB (`user_version` still 0, e.g. an
  additive-era DB) it is a safe no-op/reconcile that stamps it to version 1 — so existing databases
  adopt the runner transparently on first start with this build.
- **Future structural changes are appended** as steps 2, 3, … Released steps are never edited or
  reordered (that would corrupt already-migrated databases). `up` takes the migration `*sql.Tx`, so
  it can run any SQL — including the 12-step table rebuild SQLite uses for column drops/renames with
  data preserved.

`ensureColumns`/`columnExists` were generalized to a small `sqlExecQuerier` interface satisfied by
both `*sql.DB` and `*sql.Tx`, so the baseline step runs them inside its transaction.

## Alternatives considered

- **Stay on `ensureColumns` only (ADR-0022).** Rejected — it cannot express renames, backfills, or
  drops; only column additions.
- **A `schema_version` table instead of `PRAGMA user_version`.** Rejected — `user_version` is a
  built-in header integer (no table, no bootstrapping/ordering concerns) and is the idiomatic SQLite
  approach.
- **Pure-migrations with no baseline `schema` const (fresh DB replays every historical step).**
  Rejected for now — keeping one readable `schema` for the current shape is clearer for new
  contributors than reconstructing it from a chain of steps; the baseline step folds the const in.
- **A third-party migration library (golang-migrate, goose).** Rejected — a ~40-line runner over
  `user_version` has no new dependency and exactly fits the single-file, single-writer SQLite store.

## Consequences

- Schema can now evolve with renames/backfills/drops, run-once and transactional, without a DB
  reset and without losing operator data.
- Adding a change is: append a `migration{}` (and, for the current-shape readability, also update
  `schema`); never touch a released step. `TestMigrationRunnerAppliesPendingOnce` covers the
  run-once/stamp/no-replay contract; `TestSchemaCoversAdditiveColumns` keeps the additive list and
  `schema` in sync.
- ADR-0022's additive reconcile lives on as the baseline step; the "reset on schema change" alpha
  note now applies to neither additive nor structural changes — only a deliberately unmigrated break
  would still reset.
- A failed migration aborts startup (the daemon returns the error from `store.Open`) rather than
  running against a half-migrated DB — fail-closed.
