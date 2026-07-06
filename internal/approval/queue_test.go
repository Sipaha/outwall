package approval

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type capturePub struct {
	mu    sync.Mutex
	types []string
}

func (c *capturePub) Publish(t string, _ any) {
	c.mu.Lock()
	c.types = append(c.types, t)
	c.mu.Unlock()
}

func TestSubmitPublishesEnqueued(t *testing.T) {
	q := NewQueueWithTimeout(100 * time.Millisecond)
	cp := &capturePub{}
	q.SetPublisher(cp)
	_, _ = q.Submit(context.Background(), Pending{AgentID: "a1"}) // times out, but should have published enqueued
	cp.mu.Lock()
	defer cp.mu.Unlock()
	require.Contains(t, cp.types, "approval.enqueued")
}

func TestResolvePublishesResolved(t *testing.T) {
	q := NewQueueWithTimeout(2 * time.Second)
	cp := &capturePub{}
	q.SetPublisher(cp)
	done := make(chan struct{})
	go func() {
		_, _ = q.Submit(context.Background(), Pending{AgentID: "a1"})
		close(done)
	}()
	require.Eventually(t, func() bool { return len(q.List()) == 1 }, time.Second, 10*time.Millisecond)
	require.NoError(t, q.Resolve(q.List()[0].ID, true, ""))
	<-done
	cp.mu.Lock()
	defer cp.mu.Unlock()
	require.Contains(t, cp.types, "approval.resolved")
}

func TestSubmitApprove(t *testing.T) {
	q := NewQueueWithTimeout(2 * time.Second)
	done := make(chan bool, 1)
	go func() {
		d, err := q.Submit(context.Background(), Pending{AgentID: "a1", UpstreamID: "u1", Method: "DELETE", Path: "/x", Purpose: "cleanup"})
		require.NoError(t, err)
		done <- d.Approved
	}()

	// Wait for it to appear in the queue, then approve it.
	require.Eventually(t, func() bool { return len(q.List()) == 1 }, time.Second, 10*time.Millisecond)
	id := q.List()[0].ID
	require.NoError(t, q.Resolve(id, true, ""))
	require.True(t, <-done)
	require.Empty(t, q.List()) // removed after resolve
}

func TestSubmitTimeout(t *testing.T) {
	q := NewQueueWithTimeout(100 * time.Millisecond)
	d, err := q.Submit(context.Background(), Pending{AgentID: "a1", UpstreamID: "u1"})
	require.NoError(t, err)
	require.False(t, d.Approved) // timed out → denied
}

func TestResolveDeliversDenyReason(t *testing.T) {
	q := NewQueueWithTimeout(2 * time.Second)
	type res struct {
		d   Decision
		err error
	}
	done := make(chan res, 1)
	go func() {
		d, err := q.Submit(context.Background(), Pending{AgentID: "a1", UpstreamID: "u1"})
		done <- res{d, err}
	}()
	require.Eventually(t, func() bool { return len(q.List()) == 1 }, time.Second, 10*time.Millisecond)
	require.NoError(t, q.Resolve(q.List()[0].ID, false, "not on prod"))
	got := <-done
	require.NoError(t, got.err)
	require.False(t, got.d.Approved)
	require.Equal(t, "not on prod", got.d.Reason)
}

func TestResolveUnknown(t *testing.T) {
	q := NewQueue()
	require.ErrorIs(t, q.Resolve("nope", true, ""), ErrNotFound)
}

func TestListOrdersNewestFirst(t *testing.T) {
	q := NewQueueWithTimeout(2 * time.Second)

	submit := func(agentID string) {
		go func() { _, _ = q.Submit(context.Background(), Pending{AgentID: agentID}) }()
	}
	submit("a1")
	require.Eventually(t, func() bool { return len(q.List()) == 1 }, time.Second, 10*time.Millisecond)
	time.Sleep(2 * time.Millisecond)
	submit("a2")
	require.Eventually(t, func() bool { return len(q.List()) == 2 }, time.Second, 10*time.Millisecond)
	time.Sleep(2 * time.Millisecond)
	submit("a3")
	require.Eventually(t, func() bool { return len(q.List()) == 3 }, time.Second, 10*time.Millisecond)

	got := q.List()
	require.Len(t, got, 3)
	require.Equal(t, []string{"a3", "a2", "a1"}, []string{got[0].AgentID, got[1].AgentID, got[2].AgentID})

	// Clean up so no goroutine leaks past the test.
	for _, p := range got {
		require.NoError(t, q.Resolve(p.ID, true, ""))
	}
}
