package store

import (
	"database/sql"
	"fmt"
)

const schema = `
CREATE TABLE IF NOT EXISTS vault_meta (
	id       INTEGER PRIMARY KEY CHECK (id = 1),
	salt     BLOB NOT NULL,
	verifier BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS upstreams (
	id          TEXT PRIMARY KEY,
	name        TEXT NOT NULL UNIQUE,
	base_url    TEXT NOT NULL,
	kind        TEXT NOT NULL DEFAULT 'http',
	auth_type   TEXT NOT NULL,
	auth_config BLOB,
	created_at  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS agents (
	id           TEXT PRIMARY KEY,
	name         TEXT NOT NULL,
	token_sha256 TEXT NOT NULL UNIQUE,
	status       TEXT NOT NULL,
	created_at   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS rules (
	id                 TEXT PRIMARY KEY,
	subject_agent_id   TEXT NOT NULL DEFAULT '',
	upstream_id        TEXT NOT NULL,
	op_method          TEXT NOT NULL DEFAULT '',
	op_path_template   TEXT NOT NULL DEFAULT '',
	op_query_template  TEXT NOT NULL DEFAULT '{}',
	op_body_template   TEXT NOT NULL DEFAULT '{}',
	op_value_policies  TEXT NOT NULL DEFAULT '{}',
	outcome            TEXT NOT NULL,
	rate_limit_per_min INTEGER NOT NULL DEFAULT 0,
	k8s_namespace      TEXT NOT NULL DEFAULT '',
	k8s_resource       TEXT NOT NULL DEFAULT '',
	k8s_verb           TEXT NOT NULL DEFAULT '',
	created_at         TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS rules_by_upstream ON rules(upstream_id);
CREATE TABLE IF NOT EXISTS access_requests (
	id          TEXT PRIMARY KEY,
	agent_id    TEXT NOT NULL,
	upstream_id TEXT NOT NULL,
	purpose     TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL DEFAULT 'pending',   -- pending | granted | denied | dismissed
	created_at  TEXT NOT NULL,
	resolved_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS access_requests_by_status ON access_requests(status);
CREATE TABLE IF NOT EXISTS audit_log (
	id            TEXT PRIMARY KEY,
	ts            TEXT NOT NULL,
	agent_id      TEXT NOT NULL DEFAULT '',
	agent_name    TEXT NOT NULL DEFAULT '',
	upstream_id   TEXT NOT NULL DEFAULT '',
	upstream_name TEXT NOT NULL DEFAULT '',
	method        TEXT NOT NULL DEFAULT '',
	path          TEXT NOT NULL DEFAULT '',
	query         TEXT NOT NULL DEFAULT '',
	status_code   INTEGER NOT NULL DEFAULT 0,
	duration_ms   INTEGER NOT NULL DEFAULT 0,
	req_bytes     INTEGER NOT NULL DEFAULT 0,
	resp_bytes    INTEGER NOT NULL DEFAULT 0,
	decision      TEXT NOT NULL DEFAULT '',
	rule_id       TEXT NOT NULL DEFAULT '',
	operation     TEXT NOT NULL DEFAULT '',   -- matched operation path-template (http)
	vars_json     TEXT NOT NULL DEFAULT '',   -- extracted variable values (http), JSON object
	headers_json  TEXT NOT NULL DEFAULT '',
	error         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS audit_log_by_ts ON audit_log(ts);
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS audit_bodies (
	log_id       TEXT NOT NULL,
	kind         TEXT NOT NULL,                 -- 'request' | 'response'
	content_type TEXT NOT NULL DEFAULT '',
	size         INTEGER NOT NULL DEFAULT 0,    -- total declared/observed size
	sha256       TEXT NOT NULL DEFAULT '',
	truncated    INTEGER NOT NULL DEFAULT 0,
	stored       BLOB,                          -- NULL when non-text / not stored
	PRIMARY KEY (log_id, kind)
);
`

// sqlExecQuerier is the subset of *sql.DB / *sql.Tx the migration helpers use, so a step can run
// its statements inside the migration transaction.
type sqlExecQuerier interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
}

// migration is one ordered, run-once schema step. Its 1-based position in `migrations` is its
// version; `up` runs inside a transaction and may do anything SQL can (create/alter/rename/backfill).
type migration struct {
	name string
	up   func(tx *sql.Tx) error
}

// migrations is the ordered list of schema steps. The runner applies every step whose version is
// greater than the database's current `PRAGMA user_version`, each in its own transaction, bumping
// user_version on success — so a step runs exactly once per database, fresh or upgraded.
//
// Step 1 is the **baseline**: the full current `schema` (idempotent `CREATE TABLE IF NOT EXISTS`)
// plus the additive `ensureColumns` reconcile (ADR-0022). On a fresh DB it builds everything; on a
// pre-versioning DB (user_version still 0) it is a safe no-op/reconcile and stamps it to version 1.
// Append later **structural** changes (renames, data backfills, drops — things `ensureColumns`
// cannot express) as new entries; never edit or reorder a released entry.
var migrations = []migration{
	{"baseline", func(tx *sql.Tx) error {
		if _, err := tx.Exec(schema); err != nil {
			return fmt.Errorf("baseline schema: %w", err)
		}
		return ensureColumns(tx)
	}},
}

// migrate brings db up to len(migrations) by running each pending step (version > user_version) in a
// transaction and stamping user_version. A failed step rolls back and leaves the version unchanged.
func migrate(db *sql.DB) error {
	var ver int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	for i := ver; i < len(migrations); i++ {
		m := migrations[i]
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d (%s): %w", i+1, m.name, err)
		}
		if err := m.up(tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d (%s): %w", i+1, m.name, err)
		}
		// user_version takes no bound parameter; i+1 is an int we control (no injection).
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("stamp version %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d (%s): %w", i+1, m.name, err)
		}
	}
	return nil
}

// additiveColumns are columns that were ADDED to existing tables in later builds. On a database
// created by an older build, ensureColumns adds any that are missing (idempotent
// `ALTER TABLE … ADD COLUMN`), so a purely-additive schema change no longer forces a one-time DB
// reset.
//
// SCOPE: additions only. A column REMOVAL or a semantic model change — e.g. the path-glob →
// operation rule model that dropped `method`/`path_glob` for `op_*` (ADR-0014) — is NOT migratable
// this way and still needs a one-time reset in alpha (full migrations are a Beta item). Only list a
// column here when its absence is a pure addition to an otherwise-current table; every entry MUST
// also exist in `schema` above (TestSchemaCoversAdditiveColumns enforces this), and the DDL must
// carry a DEFAULT (SQLite requires one to ADD a NOT NULL column to a non-empty table).
var additiveColumns = []struct{ table, name, ddl string }{
	{"audit_log", "operation", "TEXT NOT NULL DEFAULT ''"},
	{"audit_log", "vars_json", "TEXT NOT NULL DEFAULT ''"},
	{"rules", "op_body_template", "TEXT NOT NULL DEFAULT '{}'"},
	{"upstreams", "kind", "TEXT NOT NULL DEFAULT 'http'"},
}

// ensureColumns brings an older database's tables up to date by adding any missing additive column.
// It is idempotent: a column that already exists is skipped, so a fresh DB (just built from
// `schema`) is a no-op.
func ensureColumns(q sqlExecQuerier) error {
	for _, c := range additiveColumns {
		has, err := columnExists(q, c.table, c.name)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		// Table/column names here are hardcoded constants, not user input — no injection surface
		// (and PRAGMA / ALTER do not accept bound parameters for identifiers anyway).
		if _, err := q.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", c.table, c.name, c.ddl)); err != nil {
			return fmt.Errorf("add column %s.%s: %w", c.table, c.name, err)
		}
	}
	return nil
}

// columnExists reports whether table has a column named column (via PRAGMA table_info).
func columnExists(q sqlExecQuerier, table, column string) (bool, error) {
	rows, err := q.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, fmt.Errorf("table_info %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, ctype      string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
