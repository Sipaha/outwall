package policy

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/store"
)

func newReg(t *testing.T) *Registry {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "r.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return NewRegistry(s)
}

func TestRuleCRUD(t *testing.T) {
	reg := newReg(t)
	r, err := reg.Create(Rule{UpstreamID: "u1", Method: "GET", PathGlob: "/repos/**", Outcome: Allow, RateLimitPerMin: 60})
	require.NoError(t, err)
	require.NotEmpty(t, r.ID)

	_, err = reg.Create(Rule{UpstreamID: "u1", Outcome: "bogus"})
	require.Error(t, err) // invalid outcome rejected

	got, err := reg.ForUpstream("u1")
	require.NoError(t, err)
	require.Len(t, got, 1)

	require.NoError(t, reg.Delete(r.ID))
	got, err = reg.ForUpstream("u1")
	require.NoError(t, err)
	require.Empty(t, got)
}
