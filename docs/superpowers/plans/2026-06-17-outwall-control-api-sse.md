# outwall — Plan 5: Control API + SSE

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development or superpowers:executing-plans.

**Goal:** Give the desktop UI a single localhost surface: the existing admin API plus a live
**SSE** event stream, served over a localhost TCP bind (`UIListen`, default `127.0.0.1:8182`)
gated by a CSRF header. An in-process event **bus** publishes domain events — agent registered,
approval enqueued/resolved, access requested, audit recorded, vault lock/unlock, upstream/rule
created — which the SSE endpoint fans out to connected clients (the web UI in Plan 6, the Wails
wrapper in Plan 7).

**Architecture:** A new `internal/events` package provides a goroutine-safe pub/sub `Bus` and a
small `Publisher` interface. The bus is injected (nil-safe) into `approval.Queue` and
`audit.Recorder` (they publish on enqueue/resolve and on record), and held by the daemon, whose
admin handlers publish on create/register/unlock. The daemon serves the admin mux + a
`GET /events` SSE handler on `UIListen` behind a CSRF-header check; the Unix socket admin
transport is unchanged (CLI). Localhost desktop only — no auth beyond the CSRF header + the
loopback bind (documented in ADR-0005; matches the single-tenant-host model).

**Tech Stack:** Plan 1–4 stack (no new deps; SSE is stdlib `net/http` flushing).

## Global Constraints

(All prior constraints apply.) Plus:
- SSE framing: `event: <type>\n` + `data: <json>\n\n`, flushed per event; send a `: ping\n\n`
  comment heartbeat every 25s to keep the connection alive.
- CSRF gate on `UIListen`: requests to `/api/**` and `/events` must carry header
  `X-Outwall-CSRF: 1` (any non-empty value), else 403. (Defeats browser cross-origin form posts;
  it is NOT authentication — loopback + single-tenant host is the trust boundary. ADR-0005.)
- The event bus must never block a publisher: a slow/full subscriber drops events (bounded
  per-subscriber buffer), it does not back-pressure the caller.

## File Structure

```
Create:  internal/events/bus.go            # Bus (pub/sub), Event, Publisher interface
         internal/events/bus_test.go
         internal/daemon/sse.go             # GET /events SSE handler
         internal/daemon/sse_test.go
Modify:  internal/approval/queue.go         # optional Publisher: publish enqueued/resolved
         internal/approval/queue_test.go
         internal/audit/recorder.go         # optional Publisher: publish recorded
         internal/daemon/daemon.go           # build bus; inject into approval+audit; UIListen listener; Config.UIListen
         internal/daemon/admin.go            # CSRF middleware on the TCP mux; publish on register/create/unlock; mount /events
         internal/daemon/admin_test.go
         internal/cli/root.go                # --ui-listen flag
```

---

### Task 1: events bus

**Files:** Create `internal/events/bus.go`, `internal/events/bus_test.go`.

**Interfaces:**
- `events.Event struct { Type string; Data any; TS time.Time }`
- `events.Publisher interface { Publish(eventType string, data any) }`
- `events.NewBus() *events.Bus` — implements `Publisher`.
- `(*Bus).Publish(eventType string, data any)` — stamps `TS=now`, fans out non-blocking to all subscribers (drop if a subscriber's buffer is full).
- `(*Bus).Subscribe() (<-chan Event, func())` — returns a buffered channel (cap 64) and a cancel func that unsubscribes + closes draining.
- Goroutine-safe; `Subscribe`/cancel/`Publish` may race.

- [ ] **Step 1: failing test** — `internal/events/bus_test.go`:
```go
package events

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPublishFanOut(t *testing.T) {
	b := NewBus()
	ch1, cancel1 := b.Subscribe()
	ch2, cancel2 := b.Subscribe()
	defer cancel1()
	defer cancel2()

	b.Publish("agent.registered", map[string]string{"id": "a1"})

	for _, ch := range []<-chan Event{ch1, ch2} {
		select {
		case e := <-ch:
			require.Equal(t, "agent.registered", e.Type)
			require.False(t, e.TS.IsZero())
		case <-time.After(time.Second):
			t.Fatal("no event delivered")
		}
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	b := NewBus()
	ch, cancel := b.Subscribe()
	cancel()
	b.Publish("x", nil) // must not panic or block
	select {
	case _, ok := <-ch:
		require.False(t, ok) // channel closed by cancel
	case <-time.After(time.Second):
		// also acceptable: nothing delivered
	}
}

func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	b := NewBus()
	_, cancel := b.Subscribe() // never drained
	defer cancel()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			b.Publish("flood", i) // must not block on the full subscriber
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
}
```

- [ ] **Step 2: run** → FAIL. **Step 3: implement** `bus.go`:
```go
// Package events is an in-process pub/sub bus for domain events surfaced to the UI over SSE.
package events

import (
	"sync"
	"time"
)

type Event struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
	TS   time.Time `json:"ts"`
}

// Publisher publishes domain events.
type Publisher interface {
	Publish(eventType string, data any)
}

const subBuffer = 64

type Bus struct {
	mu   sync.Mutex
	subs map[int]chan Event
	next int
}

func NewBus() *Bus { return &Bus{subs: map[int]chan Event{}} }

func (b *Bus) Publish(eventType string, data any) {
	e := Event{Type: eventType, Data: data, TS: time.Now().UTC()}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default: // subscriber buffer full → drop, never block
		}
	}
}

func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, subBuffer)
	b.mu.Lock()
	id := b.next
	b.next++
	b.subs[id] = ch
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, id)
			close(ch)
			b.mu.Unlock()
		})
	}
	return ch, cancel
}
```

- [ ] **Step 4: run** `go test -race ./internal/events/` → PASS. **Step 5: commit**
  `feat(events): in-process pub/sub bus`.

---

### Task 2: publishers in approval + audit; SSE endpoint

**Files:** Modify `internal/approval/queue.go`, `internal/approval/queue_test.go`, `internal/audit/recorder.go`; create `internal/daemon/sse.go`, `internal/daemon/sse_test.go`.

**Interfaces:**
- `approval.Queue` gains an optional publisher: add `(*Queue).SetPublisher(p events.Publisher)`
  (nil-safe). `Submit` publishes `approval.enqueued` with `{id, agent_id, upstream_id, method,
  path, purpose}` right after registering the waiter; `Resolve` publishes `approval.resolved`
  with `{id, approved}`. (Use `SetPublisher` rather than changing the constructor signature so
  existing `NewQueue`/`NewQueueWithTimeout` callers/tests are untouched.)
- `audit.Recorder` gains `(*Recorder).SetPublisher(p events.Publisher)`; `Record` publishes
  `audit.recorded` with `{id, agent_name, upstream_name, method, path, status_code}` after a
  successful insert.
- `daemon.sseHandler(bus *events.Bus) http.HandlerFunc` — subscribes, writes SSE frames per
  event, 25s heartbeat, returns on client disconnect (`r.Context().Done()`); requires
  `http.Flusher` (503 if unsupported).

- [ ] **Step 1: failing test (approval publish)** — add to `internal/approval/queue_test.go`:
```go
type capturePub struct{ mu sync.Mutex; types []string }

func (c *capturePub) Publish(t string, _ any) { c.mu.Lock(); c.types = append(c.types, t); c.mu.Unlock() }

func TestSubmitPublishesEnqueued(t *testing.T) {
	q := NewQueueWithTimeout(100 * time.Millisecond)
	cp := &capturePub{}
	q.SetPublisher(cp)
	_, _ = q.Submit(context.Background(), Pending{AgentID: "a1"}) // times out, but should have published enqueued
	cp.mu.Lock()
	defer cp.mu.Unlock()
	require.Contains(t, cp.types, "approval.enqueued")
}
```
  (add `"sync"` to the test imports.)

- [ ] **Step 2: failing test (SSE)** — `internal/daemon/sse_test.go`:
```go
package daemon

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/events"
)

func TestSSEStreamsEvents(t *testing.T) {
	bus := events.NewBus()
	srv := httptest.NewServer(sseHandler(bus))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	// Give the handler a moment to subscribe, then publish.
	time.Sleep(50 * time.Millisecond)
	bus.Publish("agent.registered", map[string]string{"id": "a1"})

	sc := bufio.NewScanner(resp.Body)
	var sawType, sawData bool
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event: agent.registered") {
			sawType = true
		}
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, "a1") {
			sawData = true
			break
		}
	}
	require.True(t, sawType)
	require.True(t, sawData)
}
```

- [ ] **Step 3: run** → FAIL. **Step 4: implement** `SetPublisher` on both + the publishes, and
  `sse.go`:
```go
package daemon

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Sipaha/outwall/internal/events"
)

func sseHandler(bus *events.Bus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		ch, cancel := bus.Subscribe()
		defer cancel()
		ping := time.NewTicker(25 * time.Second)
		defer ping.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ping.C:
				_, _ = w.Write([]byte(": ping\n\n"))
				flusher.Flush()
			case e, ok := <-ch:
				if !ok {
					return
				}
				data, _ := json.Marshal(e.Data)
				_, _ = w.Write([]byte("event: " + e.Type + "\ndata: "))
				_, _ = w.Write(data)
				_, _ = w.Write([]byte("\n\n"))
				flusher.Flush()
			}
		}
	}
}
```

- [ ] **Step 5: run** `go test -race ./internal/approval/ ./internal/daemon/` → PASS.
- [ ] **Step 6: commit** `feat(events,daemon): approval/audit publishers + SSE endpoint`.

---

### Task 3: daemon wiring + CSRF + UIListen + CLI

**Files:** Modify `internal/daemon/daemon.go`, `internal/daemon/admin.go`, `internal/daemon/admin_test.go`, `internal/cli/root.go`.

**Behavior:**
- `daemon.Config` gains `UIListen string` (default `127.0.0.1:8182`). `daemon.New` builds
  `events.NewBus()`, calls `approvalQueue.SetPublisher(bus)` and `auditRecorder.SetPublisher(bus)`,
  stores the bus. Admin handlers publish after success: `hAgentRegister` → `agent.registered
  {id,name}`; `hUpstreamCreate` → `upstream.created {id,name}`; `hRuleCreate` → `rule.created
  {id}`; `hVaultUnlock` → `vault.unlocked`; `access.Create` (in the MCP path) already happens
  off the admin API — also publish `access.requested` from the MCP service via the bus
  (inject the publisher into `mcpsvc` OR publish from the access registry; pick one and note it).
  Minimum required publishes for Plan 5: `agent.registered`, `upstream.created`, `rule.created`,
  `vault.unlocked`, plus `approval.*` and `audit.recorded` (already wired in Task 2).
- `daemon.Serve` starts a **fourth** listener on `UIListen` serving a TCP mux = the admin mux
  wrapped in `csrfMiddleware` + `GET /events` (also CSRF-gated). The Unix-socket admin listener
  stays CSRF-free (local CLI). Factor the route table so both transports share handlers.
- `csrfMiddleware`: if `X-Outwall-CSRF` header is empty → 403 `{"error":"missing csrf header"}`.
- CLI: `--ui-listen` persistent flag wired into `daemon.Config` in `serve`.

- [ ] **Step 1: failing test** — add to `internal/daemon/admin_test.go` (CSRF on the TCP mux):
```go
func TestUICSRFGate(t *testing.T) {
	d := newDaemon(t)
	h := d.UIHandler() // the CSRF-wrapped TCP mux
	// no CSRF header → 403
	r1 := httptest.NewRequest("GET", "/vault/status", nil)
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, r1)
	require.Equal(t, http.StatusForbidden, w1.Code)
	// with CSRF header → passes through (200)
	r2 := httptest.NewRequest("GET", "/vault/status", nil)
	r2.Header.Set("X-Outwall-CSRF", "1")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)
}
```
  (Expose `(*Daemon).UIHandler() http.Handler` returning the CSRF-wrapped mux incl. `/events`.)

- [ ] **Step 2: run** → FAIL. **Step 3: implement** `UIHandler()` (csrf-wrap the admin mux +
  mount `/events`), the `UIListen` listener in `Serve`, the publishes, and the CLI flag. Refactor
  `AdminHandler` so the route registration is shared between the socket mux and the UI mux.
- [ ] **Step 4: run** `go test ./...` → PASS.
- [ ] **Step 5: full gate + commit**
```bash
gofmt -l . ; go vet ./... ; go test -race -count=1 ./... > /tmp/outwall_plan5.txt 2>&1 ; grep -E "FAIL|DATA RACE|^ok" /tmp/outwall_plan5.txt ; make build
git add -A && git commit -m "feat: control-API UI listener (CSRF) + SSE + event publishes"
```
- [ ] **Step 6: e2e smoke** — start the daemon; `curl -N -H 'X-Outwall-CSRF: 1'
  http://127.0.0.1:8182/events &` then register an agent via the admin socket and confirm an
  `event: agent.registered` frame arrives on the curl stream. Note any limitation.

---

## Self-Review

- **Spec coverage:** consolidated UI-facing API (Task 3) ✓; SSE live events for new agents /
  approvals / access / audit / vault (Tasks 1–3) ✓; localhost bind + CSRF gate matching the
  single-tenant model (Task 3) ✓; non-blocking bus so domain paths never stall on a slow UI
  (Task 1) ✓.
- **Deferred:** the web UI itself (Plan 6) and the Wails wrapper (Plan 7) consume this; bearer/
  token auth on the TCP bind (Phase 2 / server-mode) — loopback + CSRF only for now.
- **Type consistency:** `events.Publisher{Publish(string, any)}`, `Bus.Subscribe()→(<-chan
  Event, func())`, `Queue.SetPublisher`, `Recorder.SetPublisher`, `Daemon.UIHandler()`,
  `Config.UIListen`.

## ADR + module docs (finalize)

ADR-0005 (implementer writes it): the control-API + SSE design — in-process non-blocking bus
(drop-on-full, no back-pressure), the event taxonomy, SSE framing + heartbeat, the `UIListen`
loopback TCP bind + `X-Outwall-CSRF` gate as a CSRF-not-auth boundary (single-tenant host;
token auth deferred to server mode), publisher injection via `SetPublisher`. Module doc
`events.md` (new); update `daemon.md`, `approval.md`, `audit.md`.
