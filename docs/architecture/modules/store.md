# module: internal/store

The SQLite persistence layer. Opens (or creates) the database via the pure-Go
`modernc.org/sqlite` driver (no CGO), enables WAL + foreign keys, caps the pool to a
single connection (single writer), and applies the schema migrations idempotently.

Schema tables: `vault_meta`, `upstreams`, `agents`, `grants`.

## Public API

- `Open(path string) (*Store, error)` — open/create the DB and run migrations.
- `(*Store).DB() *sql.DB` — the underlying handle for other packages.
- `(*Store).Close() error`.
