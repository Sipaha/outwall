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

// TestFreshDBFromSchema: a brand-new database is built from the current `schema` and stamped at the
// latest version. It also pins the regression that broke the Agents screen — the current schema must
// include access_requests.reason.
func TestFreshDBFromSchema(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	require.NoError(t, err)
	defer s.Close()
	// A column the app queries (whose absence raised "no such column: reason") exists in the schema.
	_, err = s.DB().Exec(`SELECT reason FROM access_requests LIMIT 0`)
	require.NoError(t, err)
	require.Equal(t, len(migrations), userVersion(t, s), "fresh DB stamped at the latest version")
}

// TestFreshDBSkipsUpgradeSteps is the core property of the model: a fresh database is built from the
// current `schema` (which already reflects every change) and stamped at the latest version, so the
// per-step ALTERs — which exist only to upgrade OLD databases — never run on it.
func TestFreshDBSkipsUpgradeSteps(t *testing.T) {
	orig := migrations
	t.Cleanup(func() { migrations = orig })
	ran := false
	migrations = append(append([]migration{}, orig...), migration{"should_not_run_on_fresh", func(tx *sql.Tx) error {
		ran = true
		_, err := tx.Exec(`ALTER TABLE settings ADD COLUMN extra TEXT NOT NULL DEFAULT ''`)
		return err
	}})

	s, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	require.NoError(t, err)
	defer s.Close()

	require.False(t, ran, "a fresh DB must be built from schema, not by running upgrade steps")
	require.Equal(t, len(migrations), userVersion(t, s), "fresh DB stamped at the latest version")
}

func userVersion(t *testing.T, s *Store) int {
	t.Helper()
	var v int
	require.NoError(t, s.DB().QueryRow(`PRAGMA user_version`).Scan(&v))
	return v
}

// TestMigrationRunnerAppliesPendingOnce verifies the versioned runner: a fresh DB lands at the
// baseline version, a newly-appended structural step runs exactly once and bumps user_version, and
// re-running migrate does not replay it.
func TestMigrationRunnerAppliesPendingOnce(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "mig.db"))
	require.NoError(t, err)
	defer s.Close()
	require.Equal(t, len(migrations), userVersion(t, s), "fresh DB is stamped at the latest version")
	baseline := userVersion(t, s)

	// Temporarily append a structural step that does something ensureColumns can't (a rename) and
	// records that it ran.
	orig := migrations
	t.Cleanup(func() { migrations = orig })
	runs := 0
	migrations = append(append([]migration{}, orig...), migration{"demo_rename", func(tx *sql.Tx) error {
		runs++
		_, err := tx.Exec(`ALTER TABLE settings RENAME TO settings_renamed; ALTER TABLE settings_renamed RENAME TO settings;`)
		return err
	}})

	require.NoError(t, migrate(s.DB()))
	require.Equal(t, 1, runs)
	require.Equal(t, baseline+1, userVersion(t, s), "the new step bumped the version")

	// Re-running is a no-op: the step is not replayed.
	require.NoError(t, migrate(s.DB()))
	require.Equal(t, 1, runs, "an applied migration must not run again")
	require.Equal(t, baseline+1, userVersion(t, s))
}

// TestServerProfileColumns: an existing v1 database is upgraded to carry the server-profile columns,
// and a fresh DB has them from the current schema.
func TestServerProfileColumns(t *testing.T) {
	// Fresh DB from current schema.
	s, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	require.NoError(t, err)
	defer s.Close()
	_, err = s.DB().Exec(`SELECT profile FROM upstreams LIMIT 0`)
	require.NoError(t, err)
	_, err = s.DB().Exec(`SELECT profile, profile_params FROM rules LIMIT 0`)
	require.NoError(t, err)

	// Simulate an OLD (v1) database: baseline schema without the new columns, stamped at version 1.
	p := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", p)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE upstreams (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, base_url TEXT NOT NULL, kind TEXT NOT NULL DEFAULT 'http', auth_type TEXT NOT NULL, auth_config BLOB, created_at TEXT NOT NULL);
		CREATE TABLE rules (id TEXT PRIMARY KEY, subject_agent_id TEXT NOT NULL DEFAULT '', upstream_id TEXT NOT NULL, op_method TEXT NOT NULL DEFAULT '', op_path_template TEXT NOT NULL DEFAULT '', op_query_template TEXT NOT NULL DEFAULT '{}', op_body_template TEXT NOT NULL DEFAULT '{}', op_value_policies TEXT NOT NULL DEFAULT '{}', outcome TEXT NOT NULL, rate_limit_per_min INTEGER NOT NULL DEFAULT 0, k8s_namespace TEXT NOT NULL DEFAULT '', k8s_resource TEXT NOT NULL DEFAULT '', k8s_verb TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);
		PRAGMA user_version = 1;`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// Re-open through the runner: the new migration adds the columns.
	s2, err := Open(p)
	require.NoError(t, err)
	defer s2.Close()
	_, err = s2.DB().Exec(`SELECT profile FROM upstreams LIMIT 0`)
	require.NoError(t, err)
	_, err = s2.DB().Exec(`SELECT profile, profile_params FROM rules LIMIT 0`)
	require.NoError(t, err)
	require.Equal(t, len(migrations), userVersion(t, s2))
}

func TestBrowseRuleColumns(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	require.NoError(t, err)
	defer s.Close()
	_, err = s.DB().Exec(`SELECT browse_methods, browse_path FROM rules LIMIT 0`)
	require.NoError(t, err)
	require.Equal(t, len(migrations), userVersion(t, s))
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
