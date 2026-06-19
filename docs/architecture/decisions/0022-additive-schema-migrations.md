# ADR-0022: Idempotent additive schema migrations

- **Status:** accepted
- **Date:** 2026-06-19

## Context

The store applied its schema with `CREATE TABLE IF NOT EXISTS` and had no per-column migration. That
is fine for a brand-new DB, but a database created by an older build keeps its old table shape: when
a later build adds a column and queries it, SQLite fails with `no such column: …` at runtime (seen
in the field: `query rules: no such column: op_method`, `query audit_log: no such column:
operation`). The alpha rule was "just reset the DB", but that wipes the operator's vault, hosts,
agents, and audit on every additive change — recurring, avoidable pain for changes that are pure
additions.

## Decision

Add an idempotent **additive** migration step. `store.migrate` now runs `ensureColumns` after
applying the schema: for each entry in a maintained `additiveColumns` list `(table, name, ddl)`, it
checks `PRAGMA table_info` and runs `ALTER TABLE <table> ADD COLUMN <name> <ddl>` only when the
column is absent. A fresh DB (just built from `schema`) finds nothing missing — a no-op.

Scope and guard-rails:
- **Additions only.** A column removal or a semantic model change is NOT handled here. The path-glob
  → operation rule model (ADR-0014) dropped `method`/`path_glob` for `op_*`; "adding" the `op_*`
  columns to an old path-glob table would leave dead columns and a half-broken table, worse than a
  clean error. Such breaks still require a one-time reset in alpha; full data migrations are a Beta
  item.
- Every `additiveColumns` entry must also exist in `schema`, and its DDL must carry a `DEFAULT`
  (SQLite requires one to add a NOT NULL column to a non-empty table). `TestSchemaCoversAdditiveColumns`
  fails the build if an entry drifts out of `schema`; `TestEnsureColumnsUpgradesOldDB` exercises the
  upgrade path against a hand-built old DB.
- Identifiers in the `ALTER`/`PRAGMA` statements are hardcoded constants (PRAGMA/ALTER do not accept
  bound parameters for identifiers), so there is no injection surface.

Initial list: `audit_log.operation`, `audit_log.vars_json`, `rules.op_body_template`,
`upstreams.kind`. New additive columns are added to both `schema` and `additiveColumns`.

## Alternatives considered

- **Keep "reset on every schema change."** Rejected for additive changes — it needlessly destroys
  operator state (vault/hosts/agents/audit) when a safe in-place `ADD COLUMN` exists.
- **A full versioned migration framework (numbered up/down migrations).** Deferred to Beta — heavier
  than alpha needs, and most changes so far are pure additions that the lightweight step covers.
- **Derive the column list by parsing the `schema` string.** Rejected — parsing SQL is fiddlier and
  more failure-prone than an explicit list guarded by a self-consistency test.

## Consequences

- Additive schema growth (new columns) no longer forces a DB reset; operator data survives.
- Model-breaking changes (column removal/replacement, retyping) still need a one-time reset in alpha;
  this is documented at `additiveColumns` and here.
- The `additiveColumns` list is a small maintenance point, kept honest by
  `TestSchemaCoversAdditiveColumns`.
- The earlier alpha note "one-time DB reset is fine" now applies only to model breaks, not to every
  schema change.
