package approval

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSubmitApprove(t *testing.T) {
	q := NewQueueWithTimeout(2 * time.Second)
	done := make(chan bool, 1)
	go func() {
		ok, err := q.Submit(context.Background(), Pending{AgentID: "a1", UpstreamID: "u1", Method: "DELETE", Path: "/x", Purpose: "cleanup"})
		require.NoError(t, err)
		done <- ok
	}()

	// Wait for it to appear in the queue, then approve it.
	require.Eventually(t, func() bool { return len(q.List()) == 1 }, time.Second, 10*time.Millisecond)
	id := q.List()[0].ID
	require.NoError(t, q.Resolve(id, true))
	require.True(t, <-done)
	require.Empty(t, q.List()) // removed after resolve
}

func TestSubmitTimeout(t *testing.T) {
	q := NewQueueWithTimeout(100 * time.Millisecond)
	ok, err := q.Submit(context.Background(), Pending{AgentID: "a1", UpstreamID: "u1"})
	require.NoError(t, err)
	require.False(t, ok) // timed out → denied
}

func TestResolveUnknown(t *testing.T) {
	q := NewQueue()
	require.ErrorIs(t, q.Resolve("nope", true), ErrNotFound)
}
