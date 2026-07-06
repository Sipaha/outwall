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

func TestVerify(t *testing.T) {
	v := newVault(t)

	// Not initialized yet → ErrNotInitialized.
	require.ErrorIs(t, v.Verify("pw"), ErrNotInitialized)

	require.NoError(t, v.Init("correct horse"))
	require.False(t, v.Locked()) // Init leaves it unlocked.

	// Correct password verifies and leaves the vault UNLOCKED (state unchanged).
	require.NoError(t, v.Verify("correct horse"))
	require.False(t, v.Locked())

	// Wrong password → ErrBadPassword; state still unlocked.
	require.ErrorIs(t, v.Verify("nope"), ErrBadPassword)
	require.False(t, v.Locked())

	// While LOCKED, Verify still works (it needs no resident key) and leaves it LOCKED.
	v.Lock()
	require.True(t, v.Locked())
	require.NoError(t, v.Verify("correct horse"))
	require.True(t, v.Locked(), "Verify must NOT unlock a locked vault")
	require.ErrorIs(t, v.Verify("nope"), ErrBadPassword)
	require.True(t, v.Locked())
}
