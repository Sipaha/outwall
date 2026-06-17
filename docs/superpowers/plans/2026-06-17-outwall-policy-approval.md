# outwall — Plan 2: Policy Engine + Blocking Approval + OIDC Client-Credentials

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Replace Plan 1's flat `grant` allow-list with a real `policy` engine (rules with
subject/upstream/method/path-glob/rate-limit → allow/deny/require-approval), add a blocking
`approval` queue so `require-approval` requests pause until the operator decides, and add an
`oidc-client-credentials` authenticator (token cache + refresh) behind the existing
`authn.For` seam.

**Architecture:** The data-plane proxy's single allow/deny call site (Plan 1: `grants.Allowed`)
becomes `policy.Decide` → `{Outcome, Rule}`. `deny` → 403; `allow` → rate-limit-check → proxy;
`require-approval` → `approval.Submit` blocks (≤5 min) → approved ⇒ proxy, denied/timeout ⇒ 403.
Per the alpha quality bar, **`internal/grant` and the `grants` table are deleted** (no legacy
path) and replaced by `internal/policy` + a `rules` table. Upstream auth gains an `authn.Manager`
that caches per-upstream OIDC tokens across requests.

**Tech Stack:** same as Plan 1 (Go 1.26, modernc.org/sqlite, net/http, slog, cobra, testify).

## Global Constraints

(All Plan 1 Global Constraints still apply — module path `github.com/Sipaha/outwall`, no citeck,
no CGO, no panics in library code, gofmt tabs, testify, etc.)

- Approval blocking timeout: **5 minutes** default (`approval.DefaultTimeout`), overridable per `Queue`.
- Rate limit: fixed-window-per-minute, in-memory, keyed by `(agentID, ruleID)`; `0` = unlimited.
- Path glob: `*` matches within one path segment (no `/`), `**` matches across segments
  (including `/`). Matching is on the **upstream-relative** path (the part after `/<upstream>`).
- Policy precedence: agent-specific rules outrank `any`-subject rules outrank default-deny.
  Within the chosen tier, **most-restrictive wins**: any `deny` ⇒ deny; else any
  `require-approval` ⇒ require-approval; else `allow`.

## File Structure

```
Create:  internal/policy/rule.go          # Rule model + Outcome consts + glob matcher
         internal/policy/registry.go       # rules CRUD over the store
         internal/policy/decide.go         # Decide() precedence engine
         internal/policy/limiter.go         # in-memory fixed-window rate limiter
         internal/policy/*_test.go
         internal/approval/queue.go         # blocking approval queue
         internal/approval/queue_test.go
         internal/authn/oidc.go             # oidc-client-credentials authenticator + Manager
         internal/authn/oidc_test.go
Modify:  internal/store/migrate.go          # drop `grants`, add `rules`
         internal/upstream/registry.go      # AuthConfig: + TokenURL/ClientID/ClientSecret/Scope
         internal/authn/authn.go            # For(): + "oidc-client-credentials" case; add Manager
         internal/proxy/proxy.go            # Deps: Policy+Limiter+Approval+AuthManager; new flow
         internal/proxy/proxy_test.go        # update construction; add approval/ratelimit/deny tests
         internal/daemon/daemon.go           # wire policy/approval/limiter/authmanager; drop grant
         internal/daemon/admin.go            # /rules, /approvals endpoints; drop /grants
         internal/daemon/admin_test.go        # rules+approvals flow
         internal/cli/{root.go,grant.go→rule.go,approval.go}  # rule/approval commands; drop grant
Delete:  internal/grant/                     # replaced by internal/policy
```

---

### Task 1: Path glob matcher

**Files:** Create `internal/policy/rule.go` (matcher portion), `internal/policy/glob_test.go`.

**Interfaces:**
- Produces: `policy.MatchGlob(pattern, path string) bool`.

- [ ] **Step 1: failing test** — `internal/policy/glob_test.go`:
```go
package policy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"/repos/**", "/repos/x/y", true},
		{"/repos/**", "/repos", false},      // ** needs the trailing segment(s)
		{"/repos/*", "/repos/x", true},
		{"/repos/*", "/repos/x/y", false},   // * does not cross '/'
		{"/**", "/anything/at/all", true},
		{"/users/*/repos", "/users/bob/repos", true},
		{"/users/*/repos", "/users/bob/x/repos", false},
		{"**", "/a/b", true},
		{"/exact", "/exact", true},
		{"/exact", "/exacto", false},
	}
	for _, c := range cases {
		require.Equalf(t, c.want, MatchGlob(c.pat, c.path), "pat=%q path=%q", c.pat, c.path)
	}
}
```

- [ ] **Step 2: run** `go test ./internal/policy/` → FAIL (undefined `MatchGlob`).

- [ ] **Step 3: implement** — add to `internal/policy/rule.go`:
```go
// Package policy is the default-deny rule engine: rules bind a subject (a specific agent or
// "any") + upstream + method + path-glob to an outcome (allow/deny/require-approval), with a
// per-rule rate limit. It replaces Plan 1's flat grant allow-list.
package policy

import (
	"regexp"
	"strings"
	"sync"
)

var (
	globMu    sync.Mutex
	globCache = map[string]*regexp.Regexp{}
)

// MatchGlob reports whether path matches pattern, where '*' matches within one path segment
// (no '/') and '**' matches across segments (including '/').
func MatchGlob(pattern, path string) bool {
	globMu.Lock()
	re, ok := globCache[pattern]
	globMu.Unlock()
	if !ok {
		re = compileGlob(pattern)
		globMu.Lock()
		globCache[pattern] = re
		globMu.Unlock()
	}
	return re.MatchString(path)
}

func compileGlob(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch {
		case strings.HasPrefix(pattern[i:], "**"):
			b.WriteString(".*")
			i++ // consume the second '*'
		case pattern[i] == '*':
			b.WriteString("[^/]*")
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	b.WriteString("$")
	// regexp.MustCompile is safe: every byte is either a quoted literal or one of our
	// controlled metacharacters.
	return regexp.MustCompile(b.String())
}
```

- [ ] **Step 4: run** `go test ./internal/policy/` → PASS.
- [ ] **Step 5: commit** `feat(policy): path glob matcher (* within segment, ** across)`.

---

### Task 2: Rule model + registry (rules table)

**Files:** Modify `internal/store/migrate.go`; add to `internal/policy/rule.go`, create `internal/policy/registry.go`, `internal/policy/registry_test.go`.

**Interfaces:**
- Produces:
  - Outcome consts: `policy.Allow = "allow"`, `policy.Deny = "deny"`, `policy.RequireApproval = "require-approval"`.
  - `policy.Rule struct { ID, SubjectAgentID, UpstreamID, Method, PathGlob, Outcome string; RateLimitPerMin int; CreatedAt time.Time }`
    (SubjectAgentID `""` means "any agent"; Method `""` or `"*"` means any method).
  - `policy.NewRegistry(s *store.Store) *policy.Registry`
  - `(*Registry).Create(r Rule) (*Rule, error)` (assigns ID + CreatedAt; validates Outcome and that RateLimitPerMin ≥ 0).
  - `(*Registry).List() ([]*Rule, error)`
  - `(*Registry).Delete(id string) error`
  - `(*Registry).ForUpstream(upstreamID string) ([]*Rule, error)` — used by Decide.

- [ ] **Step 1: migration** — in `internal/store/migrate.go` replace the `grants` table with:
```sql
CREATE TABLE IF NOT EXISTS rules (
	id                 TEXT PRIMARY KEY,
	subject_agent_id   TEXT NOT NULL DEFAULT '',
	upstream_id        TEXT NOT NULL,
	method             TEXT NOT NULL DEFAULT '',
	path_glob          TEXT NOT NULL DEFAULT '/**',
	outcome            TEXT NOT NULL,
	rate_limit_per_min INTEGER NOT NULL DEFAULT 0,
	created_at         TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS rules_by_upstream ON rules(upstream_id);
```
(Delete the entire `CREATE TABLE ... grants ...` block — alpha, no compat.) Update the
`store_test.go` table-existence loop: replace `"grants"` with `"rules"`.

- [ ] **Step 2: failing test** — `internal/policy/registry_test.go`:
```go
package policy

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/store"
)

func newReg(t *testing.T) *Registry {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "r.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return NewRegistry(s)
}

func TestRuleCRUD(t *testing.T) {
	reg := newReg(t)
	r, err := reg.Create(Rule{UpstreamID: "u1", Method: "GET", PathGlob: "/repos/**", Outcome: Allow, RateLimitPerMin: 60})
	require.NoError(t, err)
	require.NotEmpty(t, r.ID)

	_, err = reg.Create(Rule{UpstreamID: "u1", Outcome: "bogus"})
	require.Error(t, err) // invalid outcome rejected

	got, err := reg.ForUpstream("u1")
	require.NoError(t, err)
	require.Len(t, got, 1)

	require.NoError(t, reg.Delete(r.ID))
	got, err = reg.ForUpstream("u1")
	require.NoError(t, err)
	require.Empty(t, got)
}
```

- [ ] **Step 3: implement** — add the consts + `Rule` to `rule.go`:
```go
import "time" // (add alongside the existing imports in rule.go)

// Outcomes.
const (
	Allow           = "allow"
	Deny            = "deny"
	RequireApproval = "require-approval"
)

// Rule binds a subject+upstream+method+path to an outcome.
type Rule struct {
	ID              string
	SubjectAgentID  string // "" = any agent
	UpstreamID      string
	Method          string // "" or "*" = any method
	PathGlob        string
	Outcome         string
	RateLimitPerMin int
	CreatedAt       time.Time
}

// ValidOutcome reports whether o is a known outcome.
func ValidOutcome(o string) bool { return o == Allow || o == Deny || o == RequireApproval }
```
Then `internal/policy/registry.go` (mirror the Plan 1 `agent.Registry` CRUD style — `newID()`
via `crypto/rand`+`hex`, `time.RFC3339Nano` timestamps, `store.DB()`):
```go
package policy

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/Sipaha/outwall/internal/store"
)

type Registry struct{ store *store.Store }

func NewRegistry(s *store.Store) *Registry { return &Registry{store: s} }

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (r *Registry) Create(in Rule) (*Rule, error) {
	if !ValidOutcome(in.Outcome) {
		return nil, fmt.Errorf("invalid outcome %q", in.Outcome)
	}
	if in.RateLimitPerMin < 0 {
		return nil, fmt.Errorf("rate limit must be >= 0")
	}
	if in.PathGlob == "" {
		in.PathGlob = "/**"
	}
	in.ID = newID()
	in.CreatedAt = time.Now().UTC()
	_, err := r.store.DB().Exec(
		`INSERT INTO rules (id, subject_agent_id, upstream_id, method, path_glob, outcome, rate_limit_per_min, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.SubjectAgentID, in.UpstreamID, in.Method, in.PathGlob, in.Outcome, in.RateLimitPerMin,
		in.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("insert rule: %w", err)
	}
	return &in, nil
}

func (r *Registry) Delete(id string) error {
	_, err := r.store.DB().Exec(`DELETE FROM rules WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	return nil
}

func (r *Registry) scanRows(query string, args ...any) ([]*Rule, error) {
	rows, err := r.store.DB().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query rules: %w", err)
	}
	defer rows.Close()
	var out []*Rule
	for rows.Next() {
		var (
			rule    Rule
			created string
		)
		if err := rows.Scan(&rule.ID, &rule.SubjectAgentID, &rule.UpstreamID, &rule.Method,
			&rule.PathGlob, &rule.Outcome, &rule.RateLimitPerMin, &created); err != nil {
			return nil, err
		}
		rule.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, &rule)
	}
	return out, rows.Err()
}

const ruleCols = `id, subject_agent_id, upstream_id, method, path_glob, outcome, rate_limit_per_min, created_at`

func (r *Registry) List() ([]*Rule, error) {
	return r.scanRows(`SELECT ` + ruleCols + ` FROM rules ORDER BY created_at`)
}

func (r *Registry) ForUpstream(upstreamID string) ([]*Rule, error) {
	return r.scanRows(`SELECT `+ruleCols+` FROM rules WHERE upstream_id=?`, upstreamID)
}
```

- [ ] **Step 4: run** `go test ./internal/policy/ ./internal/store/` → PASS.
- [ ] **Step 5: commit** `feat(policy): rules table + CRUD registry (replaces grants)`.

---

### Task 3: Decide() precedence engine

**Files:** Create `internal/policy/decide.go`, `internal/policy/decide_test.go`.

**Interfaces:**
- Produces:
  - `policy.Input struct { AgentID, UpstreamID, Method, Path string }`
  - `policy.Decision struct { Outcome string; Rule *policy.Rule }` (Outcome is `Allow`/`Deny`/`RequireApproval`; on default-deny, `Outcome=Deny, Rule=nil`).
  - `(*Registry).Decide(in Input) (Decision, error)`.

- [ ] **Step 1: failing test** — `internal/policy/decide_test.go`:
```go
package policy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func mk(t *testing.T, reg *Registry, r Rule) {
	t.Helper()
	_, err := reg.Create(r)
	require.NoError(t, err)
}

func TestDecidePrecedence(t *testing.T) {
	reg := newReg(t)

	// default-deny when no rules
	d, err := reg.Decide(Input{AgentID: "a1", UpstreamID: "u1", Method: "GET", Path: "/x"})
	require.NoError(t, err)
	require.Equal(t, Deny, d.Outcome)
	require.Nil(t, d.Rule)

	// global allow for any agent
	mk(t, reg, Rule{UpstreamID: "u1", Method: "*", PathGlob: "/**", Outcome: Allow})
	d, _ = reg.Decide(Input{AgentID: "a1", UpstreamID: "u1", Method: "GET", Path: "/x"})
	require.Equal(t, Allow, d.Outcome)

	// agent-specific deny outranks the global allow
	mk(t, reg, Rule{SubjectAgentID: "a1", UpstreamID: "u1", Method: "*", PathGlob: "/**", Outcome: Deny})
	d, _ = reg.Decide(Input{AgentID: "a1", UpstreamID: "u1", Method: "GET", Path: "/x"})
	require.Equal(t, Deny, d.Outcome)

	// a different agent still rides the global allow
	d, _ = reg.Decide(Input{AgentID: "a2", UpstreamID: "u1", Method: "GET", Path: "/x"})
	require.Equal(t, Allow, d.Outcome)

	// method + path narrowing: require-approval only on DELETE /danger/**
	reg2 := newReg(t)
	mk(t, reg2, Rule{UpstreamID: "u1", Method: "GET", PathGlob: "/**", Outcome: Allow})
	mk(t, reg2, Rule{UpstreamID: "u1", Method: "DELETE", PathGlob: "/danger/**", Outcome: RequireApproval})
	d, _ = reg2.Decide(Input{AgentID: "a1", UpstreamID: "u1", Method: "DELETE", Path: "/danger/x"})
	require.Equal(t, RequireApproval, d.Outcome)
	d, _ = reg2.Decide(Input{AgentID: "a1", UpstreamID: "u1", Method: "GET", Path: "/safe"})
	require.Equal(t, Allow, d.Outcome)
}
```

- [ ] **Step 2: run** → FAIL.

- [ ] **Step 3: implement** — `internal/policy/decide.go`:
```go
package policy

import "strings"

type Input struct {
	AgentID, UpstreamID, Method, Path string
}

type Decision struct {
	Outcome string
	Rule    *Rule
}

func methodMatches(ruleMethod, reqMethod string) bool {
	return ruleMethod == "" || ruleMethod == "*" || strings.EqualFold(ruleMethod, reqMethod)
}

// Decide applies precedence: agent-specific rules outrank any-subject rules outrank
// default-deny; within the chosen tier, most-restrictive wins (deny > require-approval > allow).
func (r *Registry) Decide(in Input) (Decision, error) {
	rules, err := r.ForUpstream(in.UpstreamID)
	if err != nil {
		return Decision{}, err
	}
	var agentTier, anyTier []*Rule
	for _, rule := range rules {
		if !methodMatches(rule.Method, in.Method) || !MatchGlob(rule.PathGlob, in.Path) {
			continue
		}
		switch rule.SubjectAgentID {
		case in.AgentID:
			agentTier = append(agentTier, rule)
		case "":
			anyTier = append(anyTier, rule)
		}
	}
	if d, ok := resolveTier(agentTier); ok {
		return d, nil
	}
	if d, ok := resolveTier(anyTier); ok {
		return d, nil
	}
	return Decision{Outcome: Deny, Rule: nil}, nil // default-deny
}

// resolveTier picks the most-restrictive outcome among matched rules in one tier.
func resolveTier(tier []*Rule) (Decision, bool) {
	if len(tier) == 0 {
		return Decision{}, false
	}
	var deny, approval, allow *Rule
	for _, r := range tier {
		switch r.Outcome {
		case Deny:
			if deny == nil {
				deny = r
			}
		case RequireApproval:
			if approval == nil {
				approval = r
			}
		case Allow:
			if allow == nil {
				allow = r
			}
		}
	}
	switch {
	case deny != nil:
		return Decision{Outcome: Deny, Rule: deny}, true
	case approval != nil:
		return Decision{Outcome: RequireApproval, Rule: approval}, true
	default:
		return Decision{Outcome: Allow, Rule: allow}, true
	}
}
```

- [ ] **Step 4: run** → PASS.
- [ ] **Step 5: commit** `feat(policy): Decide precedence engine (agent > any > default-deny, most-restrictive)`.

---

### Task 4: In-memory rate limiter

**Files:** Create `internal/policy/limiter.go`, `internal/policy/limiter_test.go`.

**Interfaces:**
- Produces:
  - `policy.NewLimiter() *policy.Limiter`
  - `(*Limiter).Allow(key string, limitPerMin int, now time.Time) bool` — `limitPerMin<=0` ⇒ always true; fixed window keyed by `key` + the minute of `now`.

- [ ] **Step 1: failing test** — `internal/policy/limiter_test.go`:
```go
package policy

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLimiterFixedWindow(t *testing.T) {
	l := NewLimiter()
	t0 := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)

	require.True(t, l.Allow("k", 0, t0))  // unlimited
	require.True(t, l.Allow("k", 2, t0))  // 1
	require.True(t, l.Allow("k", 2, t0))  // 2
	require.False(t, l.Allow("k", 2, t0)) // 3 → over
	require.True(t, l.Allow("k", 2, t0.Add(time.Minute))) // next window resets
}
```

- [ ] **Step 2: run** → FAIL.

- [ ] **Step 3: implement** — `internal/policy/limiter.go`:
```go
package policy

import (
	"sync"
	"time"
)

type window struct {
	minute int64
	count  int
}

// Limiter is an in-memory fixed-window-per-minute counter keyed by an arbitrary string.
type Limiter struct {
	mu sync.Mutex
	m  map[string]*window
}

func NewLimiter() *Limiter { return &Limiter{m: map[string]*window{}} }

// Allow records a hit for key and reports whether it is within limitPerMin for the current
// minute of now. limitPerMin <= 0 means unlimited.
func (l *Limiter) Allow(key string, limitPerMin int, now time.Time) bool {
	if limitPerMin <= 0 {
		return true
	}
	min := now.Unix() / 60
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.m[key]
	if w == nil || w.minute != min {
		w = &window{minute: min}
		l.m[key] = w
	}
	if w.count >= limitPerMin {
		return false
	}
	w.count++
	return true
}
```

- [ ] **Step 4: run** → PASS.
- [ ] **Step 5: commit** `feat(policy): in-memory fixed-window rate limiter`.

---

### Task 5: Blocking approval queue

**Files:** Create `internal/approval/queue.go`, `internal/approval/queue_test.go`.

**Interfaces:**
- Produces:
  - `approval.DefaultTimeout = 5 * time.Minute`.
  - `approval.Pending struct { ID, AgentID, UpstreamID, Method, Path, Purpose string; CreatedAt time.Time }`
  - `approval.NewQueue() *approval.Queue` (uses DefaultTimeout) / `approval.NewQueueWithTimeout(d time.Duration) *Queue`.
  - `(*Queue).Submit(ctx context.Context, p Pending) (approved bool, err error)` — registers a
    pending entry, blocks until `Resolve` is called, the timeout elapses (⇒ `false, nil`), or
    `ctx` is canceled (⇒ `false, ctx.Err()`). The entry's `ID` is generated inside Submit.
  - `(*Queue).List() []Pending` — snapshot of currently-waiting entries.
  - `(*Queue).Resolve(id string, approve bool) error` — `ErrNotFound` if no such pending entry.
  - `approval.ErrNotFound`.

- [ ] **Step 1: failing test** — `internal/approval/queue_test.go`:
```go
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
```

- [ ] **Step 2: run** → FAIL.

- [ ] **Step 3: implement** — `internal/approval/queue.go`:
```go
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

func NewQueue() *Queue { return NewQueueWithTimeout(DefaultTimeout) }

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
```

- [ ] **Step 4: run** `go test ./internal/approval/` → PASS.
- [ ] **Step 5: commit** `feat(approval): blocking approval queue (submit/list/resolve, 5m timeout)`.

---

### Task 6: OIDC client-credentials authenticator + Manager

**Files:** Modify `internal/upstream/registry.go` (AuthConfig fields), `internal/authn/authn.go` (For case + Manager); create `internal/authn/oidc.go`, `internal/authn/oidc_test.go`.

**Interfaces:**
- `upstream.AuthConfig` gains: `TokenURL, ClientID, ClientSecret, Scope string` (json tags
  `token_url,omitempty` etc.). Type `"oidc-client-credentials"`.
- `authn.For(cfg)` gains a case `"oidc-client-credentials"` returning an authenticator that
  holds its own token cache (so a single instance, reused across requests, caches the token).
- `authn.Manager` caches one authenticator per upstream ID so OIDC tokens persist across
  requests:
  - `authn.NewManager(hc *http.Client) *authn.Manager` (nil ⇒ `http.DefaultClient`).
  - `(*Manager).Authenticator(up *upstream.Upstream) (Authenticator, error)` — returns a cached
    instance keyed by `up.ID`; rebuilds if the upstream's auth fingerprint changed.

- [ ] **Step 1: failing test** — `internal/authn/oidc_test.go`:
```go
package authn

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/upstream"
)

func TestOIDCClientCredentialsFetchesAndCaches(t *testing.T) {
	var tokenCalls int32
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenCalls, 1)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "client_credentials", r.PostForm.Get("grant_type"))
		require.Equal(t, "cid", r.PostForm.Get("client_id"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "AT123", "token_type": "Bearer", "expires_in": 3600})
	}))
	defer idp.Close()

	mgr := NewManager(idp.Client())
	up := &upstream.Upstream{ID: "u1", Auth: upstream.AuthConfig{
		Type: "oidc-client-credentials", TokenURL: idp.URL, ClientID: "cid", ClientSecret: "sec", Scope: "api",
	}}
	a, err := mgr.Authenticator(up)
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest(http.MethodGet, "https://api", nil)
		require.NoError(t, a.Apply(req))
		require.Equal(t, "Bearer AT123", req.Header.Get("Authorization"))
	}
	require.Equal(t, int32(1), atomic.LoadInt32(&tokenCalls)) // cached, not re-fetched
}
```

- [ ] **Step 2: run** → FAIL.

- [ ] **Step 3: implement** — add fields to `upstream.AuthConfig`:
```go
	// OIDC client-credentials:
	TokenURL     string `json:"token_url,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	Scope        string `json:"scope,omitempty"`
```
Then `internal/authn/oidc.go`:
```go
package authn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Sipaha/outwall/internal/upstream"
)

type oidcClientCreds struct {
	hc       *http.Client
	tokenURL string
	clientID string
	secret   string
	scope    string

	mu      sync.Mutex
	token   string
	expires time.Time
}

// earlyRefresh refreshes a bit before actual expiry to avoid races at the boundary.
const earlyRefresh = 30 * time.Second

func (o *oidcClientCreds) Apply(r *http.Request) error {
	tok, err := o.fetch(r.Context())
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

func (o *oidcClientCreds) fetch(ctx context.Context) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.token != "" && time.Now().Before(o.expires.Add(-earlyRefresh)) {
		return o.token, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {o.clientID},
		"client_secret": {o.secret},
	}
	if o.scope != "" {
		form.Set("scope", o.scope)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("oidc token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("oidc token fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oidc token endpoint: status %d", resp.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("oidc token decode: %w", err)
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("oidc token endpoint returned empty access_token")
	}
	o.token = body.AccessToken
	ttl := time.Duration(body.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = time.Minute // conservative default when expires_in absent
	}
	o.expires = time.Now().Add(ttl)
	return o.token, nil
}

// Manager caches one Authenticator per upstream so OIDC tokens persist across requests.
type Manager struct {
	hc  *http.Client
	mu  sync.Mutex
	m   map[string]managed
}

type managed struct {
	fingerprint string
	auth        Authenticator
}

func NewManager(hc *http.Client) *Manager {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Manager{hc: hc, m: map[string]managed{}}
}

func fingerprint(c upstream.AuthConfig) string {
	return strings.Join([]string{c.Type, c.Header, c.Token, c.Username, c.Password,
		c.TokenURL, c.ClientID, c.ClientSecret, c.Scope}, "\x00")
}

// Authenticator returns a cached authenticator for the upstream, rebuilding it if the auth
// config changed since last time.
func (mgr *Manager) Authenticator(up *upstream.Upstream) (Authenticator, error) {
	fp := fingerprint(up.Auth)
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if cur, ok := mgr.m[up.ID]; ok && cur.fingerprint == fp {
		return cur.auth, nil
	}
	a, err := mgr.build(up.Auth)
	if err != nil {
		return nil, err
	}
	mgr.m[up.ID] = managed{fingerprint: fp, auth: a}
	return a, nil
}

func (mgr *Manager) build(cfg upstream.AuthConfig) (Authenticator, error) {
	if cfg.Type == "oidc-client-credentials" {
		if cfg.TokenURL == "" || cfg.ClientID == "" {
			return nil, fmt.Errorf("oidc-client-credentials: token_url and client_id required")
		}
		return &oidcClientCreds{hc: mgr.hc, tokenURL: cfg.TokenURL, clientID: cfg.ClientID,
			secret: cfg.ClientSecret, scope: cfg.Scope}, nil
	}
	return For(cfg) // stateless types
}
```
Also add the `"oidc-client-credentials"` case to `For` so a stateless caller still works
(returns a fresh, non-shared instance — caching only happens via Manager):
```go
	case "oidc-client-credentials":
		if cfg.Header != "" { /* unused */ }
		return &oidcClientCreds{hc: http.DefaultClient, tokenURL: cfg.TokenURL,
			clientID: cfg.ClientID, secret: cfg.ClientSecret, scope: cfg.Scope}, nil
```
(Add `"net/http"` to `authn.go` imports.)

- [ ] **Step 4: run** `go test ./internal/authn/` → PASS.
- [ ] **Step 5: commit** `feat(authn): OIDC client-credentials authenticator + per-upstream Manager cache`.

---

### Task 7: Wire policy/approval/limiter/auth-manager into the proxy

**Files:** Modify `internal/proxy/proxy.go`, `internal/proxy/proxy_test.go`.

**Interfaces:**
- `proxy.Deps` changes: drop `Grants *grant.Registry`; add
  `Policy *policy.Registry`, `Limiter *policy.Limiter`, `Approvals *approval.Queue`,
  `AuthManager *authn.Manager`. Keep `Agents`, `Upstreams`, `Vault`, `Logger`.
- New flow (replacing the grant check + `authn.For`):
  1. vault locked → 503; bearer/token → 401; upstream → 404 (unchanged).
  2. `dec, err := Policy.Decide(policy.Input{AgentID: ag.ID, UpstreamID: up.ID, Method: r.Method, Path: "/"+rest})`.
     - on `Deny` → 403 `access denied`.
     - on `RequireApproval` → `ok, err := Approvals.Submit(r.Context(), approval.Pending{...})`;
       `!ok` → 403 `not approved` (or 504 on `ctx`/timeout — use 403 for simplicity, the body
       message distinguishes); `ok` → continue.
     - on `Allow` → continue.
  3. If `dec.Rule != nil && dec.Rule.RateLimitPerMin > 0`: `if !Limiter.Allow(ag.ID+"|"+dec.Rule.ID, dec.Rule.RateLimitPerMin, time.Now()) → 429 rate limited`.
  4. `auth, err := AuthManager.Authenticator(up)` instead of `authn.For(up.Auth)`. Rest unchanged.
- Note the path passed to Decide/MatchGlob is `"/"+rest` (upstream-relative, leading slash).

- [ ] **Step 1: update the proxy test** — replace grant usage in `proxy_test.go`'s `build()`
  with the new deps, and add cases. New `build()` returns the pieces the tests need:
```go
func build(t *testing.T) (http.Handler, *agent.Registry, *upstream.Registry, *policy.Registry, *approval.Queue, *secret.Vault) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	v := secret.NewVault(s)
	require.NoError(t, v.Init("pw"))
	ag := agent.NewRegistry(s)
	up := upstream.NewRegistry(s, v)
	pol := policy.NewRegistry(s)
	appr := approval.NewQueueWithTimeout(2 * time.Second)
	h := New(Deps{Agents: ag, Upstreams: up, Policy: pol, Limiter: policy.NewLimiter(),
		Approvals: appr, AuthManager: authn.NewManager(nil), Vault: v})
	return h, ag, up, pol, appr, v
}
```
  Update `TestProxyHappyPathInjectsAuthAndStripsAgentToken` to create an allow rule instead of
  a grant:
```go
	_, err = pol.Create(policy.Rule{UpstreamID: u.ID, Method: "*", PathGlob: "/**", Outcome: policy.Allow})
	require.NoError(t, err)
```
  Update `TestProxyGuards` similarly (default-deny still 403 before any rule; add the rule
  before the vault-lock check). Then add:
```go
func TestProxyRequireApprovalBlocksUntilResolved(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()
	h, ag, up, pol, appr, _ := build(t)
	_, token, _ := ag.Register("claude")
	u, _ := up.Create("be", backend.URL, upstream.AuthConfig{Type: "none"})
	_, err := pol.Create(policy.Rule{UpstreamID: u.ID, Method: "*", PathGlob: "/**", Outcome: policy.RequireApproval})
	require.NoError(t, err)

	go func() {
		require.Eventually(t, func() bool { return len(appr.List()) == 1 }, time.Second, 10*time.Millisecond)
		_ = appr.Resolve(appr.List()[0].ID, true)
	}()
	w := do(t, h, http.MethodGet, "/be/x", token)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "ok", w.Body.String())
}

func TestProxyRateLimit(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()
	h, ag, up, pol, _, _ := build(t)
	_, token, _ := ag.Register("claude")
	u, _ := up.Create("be", backend.URL, upstream.AuthConfig{Type: "none"})
	_, err := pol.Create(policy.Rule{UpstreamID: u.ID, Method: "*", PathGlob: "/**", Outcome: policy.Allow, RateLimitPerMin: 1})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, do(t, h, http.MethodGet, "/be/x", token).Code)
	require.Equal(t, http.StatusTooManyRequests, do(t, h, http.MethodGet, "/be/x", token).Code)
}
```

- [ ] **Step 2: run** `go test ./internal/proxy/` → FAIL (Deps mismatch).

- [ ] **Step 3: implement** the new flow in `proxy.go` (see Interfaces). Replace the `Grants`
  field and the grant/`authn.For` block; add imports `time`, `internal/policy`,
  `internal/approval`, `internal/authn` (authn already imported — keep), drop `internal/grant`.
  Decision block:
```go
	dec, err := h.Policy.Decide(policy.Input{AgentID: ag.ID, UpstreamID: up.ID, Method: r.Method, Path: "/" + rest})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "policy error")
		return
	}
	switch dec.Outcome {
	case policy.Deny:
		writeErr(w, http.StatusForbidden, "access denied")
		return
	case policy.RequireApproval:
		ok, err := h.Approvals.Submit(r.Context(), approval.Pending{
			AgentID: ag.ID, UpstreamID: up.ID, Method: r.Method, Path: "/" + rest,
		})
		if err != nil {
			writeErr(w, http.StatusGatewayTimeout, "approval wait canceled")
			return
		}
		if !ok {
			writeErr(w, http.StatusForbidden, "request not approved")
			return
		}
	}
	if dec.Rule != nil && dec.Rule.RateLimitPerMin > 0 {
		if !h.Limiter.Allow(ag.ID+"|"+dec.Rule.ID, dec.Rule.RateLimitPerMin, time.Now()) {
			writeErr(w, http.StatusTooManyRequests, "rate limited")
			return
		}
	}
	auth, err := h.AuthManager.Authenticator(up)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "auth config error")
		return
	}
```

- [ ] **Step 4: run** `go test ./internal/proxy/` → PASS.
- [ ] **Step 5: commit** `feat(proxy): policy decisions + blocking approval + rate limit + OIDC auth manager`.

---

### Task 8: Daemon wiring + admin API (rules/approvals; drop grants) + CLI

**Files:** Modify `internal/daemon/daemon.go`, `internal/daemon/admin.go`, `internal/daemon/admin_test.go`, `internal/cli/root.go`; rename `internal/cli/grant.go` → `internal/cli/rule.go`; create `internal/cli/approval.go`; **delete `internal/grant/`**.

**Interfaces / behavior:**
- `daemon.New` builds `policy.NewRegistry`, `policy.NewLimiter()`, `approval.NewQueue()`,
  `authn.NewManager(nil)`; passes them into `proxy.New`. Remove the `grants` field.
- Admin endpoints — remove `POST /grants`; add:
  - `POST /rules {subject_agent_id?, upstream_id, method?, path_glob?, outcome, rate_limit_per_min?}` → `{id}`
  - `GET /rules` → `[{id, subject_agent_id, upstream_id, method, path_glob, outcome, rate_limit_per_min}]`
  - `DELETE /rules/{id}` → `{ok:true}`
  - `GET /approvals` → `[{id, agent_id, upstream_id, method, path, purpose, created_at}]`
  - `POST /approvals/{id}/resolve {approve}` → `{ok:true}` / 404 on unknown id
- CLI — remove `outwall grant`; add `outwall rule add|list|delete` and `outwall approval list|resolve`:
  - `rule add --upstream <id> [--agent <id>] [--method GET|*] [--path '/repos/**'] [--rate 60] --outcome allow|deny|require-approval`
  - `rule list`, `rule delete <id>`
  - `approval list`, `approval resolve <id> --approve|--deny`
- Wire the new commands in `root.go` (`newRuleCmd(gf)`, `newApprovalCmd(gf)`; drop `newGrantCmd`).

- [ ] **Step 1: update `admin_test.go`** — drop the grant assertion; add a rules+approvals flow:
```go
func TestAdminRulesAndApprovals(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	// create an upstream + a rule.
	wu := req(t, h, "POST", "/upstreams", `{"name":"gh","base_url":"https://api.github.com","auth":{"type":"none"}}`)
	require.Equal(t, http.StatusOK, wu.Code)
	var up map[string]string
	require.NoError(t, json.Unmarshal(wu.Body.Bytes(), &up))

	wr := req(t, h, "POST", "/rules", `{"upstream_id":"`+up["id"]+`","method":"*","path_glob":"/**","outcome":"allow"}`)
	require.Equal(t, http.StatusOK, wr.Code, wr.Body.String())

	wl := req(t, h, "GET", "/rules", "")
	require.Equal(t, http.StatusOK, wl.Code)
	require.Contains(t, wl.Body.String(), up["id"])

	// resolving an unknown approval → 404.
	require.Equal(t, http.StatusNotFound, req(t, h, "POST", "/approvals/nope/resolve", `{"approve":true}`).Code)
}
```
  (Add `"encoding/json"` to the test imports.)

- [ ] **Step 2: run** `go test ./internal/daemon/` → FAIL.

- [ ] **Step 3: implement** the daemon + admin + CLI changes (mirror the Plan 1 handler/CLI
  style; `DELETE /rules/{id}` and `POST /approvals/{id}/resolve` use Go 1.22 path wildcards:
  `mux.HandleFunc("DELETE /rules/{id}", ...)` with `r.PathValue("id")`). Delete `internal/grant/`.
  For `/approvals/{id}/resolve`, map `approval.ErrNotFound` → 404.

- [ ] **Step 4: run** `go test ./...` → PASS (whole tree, since proxy/daemon/store all changed).

- [ ] **Step 5: full gate + commit**
```bash
gofmt -l .            # empty
go vet ./...
go test ./... > /tmp/outwall_plan2.txt 2>&1 ; grep -E "FAIL|panic|^ok" /tmp/outwall_plan2.txt
make build
git add -A && git commit -m "feat: rules/approvals admin API + CLI; remove grant package"
```

- [ ] **Step 6: end-to-end smoke** — start daemon, init vault, add upstream, register agent,
  `rule add --upstream <id> --outcome require-approval`, fire a proxied request in the
  background, `approval list` (see it pending), `approval resolve <id> --approve`, confirm the
  background request returns the upstream response. Then a `--outcome allow --rate 1` rule →
  second request within the minute returns 429.

---

## Self-Review

- **Spec coverage:** policy rules with subject/upstream/method/path-glob (Tasks 1–3) ✓;
  rate limit (Task 4) ✓; require-approval blocking (Task 5, wired Task 7) ✓; OIDC
  client-credentials with cache/refresh (Task 6) ✓; precedence agent>any>default-deny,
  most-restrictive (Task 3) ✓; grant removed, no legacy path (Tasks 2, 8) ✓.
- **Deferred (later plans):** MCP control plane (Plan 3), audit (Plan 4), control API+SSE
  (Plan 5), web UI (Plan 6), Wails (Plan 7); OIDC authorization-code + body filters are Phase 2.
- **Type consistency:** `policy.Decide(Input)→Decision{Outcome,Rule}`, `approval.Submit(ctx,
  Pending)→(bool,error)`, `authn.Manager.Authenticator(*upstream.Upstream)→(Authenticator,error)`,
  `Limiter.Allow(key,limit,now)→bool` — used consistently in proxy/daemon.

## Module docs to update at finalize

`docs/architecture/modules/policy.md`, `approval.md` (new); update `authn.md`, `proxy.md`,
`store.md`, `upstream.md`, `daemon.md`; delete `grant.md`.
