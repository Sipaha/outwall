package grant

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/store"
)

func TestDefaultDenyThenAllow(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "g.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	reg := NewRegistry(s)

	ok, err := reg.Allowed("a1", "u1")
	require.NoError(t, err)
	require.False(t, ok) // default-deny

	require.NoError(t, reg.Add("a1", "u1"))
	require.NoError(t, reg.Add("a1", "u1")) // idempotent

	ok, err = reg.Allowed("a1", "u1")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = reg.Allowed("a1", "u2")
	require.NoError(t, err)
	require.False(t, ok)
}
