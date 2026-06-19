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

**Migrations (ADR-0022).** `migrate` first applies `schema` (`CREATE TABLE IF NOT EXISTS`), then
`ensureColumns` adds any missing **additive** column from `additiveColumns` via idempotent
`ALTER TABLE … ADD COLUMN`, so a purely-additive schema growth no longer forces a one-time DB reset.
This handles ADDITIONS only — a column removal / semantic model change (e.g. path-glob → operation
rules, ADR-0014) is not migratable this way and still needs a reset in alpha; full migrations are a
Beta item. `TestSchemaCoversAdditiveColumns` enforces that every `additiveColumns` entry also exists
in `schema`.

## Public API

- `Open(path string) (*Store, error)` — open/create the DB and run migrations.
- `(*Store).DB() *sql.DB` — the underlying handle for other packages.
- `(*Store).GetSetting(key) (string, bool, error)` / `SetSetting(key, value) error` — settings KV.
- `(*Store).Close() error`.
