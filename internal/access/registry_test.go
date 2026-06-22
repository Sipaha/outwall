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

func TestGrantLatest(t *testing.T) {
	reg := newReg(t)
	_, err := reg.Create("a1", "u1", "first")
	require.NoError(t, err)
	r2, err := reg.Create("a1", "u1", "second")
	require.NoError(t, err)

	ok, err := reg.GrantLatest("a1", "u1")
	require.NoError(t, err)
	require.True(t, ok)

	// The latest pending row is now granted; the older one stays pending.
	latest, found, err := reg.Latest("a1", "u1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, r2.ID, latest.ID)
	require.Equal(t, StatusGranted, latest.Status)

	// No pending row for a different (agent, upstream) → no-op.
	ok, err = reg.GrantLatest("a1", "other")
	require.NoError(t, err)
	require.False(t, ok)
}
