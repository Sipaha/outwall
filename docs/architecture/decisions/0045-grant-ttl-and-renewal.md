# ADR-0045: grant TTL and renewal

- **Status:** accepted
- **Date:** 2026-07-07

## Context

Every grant outwall issues — a preset approval, an operation approval, a k8s approval, or a
manual rule created by the operator — lives forever until someone explicitly deletes or revokes
it. In practice the operator often wants to hand out access for a bounded window (the common
"give this agent read access for a few hours" case) and today has no way to express that: the
only lever is to remember to come back and revoke.

Rules can also be non-trivial to reconstruct — an operation rule accumulates an allowed-value set
and a body template through several rounds of approval, a preset rule pins slot bindings the
operator narrowed by hand. Whatever expiry mechanism we add must not silently delete that state:
losing an expired rule would force the operator to redo the narrowing/approval dance from
scratch even though the intent (this agent, this upstream, this scope) hasn't changed.

## Decision

Expiry is a **per-rule** property, enforced at decision time only, with rules kept forever and
surfaced/renewable in the UI.

- **Data model.** `policy.Rule` gains `ExpiresAt time.Time` (zero value = never expires).
  `rules.expires_at TEXT NOT NULL DEFAULT ''` (RFC3339Nano; `''` = never), added by migration step
  `rule_expiry` (`ALTER TABLE rules ADD COLUMN expires_at ...`); `schema` carries the column
  directly for fresh databases. `insertRule`/`scanRows`/`CreateMany` round-trip it like any other
  rule field, including the preset fanout (every rule from one preset approval shares the same
  expiry).
- **Enforcement is centralized in `policy.LiveRules(rules, now) []*Rule`**, the single definition
  of "expired ⇒ absent" (`!ExpiresAt.IsZero() && ExpiresAt.Before(now)`, `now = time.Now().UTC()`).
  Every decision path that reads `ForUpstream` and then decides something calls it right after the
  load: `Decide` (both the raw-HTTP and server-profile matching paths), the proxy's k8s
  discovery/health gate (`agentHasAnyGrant` in `internal/proxy`, which otherwise would let an
  expired-only allow keep `/version`, `/api`, `/apis`, `/openapi/...` open for kubectl), and
  `internal/mcpsvc`'s `statusFor` (drives `get_access`/`list_upstreams` "open" status) and its k8s
  access-request dedup gate (an expired-only tuple must be re-requested, not treated as already
  covered). So an expired rule cannot allow, deny, or require-approval, and cannot report as
  already granted, anywhere — it is simply absent, and default-deny applies. `ForUpstream` and
  `List` (the admin rule-list) stay **unfiltered**: the UI needs to see expired rules in order to
  mark and renew them.
- **TTL is server-authoritative.** Callers send an integer `ttl_seconds` (`0` = never); the server
  computes the absolute `expires_at = now + ttl_seconds` via a shared helper,
  `expiryFromTTL(ttlSeconds int) time.Time` (zero when `ttlSeconds <= 0`), beside the daemon
  approval code. Clients never send an absolute timestamp.
- **TTL is stamped on every grant-creating path.** `hApprovalResolve`'s body gains `ttl_seconds`;
  it flows through `applyApprovalSideEffects` into `approvePreset`/`approveOperation`/
  `approveK8sAccess`, each of which now takes an `expiresAt time.Time` and stamps it on every rule
  it creates or extends (the operation "extend" branch calls `Renew` on the existing rule, so
  re-approving an operation with a new value refreshes its expiry too). Manual creation
  (`hRuleCreate`, `POST /rules`) takes the same `ttl_seconds` and computes `ExpiresAt` the same
  way. Host-access approval attaches a credential and creates no rule, so it is unaffected.
- **Renewal.** A new operator-gated `POST /rules/{id}/renew` (`hRuleRenew`) takes `{ttl_seconds}`
  and calls `policy.Registry.Renew(id string, expiresAt time.Time) error`, which recomputes and
  persists `expires_at` (zero time → `''`, i.e. renewing with `ttl_seconds:0` makes a rule
  permanent). `GET /rules` exposes `expires_at` so the UI can render it. The explicit `/renew`
  endpoint is an unconditional set (the operator is deliberately choosing a new expiry). The
  *implicit* renew on the operation-approval **extend path** (`approveOperation` in
  `internal/daemon/admin.go`, re-approving an existing template with a new value) is instead
  **never-shrink**: it calls `Renew` only when the existing rule is finite AND the new expiry is
  zero (permanent) or later than the current one — a permanent rule always stays permanent, and a
  shorter ttl chosen on a routine re-approval (e.g. the UI's 1h default) never silently shortens a
  longer-lived existing grant.
- **Durations are fixed, not calendar-accurate.** The operator-facing duration picker offers
  1h/2h/8h/24h/2d/7d, plus a "1 month" option fixed at 30 days and a "1 year" option fixed at 365
  days, and "Бессрочно" (never, `ttl_seconds:0`). Default is **1 hour** in both the approval card
  and the manual-grant modal, so an operator who ignores the control still hands out a bounded
  grant rather than a permanent one.
- **Expired rules are never deleted.** They stay in the store, `List`/`ForUpstream` keep returning
  them, and the web UI marks them (a red "истекло" badge on `RuleRow`, an aggregate "истекло"/
  "истекает скоро" badge on the containing grant card via `lib/grants.ts`'s `grantExpiry`) with a
  one-click "Продлить" (renew) action that reopens the duration picker and calls
  `renewRule(id, ttlSeconds)`. There is no background janitor.

## Alternatives considered

- **A background janitor that deletes expired rules.** Rejected: rules can carry real,
  hard-won state (accumulated value sets, narrowed preset bindings); auto-deleting them would
  force the operator to redo approval work for an agent whose access should simply resume once
  renewed. Keeping the row and gating only at decision time is strictly cheaper to recover from.
- **Filter expired rules out of `ForUpstream`/the admin rule list.** Rejected: `Decide` needs an
  upstream's *live* rules, but the operator UI needs to see the *whole* rule set — including
  expired ones — in order to mark them "истекло" and offer renewal. Filtering at the read layer
  would make expired rules invisible and unrenewable.
- **Let the client supply an absolute `expires_at`.** Rejected: the daemon is the only party in a
  position to be authoritative about "now"; a client-supplied timestamp opens clock-skew and
  trust issues for a security-relevant field. The client sends a relative `ttl_seconds`; the
  server computes the absolute timestamp.
- **Expose rule expiry to the agent (`get-access` reporting a TTL).** Deferred: the concrete need
  was operator-side bounded grants; an agent-facing expiry signal is a separate, additive surface
  that can be layered on later without touching the enforcement or storage decided here.

## Consequences

- One additive schema change (`rules.expires_at`, migration `rule_expiry`); alpha, no compat
  burden — a fresh database gets the column directly from `schema`.
- Signature changes ripple through the approval side-effect chain:
  `applyApprovalSideEffects`/`approvePreset`/`approveOperation`/`approveK8sAccess` all gained an
  `expiresAt time.Time` parameter, and their callers (`hApprovalResolve`) compute it once via
  `expiryFromTTL` from the request's `ttl_seconds`.
- The operation approval's existing "extend an existing rule with a new allowed value" path now
  also (never-shrink) refreshes that rule's `expires_at` on a re-approval whose ttl is later (or
  permanent) — an agent whose value set keeps growing via repeated approvals also keeps its grant
  alive, which is the intended behavior (the operator is actively re-affirming access each time
  they approve), without a routine re-approval's shorter default ttl ever shortening a longer-lived
  grant.
- An operator can no longer lose a complex, hand-tuned rule to an expiry: it sits marked
  "истекло" until explicitly renewed or deleted, and `Decide` treats it as absent in the meantime.
- Every other rule-reading decision path (the k8s discovery gate, mcpsvc status/dedup) shares the
  exact same expiry semantics as `Decide` via `policy.LiveRules` — there is one place to fix if the
  "expired ⇒ absent" rule ever needs to change, not N call sites that can drift out of sync.
- Covered by tests: `internal/policy` (expired rules excluded from `Decide` on both matching
  paths and for grant/deny/require-approval outcomes; `''` always matches; `Renew` round-trips;
  `insertRule`/`scanRows`/`CreateMany` carry `ExpiresAt`), `internal/proxy` (an expired-only k8s
  allow rule does not open cluster discovery), `internal/mcpsvc` (an expired-only rule reports
  `needs-request` not `open`/`granted`; an expired-only k8s tuple is re-requested, not deduped as
  already covered), `internal/store` (`rule_expiry` migration upgrades an old DB; a fresh DB has
  the column), `internal/daemon` (approve preset/operation/k8s with `ttl_seconds` stamps
  `expires_at`; manual create; `POST /rules/{id}/renew`; `ttl_seconds:0` ⇒ permanent; the
  operation-approval extend path never shrinks a permanent or longer-lived finite expiry), and the
  web layer (`DurationSelect` options/default/never, `RuleRow` chip/badge/renew, grant-card
  aggregate badge, `api.ts` payload shapes).

Links: [ADR-0044](0044-operator-edit-disclosure.md) (the most recent addition to the approval
side-effect / disclosure surface this decision also touches), [ADR-0042](0042-access-page-grant-first-class.md)
(the grant-card/`RuleRow` UI this decision extends with expiry chips and renewal),
[ADR-0027](0027-schema-current-plus-forward-migrations.md) (the migration-step mechanism
`rule_expiry` follows).
