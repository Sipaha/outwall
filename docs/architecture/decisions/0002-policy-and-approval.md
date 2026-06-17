# ADR-0002: Policy engine, blocking approval, and OIDC client-credentials

- **Status:** accepted
- **Date:** 2026-06-17

## Context

Plan 1 enforced access with a flat `grant` allow-list: a `(agent, upstream)` pair was either
present (allow) or absent (default-deny). That is too coarse for the product goal — operators
need to allow `GET` but require approval for `DELETE`, scope rules to a path prefix, rate-limit
chatty agents, and let a request *pause* for a human decision rather than be flatly refused.

We also need upstream auth that holds state across requests: OIDC client-credentials fetches a
bearer token from a token endpoint and must cache it (a fresh fetch per proxied request would
hammer the IdP and add latency). Plan 1's `authn.For` returns a fresh, stateless authenticator
per call, so there was nowhere to keep a token.

This is an alpha project with no released storage format to preserve, so we are free to delete
the old table and package rather than carry a legacy path.

## Decision

**Rule model + precedence.** Replace `grant` with `internal/policy`. A `Rule` binds
`(subject_agent_id, upstream_id, method, path_glob) → outcome` (`allow` | `deny` |
`require-approval`) plus a `rate_limit_per_min`. Stored in a new `rules` table (the `grants`
table is dropped). `subject_agent_id=""` means "any agent"; `method=""`/`"*"` means any method.
Path globs match the **upstream-relative** path (`/<upstream>` stripped): `*` matches within one
segment, `**` across segments, compiled to a cached regexp.

`Decide(Input) → Decision{Outcome, Rule}` applies two-level precedence: **agent-specific rules
outrank any-subject rules outrank default-deny.** Within the chosen (first non-empty) tier,
**most-restrictive wins**: any `deny` ⇒ deny, else any `require-approval` ⇒ require-approval,
else `allow`. No rules anywhere ⇒ `Deny` with `Rule=nil`.

**Blocking approval.** `internal/approval.Queue` is an in-memory map of waiters. On
`require-approval`, the proxy calls `Submit(ctx, Pending)`, which registers a pending entry and
**blocks** on a buffered channel until `Resolve(id, approve)` is called, a **5-minute** default
timeout elapses (⇒ denied), or the request context is canceled. Approved ⇒ proxy forwards;
denied/timeout ⇒ 403; ctx-canceled ⇒ 504. The queue is intentionally not persisted: a pending
approval is meaningful only while its HTTP request is in flight, and the daemon restarting
cancels those requests anyway.

**Rate limiter.** `internal/policy.Limiter` is an in-memory fixed-window-per-minute counter
keyed by `agentID|ruleID`, evaluated only when the matched rule has `rate_limit_per_min > 0`.
Over the limit ⇒ 429. `0` (the default) ⇒ unlimited.

**OIDC client-credentials + Manager.** `upstream.AuthConfig` gains `TokenURL/ClientID/
ClientSecret/Scope` and a new type `oidc-client-credentials`. The authenticator POSTs the
client-credentials grant and caches the token until ~30 s before `expires_in`. To keep the cache
alive across requests, `authn.Manager` holds one authenticator instance per upstream ID, keyed
by an auth-config fingerprint (rebuilt when the config changes). The proxy now calls
`AuthManager.Authenticator(up)` instead of the stateless `authn.For`.

## Alternatives considered

- **Keep `grant` and layer rules on top.** Rejected: two overlapping allow paths to reason
  about; alpha lets us delete cleanly.
- **Persisted approval queue (SQLite).** Rejected: a pending approval has no meaning once its
  request is gone; in-memory matches the lifecycle and avoids cleanup logic.
- **Token-bucket rate limiter.** Rejected for now: fixed-window is simpler, allocation-free, and
  adequate for per-minute caps; can revisit if burst smoothing is needed.
- **Caching tokens inside `authn.For`.** Rejected: `For` returns a fresh instance per call, so a
  caller-side cache (the Manager) is where cross-request state belongs.

## Consequences

- The proxy's single allow/deny call site becomes a richer `Decide → switch` with approval and
  rate-limit branches; behaviour is covered by `TestProxyRequireApprovalBlocksUntilResolved` and
  `TestProxyRateLimit` against local backends.
- A one-time DB reset is required for anyone with a Plan-1 database (the `grants` table is gone).
  Acceptable per the alpha quality bar.
- Approval state and rate-limit counters are per-process: they reset on daemon restart and are
  not shared across multiple daemons. A future multi-instance deployment would need a shared
  store; out of scope here.
- The approval queue blocks a goroutine (and the client's HTTP connection) for up to 5 minutes.
  The timeout bounds resource use; the buffered resolve channel prevents `Resolve` from blocking
  even if the waiter has already left.
- Deferred to later plans: OIDC authorization-code flow, body filters, audit logging, the MCP
  control plane, and the control API + SSE that would stream the pending-approval list to a UI.
