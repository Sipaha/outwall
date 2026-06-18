//go:build !windows

package desktop

import (
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAcquireInstanceLockExclusive(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "desktop.lock")
	deadSocket := filepath.Join(dir, "dead.sock") // nothing listening here

	// First acquire succeeds even though the focus socket is dead (no prior instance).
	lock, err := AcquireInstanceLock(lockPath, deadSocket)
	require.NoError(t, err)
	require.NotNil(t, lock)

	// A second acquire while the lock is held, with a socket that ANSWERS /desktop/focus,
	// must report ErrFocusedExisting (caller exits 0 — the running instance was raised).
	liveSocket := filepath.Join(dir, "live.sock")
	serveUnix(t, liveSocket, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	_, err = AcquireInstanceLock(lockPath, liveSocket)
	require.ErrorIs(t, err, ErrFocusedExisting)

	// A second acquire with a socket that does NOT answer (stale lock, no live daemon)
	// returns a non-nil error that is NOT ErrFocusedExisting.
	_, err = AcquireInstanceLock(lockPath, deadSocket)
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrFocusedExisting))

	// After Release, a fresh acquire succeeds.
	lock.Release()
	lock2, err := AcquireInstanceLock(lockPath, deadSocket)
	require.NoError(t, err)
	require.NotNil(t, lock2)
	lock2.Release()
}
