# module: internal/events

An in-process pub/sub **event bus** for domain events surfaced to the desktop UI over SSE
(see `internal/daemon/sse.go`). The bus is the single fan-out point: domain code publishes,
the `GET /events` SSE handler subscribes, and connected UI clients receive the frames live.

The bus **never back-pressures a publisher**. Each subscriber gets a bounded buffer (cap 64);
when that buffer is full, `Publish` drops the event for that subscriber instead of blocking
(drop-on-full). This keeps domain paths — approval enqueue/resolve, audit record, admin
register/create/unlock — from stalling on a slow or stuck UI client. See ADR-0005.

## Public API

- `Event struct { Type string; Data any; TS time.Time }` — JSON-tagged (`type`, `data`, `ts`);
  `Publish` stamps `TS = time.Now().UTC()`.
- `Publisher interface { Publish(eventType string, data any) }` — the injection seam. Callers
  hold a `Publisher` (nil-safe: they guard with a nil check) so the bus can be wired in via
  `SetPublisher` without changing constructor signatures.
- `NewBus() *Bus` — `*Bus` implements `Publisher`.
- `(*Bus).Publish(eventType string, data any)` — non-blocking fan-out to all subscribers.
- `(*Bus).Subscribe() (<-chan Event, func())` — returns a buffered channel (cap 64) and a
  cancel func that unsubscribes and closes the channel; the cancel func is idempotent.

## Event taxonomy (Plan 5)

`agent.registered`, `upstream.created`, `rule.created`, `vault.unlocked`,
`approval.enqueued`, `approval.resolved`, `audit.recorded`, `access.requested`.
