//go:build !windows

package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrFocusedExisting signals the caller (main) to exit 0: another instance was already running
// and was told to raise its window. It is distinct from a hard error (lock held but nobody
// answered the focus socket — a stale lock). The os.Exit is the caller's job so this stays
// testable. See ADR-0013.
var ErrFocusedExisting = errors.New("another instance is running; focused it")

// InstanceLock is a flock-based single-instance lock for the desktop process. flock is released
// automatically when the process dies (crash-safe — no stale-lock bookkeeping). Release it on
// a clean exit.
type InstanceLock struct {
	file *os.File
}

// AcquireInstanceLock flocks lockPath (LOCK_EX|LOCK_NB) to ensure only one desktop process runs
// at a time. On success it returns the held lock. On a lock conflict it posts to the running
// instance's focus socket (socketPath): if that answers, it returns ErrFocusedExisting (the
// caller exits 0 — the running window was raised); if it does not answer (stale lock, no live
// daemon) it returns a wrapped error. os.Exit is the caller's job. See ADR-0013.
func AcquireInstanceLock(lockPath, socketPath string) (*InstanceLock, error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		// Another instance holds the lock. Hand focus off to it over the admin socket. Only
		// swallow the conflict when the daemon actually answers — a stale lock file with no
		// live daemon must still surface as an error rather than silently exit.
		if notifyErr := NotifyExistingInstance(socketPath); notifyErr != nil {
			return nil, fmt.Errorf("instance lock held but no live daemon to focus: %w", notifyErr)
		}
		return nil, ErrFocusedExisting
	}

	// We are the primary instance. Record our pid for diagnostics (the flock, not this content,
	// is the gate).
	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	_ = f.Sync()

	return &InstanceLock{file: f}, nil
}

// Release unlocks and removes the lock file. Safe to call on a nil-file lock.
func (l *InstanceLock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	name := l.file.Name()
	_ = l.file.Close()
	_ = os.Remove(name)
}
