# Grant TTL (expiry) and renewal — design

- **Date:** 2026-07-07
- **Status:** approved (brainstorm)

## Problem

outwall grants are permanent: an approved rule lives forever. The operator has no way to hand out
time-limited access (the common "give this agent read access for a few hours" case). We add a
grant-duration control to both places the operator creates grants, enforce expiry (an expired rule
stops granting), and — because rules can be complex and worth keeping — never auto-delete expired
rules but instead mark them in the UI and offer a one-click renewal.

## Scope

- A duration dropdown on the **operator approval card** ("Запросы прав") and the **manual grant
  modal** ("Выдать вручную"). Default **1 hour**. Options: 1h, 2h, 8h, 24h, 2d, 7d, 1 month (=30d),
  1 year (=365d), and **Бессрочно** (never).
- Expiry is a per-**rule** property (`expires_at`). One preset approval creates several rules
  (browse + read) that share the chosen expiry; separate approvals / manual grants can have
  different expiries.
- An expired rule is **ignored at decision time** (default-deny) but is **never deleted**. The UI
  marks expired/expiring rules and offers **"Продлить"** (renew) to set a fresh expiry.
- Out of scope: agent-facing expiry (get-access does not report rule expiry); calendar-accurate
  months/years (durations are fixed seconds); a background janitor (explicitly rejected — rules are
  kept).

## Data model & enforcement

- **Store.** `rules` gains `expires_at TEXT NOT NULL DEFAULT ''` (RFC3339Nano; `''` = never).
  Migration step `rule_expiry` (`ALTER TABLE rules ADD COLUMN expires_at TEXT NOT NULL DEFAULT ''`).
  `schema` updated to the current shape.
- **policy.Rule.** Gains `ExpiresAt time.Time` (zero value = never). `insertRule` persists it
  (`''` when zero, else RFC3339Nano); `scanRows` parses it (empty → zero). `CreateMany` carries it
  per rule (preset fanout sets the same value on every rule).
- **Enforcement is in `Decide` only.** After `ForUpstream` loads the upstream's rules, `Decide`
  skips any rule with `!ExpiresAt.IsZero() && ExpiresAt.Before(now)` before matching, so an expired
  rule cannot grant, deny, or require-approval — it is simply absent → default-deny. `now` is
  `time.Now().UTC()` inside `Decide` (tests set `expires_at` in the past/future).
- **`ForUpstream` and the admin rule-list stay unfiltered** — the UI must see expired rules to mark
  and renew them.

## Grant / approval flow (TTL in)

- **Transport.** The client sends `ttl_seconds` (integer; `0` = never). The server computes
  `expires_at = now + ttl_seconds` (or `''` when `0`) — server-authoritative time. A helper
  `expiryFromTTL(ttlSeconds int) time.Time` (zero when 0) lives beside the daemon approval code.
- **Approval.** `hApprovalResolve` request body gains `ttl_seconds int`. It flows through
  `applyApprovalSideEffects` into `approvePreset` / `approveOperation` / `approveK8sAccess`, each of
  which stamps `ExpiresAt` on every rule it creates. (Host-access attaches a credential and creates
  no rule — unaffected.)
- **Manual grant.** The manual-rule create endpoint (`POST /rules`) body gains `ttl_seconds int`;
  the created rule gets the computed `ExpiresAt`.
- **Default.** The UI dropdown defaults to 1h in both places, so an operator who ignores it still
  hands out a bounded grant.

## Renewal & expired marking

- **API.** New operator-gated `POST /rules/{id}/renew` with `{ttl_seconds int}` → sets
  `expires_at = now + ttl_seconds` (or `''` when `0` = make permanent). Backed by
  `policy.Registry.Renew(id string, expiresAt time.Time) error` (zero time → `''`).
- **RuleRow.** Renders the rule's expiry with `RelTime` ("истекает через 3ч", exact time on hover).
  When `expires_at` is in the past, a red **"истекло"** badge. A **"Продлить"** button opens the
  duration dropdown (inline popover) and calls `renewRule(id, ttlSeconds)`. A permanent rule
  (`''`) shows no expiry chip (optionally a subtle "бессрочно").
- **Grant card aggregate.** The grant container (by-agent `UpstreamGrantCard`, by-upstream
  `UpstreamGroupCard` / the agent card) shows an aggregate badge derived from its rules' expiries:
  "истекло" if any rule is expired, else "истекает скоро" if the soonest expiry is within a small
  window (e.g. < 1h), else none.

## UI components

- **`DurationSelect.tsx`** — a `<select>` of the fixed options plus "Бессрочно", value =
  `ttl_seconds` (number; 0 = never), default 1h. Reused by `ApprovalCards`, `ManualRuleModal`, and
  the RuleRow "Продлить" popover. A single `DURATION_OPTIONS` list is the source of truth.
- **`types.ts`** — `Rule` gains `expires_at: string` (RFC3339 or `''`).
- **`api.ts`** — `renewRule(id, ttlSeconds)`; `ttl_seconds` added to the approve-resolve and
  manual-create payloads.
- **Status colour.** Add an `expired` colour token / `StatusBadge` variant (the backlog already
  noted `revoked`/`expired` had no colour).

## Testing

- **policy:** an expired rule is not matched by `Decide` (grant, deny, and require-approval rules);
  a not-yet-expired rule matches; `''` = never always matches; `Renew` sets/clears expiry;
  round-trip through `insertRule`/`scanRows`/`CreateMany`.
- **store:** the `rule_expiry` migration upgrades an old (pre-column) DB; a fresh DB has the column.
- **daemon:** approve (preset/operation/k8s) with `ttl_seconds` stamps `expires_at` on the created
  rules; manual create with `ttl_seconds`; `POST /rules/{id}/renew` updates expiry; `ttl_seconds:0`
  ⇒ permanent.
- **web:** `DurationSelect` (options + default + never); `RuleRow` (expiry chip, expired badge,
  Продлить → renewRule); grant-card aggregate badge; `api` payload shape.

## Docs

- ADR-0045 (grant TTL + renewal). Module docs: `policy` (Rule.ExpiresAt, Decide expiry filter,
  Renew), `store` (rule_expiry migration), `daemon`/`webui` as touched. `docs/INDEX.md` ADR line.
</content>
