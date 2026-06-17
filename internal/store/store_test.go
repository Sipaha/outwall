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
