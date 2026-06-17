// Package approval is the blocking approval queue: a require-approval data-plane request
// parks in Submit until the operator resolves it (or it times out).
package approval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// DefaultTimeout bounds how long a request blocks waiting for a decision.
const DefaultTimeout = 5 * time.Minute

// ErrNotFound is returned by Resolve for an unknown pending id.
var ErrNotFound = errors.New("approval not found")

// Pending describes a request awaiting approval.
type Pending struct {
	ID         string
	AgentID    string
	UpstreamID string
	Method     string
	Path       string
	Purpose    string
	CreatedAt  time.Time
}

type waiter struct {
	p  Pending
	ch chan bool
}

// Queue holds in-flight approval waiters.
type Queue struct {
	timeout time.Duration
	mu      sync.Mutex
	waiters map[string]*waiter
}

// NewQueue constructs a Queue with the default timeout.
func NewQueue() *Queue { return NewQueueWithTimeout(DefaultTimeout) }

// NewQueueWithTimeout constructs a Queue with a custom blocking timeout.
func NewQueueWithTimeout(d time.Duration) *Queue {
	return &Queue{timeout: d, waiters: map[string]*waiter{}}
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Submit parks until Resolve, timeout, or ctx cancellation.
func (q *Queue) Submit(ctx context.Context, p Pending) (bool, error) {
	p.ID = newID()
	p.CreatedAt = time.Now().UTC()
	w := &waiter{p: p, ch: make(chan bool, 1)}
	q.mu.Lock()
	q.waiters[p.ID] = w
	q.mu.Unlock()

	defer func() {
		q.mu.Lock()
		delete(q.waiters, p.ID)
		q.mu.Unlock()
	}()

	timer := time.NewTimer(q.timeout)
	defer timer.Stop()
	select {
	case ok := <-w.ch:
		return ok, nil
	case <-timer.C:
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// List snapshots the currently-waiting entries.
func (q *Queue) List() []Pending {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Pending, 0, len(q.waiters))
	for _, w := range q.waiters {
		out = append(out, w.p)
	}
	return out
}

// Resolve delivers a decision to a waiting Submit.
func (q *Queue) Resolve(id string, approve bool) error {
	q.mu.Lock()
	w, ok := q.waiters[id]
	q.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	w.ch <- approve
	return nil
}
