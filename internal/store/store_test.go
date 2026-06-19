package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAppliesSchemaIdempotently(t *testing.T) {
	p := filepath.Join(t.TempDir(), "outwall.db")

	s, err := Open(p)
	require.NoError(t, err)

	// Tables exist.
	for _, table := range []string{"vault_meta", "upstreams", "agents", "rules"} {
		var name string
		err := s.DB().QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		require.NoError(t, err, "table %s missing", table)
		require.Equal(t, table, name)
	}
	require.NoError(t, s.Close())

	// Re-open is idempotent (migrations don't fail on existing schema).
	s2, err := Open(p)
	require.NoError(t, err)
	require.NoError(t, s2.Close())
}

// TestSchemaCoversAdditiveColumns keeps additiveColumns honest: every column the migration may add
// must actually exist in a freshly-built schema (catches typos / stale entries).
func TestSchemaCoversAdditiveColumns(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	require.NoError(t, err)
	defer s.Close()
	for _, c := range additiveColumns {
		has, err := columnExists(s.DB(), c.table, c.name)
		require.NoError(t, err)
		require.True(t, has, "additive column %s.%s is missing from the fresh schema", c.table, c.name)
	}
}

// TestEnsureColumnsUpgradesOldDB simulates a database created by an older build (tables lacking the
// later additive columns) and verifies Open's migration adds them — no reset needed.
func TestEnsureColumnsUpgradesOldDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	// Build an "old" DB by hand: audit_log without operation/vars_json, rules without
	// op_body_template, upstreams without kind. Insert a row so the table is non-empty (the
	// stricter ADD COLUMN path).
	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = raw.Exec(`
		CREATE TABLE audit_log (id TEXT PRIMARY KEY, ts TEXT NOT NULL DEFAULT '', method TEXT NOT NULL DEFAULT '');
		INSERT INTO audit_log (id, ts, method) VALUES ('a1', '2026-06-19T00:00:00Z', 'GET');
		CREATE TABLE rules (id TEXT PRIMARY KEY, upstream_id TEXT NOT NULL DEFAULT '', op_method TEXT NOT NULL DEFAULT '');
		INSERT INTO rules (id, upstream_id, op_method) VALUES ('r1', 'u1', 'GET');
		CREATE TABLE upstreams (id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '');
		INSERT INTO upstreams (id, name) VALUES ('u1', 'gh');
	`)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	// Open runs the migration, which must add the missing additive columns to the existing tables.
	s, err := Open(path)
	require.NoError(t, err)
	defer s.Close()

	for _, c := range additiveColumns {
		has, err := columnExists(s.DB(), c.table, c.name)
		require.NoError(t, err)
		require.True(t, has, "%s.%s should have been added by the migration", c.table, c.name)
	}

	// The pre-existing rows survive and the new columns are usable (default applied).
	var op string
	require.NoError(t, s.DB().QueryRow(`SELECT operation FROM audit_log WHERE id='a1'`).Scan(&op))
	require.Equal(t, "", op)
	var body string
	require.NoError(t, s.DB().QueryRow(`SELECT op_body_template FROM rules WHERE id='r1'`).Scan(&body))
	require.Equal(t, "{}", body)
}

func TestSettingsRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "outwall.db"))
	require.NoError(t, err)
	defer s.Close()

	_, ok, err := s.GetSetting("missing")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, s.SetSetting("k", "v1"))
	v, ok, err := s.GetSetting("k")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "v1", v)

	// Upsert overwrites.
	require.NoError(t, s.SetSetting("k", "v2"))
	v, _, _ = s.GetSetting("k")
	require.Equal(t, "v2", v)
}
