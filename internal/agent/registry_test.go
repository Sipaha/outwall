package agent

import (
	"path/filepath"
	"strings"
	"testing"

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
