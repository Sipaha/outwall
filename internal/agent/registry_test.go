package agent

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/store"
)

func newReg(t *testing.T) *Registry {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return NewRegistry(s)
}

func TestRegisterAndAuthenticate(t *testing.T) {
	reg := newReg(t)

	a, token, err := reg.Register("claude-code")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(token, "owa_"))
	require.Equal(t, "new", a.Status)

	got, err := reg.Authenticate(token)
	require.NoError(t, err)
	require.Equal(t, a.ID, got.ID)

	_, err = reg.Authenticate("owa_bogus")
	require.ErrorIs(t, err, ErrUnknownToken)
}

func TestListOrdersNewestFirst(t *testing.T) {
	reg := newReg(t)

	a1, _, err := reg.Register("first")
	require.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	a2, _, err := reg.Register("second")
	require.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	a3, _, err := reg.Register("third")
	require.NoError(t, err)

	got, err := reg.List()
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, []string{a3.ID, a2.ID, a1.ID}, []string{got[0].ID, got[1].ID, got[2].ID})
}
