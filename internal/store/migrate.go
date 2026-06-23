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
	profile     TEXT NOT NULL DEFAULT 'raw-http',
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
	profile            TEXT NOT NULL DEFAULT '',
	profile_params     TEXT NOT NULL DEFAULT '{}',
	browse_methods     TEXT NOT NULL DEFAULT '',
	browse_path        TEXT NOT NULL DEFAULT '',
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

// migration is one ordered, run-once upgrade step for EXISTING databases. Its 1-based position in
// `migrations` is its version; `up` runs inside a transaction and may do anything SQL can
// (ALTER/backfill/rename).
type migration struct {
	name string
	up   func(tx *sql.Tx) error
}

// migrations is the ordered list of schema versions.
//
//   - Version 1 is the **baseline**: the full `schema` (CREATE TABLE IF NOT EXISTS).
//   - Each later entry is a forward upgrade step that brings a database created by an EARLIER
//     version up by one (an ALTER, a backfill, a rename, …).
//
// `schema` above is always the CURRENT shape. A brand-new database is built straight from it and
// stamped at the latest version (see migrate), so it NEVER runs the upgrade steps — those exist only
// to catch up OLD databases. Therefore, when you change the schema: (1) edit `schema` to the new
// current shape, AND (2) append a migration step performing the same change via ALTER for existing
// databases. **Never edit or reorder a released step** — its position is its version.
var migrations = []migration{
	{"baseline", func(tx *sql.Tx) error {
		if _, err := tx.Exec(schema); err != nil {
			return fmt.Errorf("baseline schema: %w", err)
		}
		return nil
	}},
	{"server_profiles", func(tx *sql.Tx) error {
		for _, stmt := range []string{
			`ALTER TABLE upstreams ADD COLUMN profile TEXT NOT NULL DEFAULT 'raw-http'`,
			`ALTER TABLE rules ADD COLUMN profile TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE rules ADD COLUMN profile_params TEXT NOT NULL DEFAULT '{}'`,
		} {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("server_profiles: %w", err)
			}
		}
		return nil
	}},
	{"browse_rules", func(tx *sql.Tx) error {
		for _, stmt := range []string{
			`ALTER TABLE rules ADD COLUMN browse_methods TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE rules ADD COLUMN browse_path TEXT NOT NULL DEFAULT ''`,
		} {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("browse_rules: %w", err)
			}
		}
		return nil
	}},
}

// migrate brings db up to the latest version. A brand-new (empty) database is created directly from
// the current `schema` and stamped at the latest version, so its upgrade steps never run. An
// existing database runs each pending step (version > user_version) in its own transaction, stamping
// user_version on success; a failed step rolls back and leaves the version unchanged.
func migrate(db *sql.DB) error {
	var ver int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if ver == 0 {
		empty, err := isEmpty(db)
		if err != nil {
			return err
		}
		if empty {
			// Fresh database: the current schema already reflects every migration.
			if _, err := db.Exec(schema); err != nil {
				return fmt.Errorf("apply schema: %w", err)
			}
			return setUserVersion(db, len(migrations))
		}
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

// setUserVersion stamps the schema version. PRAGMA takes no bound parameter; n is an int we control.
func setUserVersion(db *sql.DB, n int) error {
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", n)); err != nil {
		return fmt.Errorf("stamp version %d: %w", n, err)
	}
	return nil
}

// isEmpty reports whether the database has no user tables yet (a brand-new file). The internal
// sqlite_* tables are excluded.
func isEmpty(db *sql.DB) (bool, error) {
	var n int
	if err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`,
	).Scan(&n); err != nil {
		return false, fmt.Errorf("count tables: %w", err)
	}
	return n == 0, nil
}
