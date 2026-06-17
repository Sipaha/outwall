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

	"github.com/Sipaha/outwall/internal/audit"
	"github.com/Sipaha/outwall/internal/events"
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

	// k8s display fields (set for k8s-cluster requests; empty otherwise). Used by the UI to
	// show the parsed tuple. K2 makes mutating verbs actually park here.
	Namespace string
	Resource  string // "resource" or "resource/subresource"
	Verb      string

	// RequestBody is the captured agent-sent request body (the patch/apply payload), capped at
	// audit.BodyCap, surfaced on the approval card so the operator sees exactly what will
	// change. It carries ONLY the agent's body — never the injected cluster credential. Empty
	// for bodyless requests.
	RequestBody []byte
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
	pub     events.Publisher
}

// SetPublisher attaches a (nil-safe) event publisher. The queue publishes "approval.enqueued"
// on Submit and "approval.resolved" on Resolve. Passing nil disables publishing.
func (q *Queue) SetPublisher(p events.Publisher) {
	q.mu.Lock()
	q.pub = p
	q.mu.Unlock()
}

func (q *Queue) publisher() events.Publisher {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pub
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

	if pub := q.publisher(); pub != nil {
		evt := map[string]any{
			"id": p.ID, "agent_id": p.AgentID, "upstream_id": p.UpstreamID,
			"method": p.Method, "path": p.Path, "purpose": p.Purpose,
			// k8s tuple (empty for http approvals) so the console can render the change target.
			"namespace": p.Namespace, "resource": p.Resource, "verb": p.Verb,
		}
		// The agent-sent patch/apply body, credentials masked — never the injected cluster
		// credential (that is added downstream of capture).
		if len(p.RequestBody) > 0 {
			evt["request_body"] = audit.MaskBody(p.RequestBody)
		}
		pub.Publish("approval.enqueued", evt)
	}

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
	if pub := q.publisher(); pub != nil {
		pub.Publish("approval.resolved", map[string]any{"id": id, "approved": approve})
	}
	return nil
}
