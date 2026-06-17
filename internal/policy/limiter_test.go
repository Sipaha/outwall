package policy

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLimiterFixedWindow(t *testing.T) {
	l := NewLimiter()
	t0 := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)

	require.True(t, l.Allow("k", 0, t0))                  // unlimited
	require.True(t, l.Allow("k", 2, t0))                  // 1
	require.True(t, l.Allow("k", 2, t0))                  // 2
	require.False(t, l.Allow("k", 2, t0))                 // 3 → over
	require.True(t, l.Allow("k", 2, t0.Add(time.Minute))) // next window resets
}
