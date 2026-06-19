package store

import (
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
