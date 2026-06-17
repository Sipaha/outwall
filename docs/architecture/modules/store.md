# module: internal/store

The SQLite persistence layer. Opens (or creates) the database via the pure-Go
`modernc.org/sqlite` driver (no CGO), enables WAL + foreign keys, caps the pool to a
single connection (single writer), and applies the schema migrations idempotently.

Schema tables: `vault_meta`, `upstreams`, `agents`, `rules`, `access_requests` (the Plan-1
`grants` table was dropped in Plan 2 — see ADR-0002). `access_requests` is the MCP
control-plane's intent log (added in Plan 3 — see ADR-0003): `(id, agent_id, upstream_id,
purpose, status, created_at, resolved_at)` with `status ∈ {pending,granted,denied,dismissed}`
and an index on `status`.

## Public API

- `Open(path string) (*Store, error)` — open/create the DB and run migrations.
- `(*Store).DB() *sql.DB` — the underlying handle for other packages.
- `(*Store).Close() error`.
