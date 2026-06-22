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
	reason      TEXT NOT NULL DEFAULT '',          -- operator's deny reason, surfaced to the agent
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

// migrations is the ordered list of schema steps. The runner applies every step whose version
// (its 1-based position) is greater than the database's `PRAGMA user_version`, each in its own
// transaction, bumping user_version on success — so a step runs exactly once per database, fresh
// or upgraded.
//
//   - Step 1 is the **baseline**: the full current `schema` (idempotent `CREATE TABLE IF NOT
//     EXISTS`). A fresh DB gets every table+column here; an old DB only gets tables it was missing.
//   - Each later **additive column** is its own subsequent step, built from `additiveColumns`
//     (ADR-0022 / ADR-0027). It is a *guarded* ADD COLUMN — a no-op when the column is already
//     present (e.g. a fresh DB whose baseline just created it, or an old DB already carrying it).
//     The guard is needed because the baseline is the *current* schema, so a fresh DB already has
//     the column; it also tolerates the historically-moving baseline (different old DBs hold
//     different column subsets at the same version).
//
// Append later **structural** changes (renames, backfills, drops) as new explicit entries after the
// generated additive steps. **Never edit or reorder a released step** — its position is its version.
var migrations = buildMigrations()

func buildMigrations() []migration {
	migs := []migration{
		{"baseline", func(tx *sql.Tx) error {
			if _, err := tx.Exec(schema); err != nil {
				return fmt.Errorf("baseline schema: %w", err)
			}
			return nil
		}},
	}
	for _, c := range additiveColumns {
		c := c // capture per-iteration for the closure
		migs = append(migs, migration{
			name: "add " + c.table + "." + c.name,
			up:   func(tx *sql.Tx) error { return addColumnIfMissing(tx, c.table, c.name, c.ddl) },
		})
	}
	return migs
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

// additiveColumns are columns ADDED to existing tables after the baseline. buildMigrations turns
// each into its own run-once, version-gated migration step (a guarded ADD COLUMN), so a
// purely-additive schema change reaches every database — fresh or long-lived — without a reset.
//
// APPEND-ONLY: an entry's position fixes its migration version, so never reorder or delete one
// (that would shift versions and replay the wrong steps). Add new additive columns to the END.
//
// SCOPE: additions only. A column REMOVAL or a semantic model change — e.g. the path-glob →
// operation rule model that dropped `method`/`path_glob` for `op_*` (ADR-0014) — is NOT expressed
// here; add an explicit structural migration step (and in alpha a one-time reset may still be
// needed). Every entry MUST also exist in `schema` above (TestSchemaCoversAdditiveColumns enforces
// this), and the DDL must carry a DEFAULT (SQLite requires one to ADD a NOT NULL column to a
// non-empty table).
var additiveColumns = []struct{ table, name, ddl string }{
	{"audit_log", "operation", "TEXT NOT NULL DEFAULT ''"},
	{"audit_log", "vars_json", "TEXT NOT NULL DEFAULT ''"},
	{"rules", "op_body_template", "TEXT NOT NULL DEFAULT '{}'"},
	{"upstreams", "kind", "TEXT NOT NULL DEFAULT 'http'"},
	{"access_requests", "reason", "TEXT NOT NULL DEFAULT ''"},
}

// addColumnIfMissing adds one column via ALTER TABLE … ADD COLUMN, but only if it is absent — so the
// step is a no-op on a database whose baseline already created the column (a fresh DB) or that an
// older build already carries it. Table/column names are hardcoded constants, not user input — no
// injection surface (and ALTER / PRAGMA do not accept bound parameters for identifiers anyway).
func addColumnIfMissing(q sqlExecQuerier, table, name, ddl string) error {
	has, err := columnExists(q, table, name)
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	if _, err := q.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, name, ddl)); err != nil {
		return fmt.Errorf("add column %s.%s: %w", table, name, err)
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
