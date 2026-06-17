package access

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/store"
)

func newReg(t *testing.T) *Registry {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "acc.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return NewRegistry(s)
}

func TestAccessRequestLifecycle(t *testing.T) {
	reg := newReg(t)
	r, err := reg.Create("a1", "u1", "triage issues")
	require.NoError(t, err)
	require.Equal(t, "pending", r.Status)

	p, err := reg.Pending()
	require.NoError(t, err)
	require.Len(t, p, 1)
	require.Equal(t, "triage issues", p[0].Purpose)

	require.NoError(t, reg.Resolve(r.ID, "granted"))
	require.ErrorIs(t, reg.Resolve("nope", "granted"), ErrNotFound)
	require.Error(t, reg.Resolve(r.ID, "bogus")) // invalid status

	p, _ = reg.Pending()
	require.Empty(t, p) // no longer pending
}
