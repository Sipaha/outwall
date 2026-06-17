# ADR-0005: Control API + SSE event stream

- **Status:** accepted
- **Date:** 2026-06-17

## Context

The desktop UI (web frontend in Plan 6, Wails wrapper in Plan 7) needs two things from the
daemon: the existing admin API (vault, upstreams, agents, rules, approvals, access-requests,
audit) reachable from a browser, and a **live** feed of what is happening — an agent
registering, an approval enqueuing/resolving, an access request arriving, an audit row being
written, the vault unlocking. Polling the admin API for liveness is wasteful and laggy.

Constraints: localhost desktop, single-tenant host (one operator owns the machine). The admin
API is currently served only over a `0600` unix socket for the CLI — a browser cannot reach a
unix socket. We need a TCP surface, but a TCP bind is reachable by any process/origin on the
host, including a malicious web page in the operator's browser issuing cross-origin form posts.
We also must not let a slow or stuck UI client stall the domain paths (a publish on the request
hot path must never block).

## Decision

**In-process event bus (`internal/events`).** A `Bus` with `Publish(eventType string, data any)`
and `Subscribe() (<-chan Event, func())`. `Event{Type string; Data any; TS time.Time}`, JSON
tags `type`/`data`/`ts`, `TS` stamped at publish. Each subscriber has a bounded buffer (cap 64);
`Publish` fans out under a mutex and **drops on a full buffer** — no back-pressure, ever. The
bus implements `events.Publisher`. This is a deliberate liveness-over-completeness choice: the
UI is a live view, not a durable log (the audit DB is the durable record); a dropped UI event is
acceptable, a stalled approval/proxy goroutine is not.

**Publisher injection via `SetPublisher` (nil-safe).** `approval.Queue`, `audit.Recorder`, and
`mcpsvc.Service` each gain a `SetPublisher(events.Publisher)` rather than a changed constructor
signature, so existing callers/tests stay untouched and publishing is opt-in. `daemon.New`
builds one `events.NewBus()` and injects it into all three plus holds it on the daemon.

**Event taxonomy.** `agent.registered {id,name}`, `upstream.created {id,name}`,
`rule.created {id}`, `vault.unlocked {}` (from admin handlers); `approval.enqueued
{id,agent_id,upstream_id,method,path,purpose}` and `approval.resolved {id,approved}` (from the
queue); `audit.recorded {id,agent_name,upstream_name,method,path,status_code}` (from the
recorder); `access.requested {id,agent_id,upstream_id,upstream_name,purpose}` (from `mcpsvc`).

**`access.requested` is published from `mcpsvc.Service.RequestAccess`**, not from a daemon admin
handler, because the access-request `Create` happens on the MCP control-plane path, not the
admin API. Injecting the bus into the one place that creates the record (the service) is cleaner
than reaching back from the daemon, and it keeps the publish adjacent to the state change.

**SSE endpoint.** `GET /events` (`daemon/sse.go`): sets `Content-Type: text/event-stream`,
subscribes to the bus, writes `event: <type>\ndata: <json>\n\n` per event flushed immediately,
sends a `: ping\n\n` comment heartbeat every 25s, and returns on client disconnect
(`r.Context().Done()`). Requires `http.Flusher` (503 if the transport cannot stream).

**`UIListen` TCP bind + `X-Outwall-CSRF` gate.** `Serve` starts a fourth listener on `UIListen`
(default `127.0.0.1:8182`) serving `UIHandler()` = the shared admin route table (`adminMux`)
plus `/events`, wrapped in `csrfMiddleware`. Any request lacking a non-empty `X-Outwall-CSRF`
header → 403. The unix-socket transport (`AdminHandler`) stays CSRF-free for the CLI. The CSRF
header is a **CSRF-not-auth** boundary: it defeats browser cross-origin form posts (which cannot
set custom headers without a CORS preflight the daemon never approves), but it is **not
authentication**. The trust boundary is the loopback bind + single-tenant host model. Bearer-
token auth on the TCP bind is deferred to a future multi-user/server mode.

## Alternatives considered

- **Serve the admin API over the existing unix socket to the UI** — a browser cannot open a unix
  socket; the Wails/web UI needs TCP. Rejected.
- **WebSocket instead of SSE** — bidirectional, but the feed is one-way (daemon → UI) and SSE is
  plain `net/http` flushing with auto-reconnect built into `EventSource`, no new dependency.
  Rejected for this one-way feed.
- **Blocking / unbounded bus** — a slow UI client would back-pressure the proxy/approval hot
  paths or grow memory without bound. Rejected; drop-on-full is the explicit trade-off.
- **Real authentication on the TCP bind now** (bearer token, OS-keyring) — overkill for the
  single-tenant localhost model and a larger design (token issuance/rotation). Deferred to
  server mode; loopback + CSRF is the documented interim boundary.

## Consequences

- The UI gets one consolidated localhost surface (admin API + live SSE) with no polling.
- Domain paths never stall on the UI; the cost is that a saturated/disconnected subscriber may
  miss events — acceptable because the audit DB is the durable record and the UI refetches lists
  on reconnect.
- `SetPublisher` keeps the bus optional, so unit tests of approval/audit/mcpsvc run without a bus
  and existing constructor callers are unchanged.
- The CSRF gate is explicitly **not auth**. If outwall ever exposes the control API beyond
  loopback or to multiple users, this must be revisited: add real auth on the TCP bind (a future
  ADR), at which point the CSRF gate becomes one layer rather than the boundary.
- Adding a new UI event is a one-line `Publish` at the state change plus a doc entry in the
  taxonomy; no SSE/transport change needed.
