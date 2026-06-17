package secret

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/store"
)

func newVault(t *testing.T) *Vault {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "v.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return NewVault(s)
}

func TestInitUnlockRoundTrip(t *testing.T) {
	v := newVault(t)

	init, err := v.Initialized()
	require.NoError(t, err)
	require.False(t, init)

	require.NoError(t, v.Init("correct horse"))
	require.False(t, v.Locked()) // Init leaves it unlocked

	enc, err := v.Encrypt([]byte("s3cr3t"))
	require.NoError(t, err)
	require.NotContains(t, string(enc), "s3cr3t")

	v.Lock()
	require.True(t, v.Locked())
	_, err = v.Encrypt([]byte("x"))
	require.ErrorIs(t, err, ErrLocked)

	require.ErrorIs(t, v.Unlock("wrong"), ErrBadPassword)
	require.NoError(t, v.Unlock("correct horse"))

	dec, err := v.Decrypt(enc)
	require.NoError(t, err)
	require.Equal(t, "s3cr3t", string(dec))
}
