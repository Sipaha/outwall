# module: internal/store

The SQLite persistence layer. Opens (or creates) the database via the pure-Go
`modernc.org/sqlite` driver (no CGO), enables WAL + foreign keys, caps the pool to a
single connection (single writer), and applies the schema migrations idempotently.

Schema tables: `vault_meta`, `upstreams`, `agents`, `rules`, `access_requests` (the Plan-1
`grants` table was dropped in Plan 2 — see ADR-0002). `access_requests` is the MCP
control-plane's intent log (added in Plan 3 — see ADR-0003): `(id, agent_id, upstream_id,
purpose, status, created_at, resolved_at)` with `status ∈ {pending,granted,denied,dismissed}`
and an index on `status`. Plan 4 added the audit journal (see ADR-0004): `audit_log`
(`id, ts, agent_id/name, upstream_id/name, method, path, query, status_code, duration_ms,
req_bytes, resp_bytes, decision, rule_id, headers_json, error`, indexed by `ts`) and
`audit_bodies` (`log_id, kind, content_type, size, sha256, truncated, stored`, keyed by
`(log_id, kind)`) — bodies live in their own table so the journal lists without reading blobs.
A `settings(key, value)` KV table backs operator settings (e.g. audit retention — ADR-0018).
`agents` also carries a nullable `last_seen_at` (added by the `agent_last_seen` migration step),
best-effort touched by `agent.Registry.Authenticate` on every resolved token.

**Migrations (ADR-0027, on the ADR-0023 runner).** `schema` is always the **current** shape, and
`migrations` is a forward-only list of run-once upgrade steps (version = 1-based position; step 1 is
the baseline = `schema`). `migrate` reads `PRAGMA user_version`: a **fresh** (empty) database is built
straight from `schema` and stamped at the latest version, so it **never runs the upgrade steps**
(they exist only to advance OLD databases); an **existing** database runs each pending step
(version > `user_version`) in a transaction, stamping on success (a failed step rolls back and aborts
`Open` — fail-closed). Changing the schema = two edits: update `schema`, AND append a step doing the
same via `ALTER` for existing DBs; never edit or reorder a released step. `TestFreshDBSkipsUpgradeSteps`
pins that fresh DBs skip steps; `TestMigrationRunnerAppliesPendingOnce` covers the
run-once/stamp/no-replay contract.

## Public API

- `Open(path string) (*Store, error)` — open/create the DB and run migrations.
- `(*Store).DB() *sql.DB` — the underlying handle for other packages.
- `(*Store).GetSetting(key) (string, bool, error)` / `SetSetting(key, value) error` — settings KV.
- `(*Store).Close() error`.
