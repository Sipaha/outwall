package opsession

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionLifecycle(t *testing.T) {
	now := time.Unix(1000, 0)
	s := New(time.Hour)
	s.now = func() time.Time { return now }

	require.False(t, s.Authorized()) // closed before Open

	s.Open()
	require.True(t, s.Authorized()) // authorized right after Open

	// Sliding window: a call just under the TTL refreshes lastUsed, so a further sub-TTL gap
	// still authorizes (idle time is measured from the LAST authorized call, not from Open).
	now = now.Add(59 * time.Minute)
	require.True(t, s.Authorized())
	now = now.Add(59 * time.Minute)
	require.True(t, s.Authorized(), "each Authorized() call slides the idle window")

	// A full TTL of idleness after the last call → not authorized.
	now = now.Add(time.Hour)
	require.False(t, s.Authorized())

	// Re-open, then Lock → not authorized.
	s.Open()
	require.True(t, s.Authorized())
	s.Lock()
	require.False(t, s.Authorized())
}

func TestSessionStatusDoesNotSlide(t *testing.T) {
	now := time.Unix(1000, 0)
	s := New(time.Hour)
	s.now = func() time.Time { return now }

	open, rem := s.Status()
	require.False(t, open)
	require.Zero(t, rem)

	s.Open()
	open, rem = s.Status()
	require.True(t, open)
	require.Equal(t, time.Hour, rem)

	// Status is a read-only peek: it must NOT refresh lastUsed, so remaining shrinks over time.
	now = now.Add(30 * time.Minute)
	open, rem = s.Status()
	require.True(t, open)
	require.Equal(t, 30*time.Minute, rem)

	// Past the TTL, Status reports closed.
	now = now.Add(31 * time.Minute)
	open, rem = s.Status()
	require.False(t, open)
	require.Zero(t, rem)
}

func TestDefaultTTL(t *testing.T) {
	require.Equal(t, time.Hour, DefaultTTL)
	require.Equal(t, DefaultTTL, New(0).ttl, "a non-positive ttl falls back to DefaultTTL")
	require.Equal(t, DefaultTTL, New(-time.Second).ttl)
}
