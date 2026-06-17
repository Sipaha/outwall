# ADR-0004: Audit — request journal + body store

- **Status:** accepted
- **Date:** 2026-06-17

## Context

outwall must record every data-plane request/response so an operator can review what agents
did: which agent hit which upstream, the method/path/query, status, duration, sizes, the policy
decision (and matched rule), the agent's request headers (with credentials masked), and the
request/response bodies. Bodies can be large or binary, and the proxy streams them — it must not
buffer entire bodies in memory just to audit them, and it must not block streaming to the agent.
The injected upstream credential must never be persisted, and the agent's own bearer token must
not leak into the journal.

Constraints: pure-Go SQLite (`modernc.org/sqlite`, no CGO), no panics in library code, and the
audit recorder must be **optional** so the proxy keeps its Plans 1–3 behaviour when audit is off
(e.g. in unit tests with no recorder wired).

## Decision

A new `internal/audit` package owns two tables and a `Recorder`:

- `audit_log` — the fast-to-list journal: `id, ts, agent_id/name, upstream_id/name, method,
  path, query, status_code, duration_ms, req_bytes, resp_bytes, decision, rule_id, headers_json,
  error`, indexed by `ts`. `path` is the **upstream-relative** path (`/<rest>`); `query` is the
  raw query string. Masked headers are stored as a JSON object in `headers_json`.
- `audit_bodies` — `(log_id, kind, content_type, size, sha256, truncated, stored)` keyed by
  `(log_id, kind)` where `kind ∈ {request, response}`. Bodies live in a separate table so listing
  the journal never reads body blobs.

`Recorder` exposes `Record(Entry, ...Body)`, `List(limit)` (newest first, no bodies),
`Get(id) (Entry, []Body, error)` (`ErrNotFound` when absent), and `Prune(olderThan)` (deletes
bodies then log rows with `ts < olderThan`, returns the count).

**Streaming capped capture.** `NewCapture(src, cap, onClose)` returns an `io.ReadCloser` tee:
reads pass straight through to the consumer, at most `cap = BodyCap (256 KiB)` bytes are retained,
a running `total` counts every byte, and `onClose` fires once on `Close` with the retained bytes,
the total, and `truncated = total > cap`. No full body is ever buffered. `NewCaptureRef` is the
same tee but exposes the captured state via a `*Capture` handle (used for the request body, which
the proxy reads at response time rather than on Close — see timing below).

**Classification.** `ClassifyBody` stores body bytes only when the Content-Type is textual
(`text/*`, `application/json`, `application/xml`, `application/x-www-form-urlencoded`,
`application/*+json`, `application/*+xml`, or empty Content-Type on a non-empty body); otherwise
it keeps **metadata only** (`Stored = nil`) — content-type, declared/observed `size`, and the
sha256. **`sha256` is computed over the stored (captured, ≤ cap) bytes**, not the full body, and
only when there are stored bytes. This is a deliberate trade-off: a hash over a truncated/partial
capture, documented here so it is never mistaken for a whole-body digest.

**Masking.** `MaskHeaders` replaces the value of `Authorization`, `Proxy-Authorization`,
`Cookie`, `Set-Cookie`, and any header whose name contains `api-key`/`apikey`/`token`/`secret`
(case-insensitive) with `"***"`. The upstream credential is injected by the ReverseProxy
`Rewrite` step **after** the agent request is captured, so it never appears in the stored headers;
the mask is defence-in-depth.

**Record-on-response-close timing.** The proxy wraps the inbound request body with a capture
before forwarding and wraps the upstream response body in `ModifyResponse`. The audit row is
written from the **response capture's `onClose`** — i.e. after the response has fully streamed to
the agent and its body is closed — so `resp_bytes`/truncation reflect what the agent actually
received and recording never blocks the stream. The request body's captured bytes are read from
its `*Capture` handle at that same point (the request round-trip has completed by the time the
response body closes, so the bytes are stable — verified race-clean under `-race`). For upstream
transport failures the `ErrorHandler` records a minimal entry with `status 502` and the error
string.

**Early (non-proxied) outcomes.** Policy-decision outcomes are recorded inline before returning:
`deny → 403 decision "deny"`, `require-approval` denied `→ 403 decision "require-approval"`,
rate-limited `→ 429`. The pre-policy guards — `401` (missing/invalid token), `404` (unknown
upstream), `503` (vault locked) — are **not** recorded: they are not policy decisions and carry no
authenticated agent/upstream context worth journaling. (This choice is revisitable; it keeps the
journal focused on authorized-request activity.)

**Scope: data plane only.** This ADR covers the reverse-proxy data plane. MCP control-plane calls
(`request_access`, etc.) are **not** audited here; that is deferred.

`proxy.Deps` gains an optional `Audit *audit.Recorder`. When nil the proxy behaves exactly as in
Plans 1–3 (no capture, no recording). The daemon wires one recorder over the shared store.

## Alternatives considered

- **Buffer the whole body, then record.** Rejected: unbounded memory, defeats streaming, and
  blocks the agent on large/slow bodies.
- **Single table with body columns.** Rejected: every `List` would drag body blobs through the
  row scan; a separate `audit_bodies` table keeps listing cheap.
- **sha256 over the full body (re-read/stream-hash the untruncated stream).** Rejected for now:
  it reintroduces whole-body processing cost for binary payloads we deliberately don't store. The
  stored-bytes hash is enough to detect changes in the captured prefix; documented as such.
- **Record on request receipt (before the response).** Rejected: status, duration, and response
  size are unknown then; recording on response close captures the full outcome in one row.

## Consequences

- Listing the journal is fast (no blobs); a body fetch is a second targeted query.
- Memory is bounded to `2 × BodyCap` per in-flight request regardless of body size.
- Recording is best-effort: a `Record` error is logged, not propagated to the agent (audit must
  never break the data plane).
- Keep-all by default; pruning is **manual** (`POST /audit/prune` / `outwall audit prune`).
  Automatic retention/auto-prune is **Phase 2**.
- Deferred: MCP control-plane audit; SSE streaming of the audit tail (a later plan); auto-prune.
- A future change wanting whole-body hashes or full-body storage would alter `ClassifyBody` and
  the capture (and likely add a content-addressed blob store); the `sha256`/`stored` columns and
  the record-on-close timing are the integration points to revisit.
