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
	method             TEXT NOT NULL DEFAULT '',
	path_glob          TEXT NOT NULL DEFAULT '/**',
	outcome            TEXT NOT NULL,
	rate_limit_per_min INTEGER NOT NULL DEFAULT 0,
	created_at         TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS rules_by_upstream ON rules(upstream_id);
`

func migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}
