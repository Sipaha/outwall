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

// NewValue is a not-yet-allowed (variable, value) pair on an http operation new-value approval.
type NewValue struct {
	Var   string `json:"var"`
	Value string `json:"value"`
}

// Variable is a declared typed operation variable carried on a KindOperation approval (mirrors
// optemplate.Variable without importing it, so approval stays a leaf package).
type Variable struct {
	Name string `json:"name"`
	Type string `json:"type"` // "text" | "date"
}

// K8sGrant is one (namespace, resource, verb) tuple requested in a KindK8sAccess approval. A single
// request_k8s_access call can carry several (e.g. pods/get, pods/list, pods/log/get) so one card
// covers everything the agent needs; on approve each becomes an allow rule (see ADR-0029).
type K8sGrant struct {
	Namespace string `json:"namespace"`
	Resource  string `json:"resource"`
	Verb      string `json:"verb"`
}

// Kinds of MCP control-plane approval (the discriminator the daemon resolve path switches on).
// An empty Kind is a pre-H2 data-plane new-value / k8s approval, resolved by the queue alone.
const (
	// KindHostAccess is a tier-1 MCP host-access request: on approve the operator attaches the
	// host credential to the lazily-created host upstream; on deny the host upstream is dropped.
	KindHostAccess = "host-access"
	// KindOperation is a tier-2 MCP operation request carrying the parsed operation shape + the
	// requested values: on approve the resolve path creates/extends the H1 operation rule.
	KindOperation = "operation"
	// KindK8sAccess is an MCP k8s-access request carrying a (namespace, resource, verb) tuple: on
	// approve the resolve path creates an agent-scoped allow k8s rule for that tuple. k8s clusters
	// are pre-credentialed, so there is no separate host tier for them (see ADR-0025).
	KindK8sAccess = "k8s-access"
	// KindPreset is an MCP preset request carrying a preset id + slot bindings: on approve the
	// resolve path expands the preset into agent-scoped rules (see ADR-0037).
	KindPreset = "preset"
)

// Pending describes a request awaiting approval.
type Pending struct {
	ID         string
	AgentID    string
	UpstreamID string
	Method     string
	Path       string
	Purpose    string
	CreatedAt  time.Time

	// Kind discriminates the MCP control-plane approvals (KindHostAccess / KindOperation) from
	// the pre-H2 data-plane new-value and k8s approvals (empty Kind). The daemon resolve path
	// uses it to attach a host credential or create/extend an operation rule.
	Kind string

	// Host is the host whose upstream this approval concerns (set for KindHostAccess /
	// KindOperation). On a host approve the operator attaches the credential to this host.
	Host string

	// Operation fields (set for KindOperation): the parsed-and-revalidated operation shape (so
	// optemplate.Template.Key() identity matches the H1 rule), the declared typed variables, and
	// the concrete values the agent intends to use (varName -> value). On approve the resolve path
	// creates the rule for this shape if absent and extends each text variable's allowed-set.
	OpMethod        string
	OpPathTemplate  string
	OpQueryTemplate map[string]string
	OpBodyTemplate  map[string]string
	OpVariables     []Variable
	OpValues        map[string]string

	// k8s display fields (set for k8s-cluster requests; empty otherwise). Used by the UI to
	// show the parsed tuple. K2 makes mutating verbs actually park here.
	Namespace string
	Resource  string // "resource" or "resource/subresource"
	Verb      string

	// K8sGrants is the set of (namespace, resource, verb) tuples on a KindK8sAccess approval — one
	// card can request several at once (ADR-0029). On approve each becomes an allow rule.
	K8sGrants []K8sGrant

	// Preset fields (set for KindPreset): the requested preset and its slot bindings. On approve the
	// resolve path re-validates the (possibly operator-edited) bindings and fans them out into rules.
	PresetID string
	Bindings map[string]string

	// HTTP operation fields (set for an http new-value approval; empty otherwise). RuleID is the
	// matched operation rule; NewValues are the not-yet-allowed (variable, value) pairs the
	// operator is being asked to admit; Template is the matched path-template for display. On
	// approve, the resolve path extends each variable's allowed-set with these values.
	RuleID    string
	NewValues []NewValue
	Template  string

	// RequestBody is the captured agent-sent request body (the patch/apply payload), capped at
	// audit.BodyCap, surfaced on the approval card so the operator sees exactly what will
	// change. It carries ONLY the agent's body — never the injected cluster credential. Empty
	// for bodyless requests.
	RequestBody []byte
}

// Decision is the operator's verdict on a pending approval. Reason is an optional human-readable
// explanation the operator may attach when denying (surfaced to the agent — see ADR addendum).
type Decision struct {
	Approved bool
	Reason   string
}

type waiter struct {
	p  Pending
	ch chan Decision
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

// Submit parks until Resolve, timeout, or ctx cancellation, returning the operator's Decision (a
// timeout or cancellation yields a non-approved Decision with no reason).
func (q *Queue) Submit(ctx context.Context, p Pending) (Decision, error) {
	p.ID = newID()
	p.CreatedAt = time.Now().UTC()
	w := &waiter{p: p, ch: make(chan Decision, 1)}
	q.mu.Lock()
	q.waiters[p.ID] = w
	q.mu.Unlock()

	if pub := q.publisher(); pub != nil {
		evt := map[string]any{
			"id": p.ID, "agent_id": p.AgentID, "upstream_id": p.UpstreamID,
			"host":   p.Host, // set for MCP host/operation approvals; empty for data-plane/k8s
			"method": p.Method, "path": p.Path, "purpose": p.Purpose,
			// k8s tuple (empty for http approvals) so the console can render the change target.
			"namespace": p.Namespace, "resource": p.Resource, "verb": p.Verb,
		}
		// http new-value approval context (empty for k8s approvals).
		if len(p.NewValues) > 0 {
			evt["new_values"] = p.NewValues
			evt["template"] = p.Template
			evt["rule_id"] = p.RuleID
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
	case d := <-w.ch:
		return d, nil
	case <-timer.C:
		return Decision{}, nil
	case <-ctx.Done():
		return Decision{}, ctx.Err()
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

// Get returns the pending entry with the given id and whether it was found. The daemon resolve
// path uses it to inspect a pending's Kind (host / operation) before delivering the decision, so
// it can attach a host credential or create/extend an operation rule.
func (q *Queue) Get(id string) (Pending, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	w, ok := q.waiters[id]
	if !ok {
		return Pending{}, false
	}
	return w.p, true
}

// Resolve delivers a decision to a waiting Submit. reason is an optional operator explanation,
// surfaced to the agent on deny; it is ignored on approve.
func (q *Queue) Resolve(id string, approve bool, reason string) error {
	q.mu.Lock()
	w, ok := q.waiters[id]
	q.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	if approve {
		reason = ""
	}
	w.ch <- Decision{Approved: approve, Reason: reason}
	if pub := q.publisher(); pub != nil {
		pub.Publish("approval.resolved", map[string]any{"id": id, "approved": approve, "reason": reason})
	}
	return nil
}
