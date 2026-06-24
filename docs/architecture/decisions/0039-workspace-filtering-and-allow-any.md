# ADR-0039: `workspace: *` grants and mode-aware read-query workspace filtering

- **Status:** accepted
- **Date:** 2026-06-24

## Context

ADR-0037 made presets first-class and deliberately set the citeck `workspace` slot to
`AllowAny: false`, forcing a concrete workspace on every read/write grant so that an agent could not
silently obtain cross-workspace access. Using outwall to open a real Citeck SPA
(`enterprise.ecos24.ru`) exposed two consequences of that rigidity:

1. There is **no way to express a legitimate all-workspaces read grant** — the slot rejects `"*"`
   even though the rule layer (`matchWorkspace`) already treats `""`/`"*"` as "any workspace". Both
   the agent (`request_preset`) and the operator (approval card) are blocked by `ValidateBindings`.

2. A browser SPA issues bootstrap read queries that span workspaces the agent cannot see. Because
   `citeck.Match` requires **every** touched `(source, workspace)` pair to pass, the whole query is
   denied with `403`, which the SPA surfaces as "Server connection error". For a browser the useful
   behavior is to **narrow** the query to the workspaces the agent may read; for an API caller the
   strict `403` remains correct.

An earlier proposal to make a `default` workspace always readable was rejected: it punched a hole in
default-deny (any authenticated agent could read `default` with no grant) for little benefit.

## Decision

### `workspace: "*"` is allowed (R1)

The citeck presets' `workspace` slot becomes `AllowAny: true`. `"*"` (or empty) means **all
workspaces**, as the rule layer already interprets it. The operator still sees `"* (all)"` on the
approval card and may narrow it. This supersedes ADR-0037's `AllowAny: false` choice for this slot;
the guard moves from "you cannot express all-workspaces" to "all-workspaces is an explicit,
operator-visible value".

### Read-query workspace filtering is mode-aware (R2–R4)

For `POST /records/query` only, authorization is computed against the agent's set of allowed read
workspaces **A** (the union over its citeck read-allow rules; `*`/empty ⇒ A = all):

- Explicit `query.workspaces`, all in A → forward unchanged.
- Explicit `query.workspaces`, partially in A → **browser:** rewrite the body to the allowed subset
  and forward; **API:** `403`.
- Explicit `query.workspaces`, none in A → **browser:** synthetic empty `200`
  (`{"records":[],"hasMore":false,"totalCount":0,"messages":[],"version":1}`, no upstream call);
  **API:** `403`.
- Absent `query.workspaces`: if A = all (a `*` grant) → forward unchanged; else rewrite to A's
  concrete workspaces (both modes); if A has no concrete values → treat as "none in A".

"Browser" = authenticated via the `outwall_token` cookie (already computed as `viaCookie` in the
proxy); "API" = `Authorization: Bearer`. `mutate`, `delete`, and get-atts reads are unchanged
(all-or-nothing, no rewrite). Only **concrete** workspace values are injectable; pure-glob grants
authorize explicit matches but are not enumerated into an injected list.

### Mechanism: `Profile.Authorize` replaces `Profile.Match`

The filtering is citeck-specific (knows the body shape) and cross-rule (A spans rules), which the
per-rule `Match` cannot express, and the core must stay platform-agnostic. The `Profile` interface's
authorization method changes from `Match(rule, op) (outcome, matched)` to:

```go
Authorize(in AuthInput) (AuthResult, error)
// AuthInput{ Op, Body []byte, Browser bool, Agent []Rule, Any []Rule }
// AuthResult{ Outcome, Rule *Rule, RewriteBody []byte, EmptyResult bool }
```

Tier precedence (deny > allow; agent tier > any tier) moves into the plugin via small shared helpers,
mirroring today's `resolveTier`. `policy.Decision` gains `RewriteBody []byte` and `EmptyResult bool`;
`policy.Input` gains `Browser bool`. `policy.decideProfile` becomes a thin adapter: split the
profile's rules into agent/any tiers, call `Authorize`, map the result into a `Decision`. The proxy,
which already reads the body before `Decide` and knows `viaCookie`, applies the result: `EmptyResult`
→ synthetic `200`; `RewriteBody` → swap `r.Body`/Content-Length before the existing forward + audit
tee.

The core learns only opaque, profile-neutral signals (`RewriteBody`, `EmptyResult`, `Browser`); none
name workspaces or citeck. This keeps the ADR-0034 boundary intact.

## Alternatives considered

- **Keep `Match`, add a second `Rewrite(op, allowedScopes, body)` method.** Rejected: it leaks the
  "subset of allowed scopes" concept into the core policy engine and splits one decision across two
  profile calls. `Authorize` keeps the whole citeck decision in one place.
- **Filter in the proxy.** Rejected: the proxy must not know citeck body shapes (hard rule / ADR-0034).
- **Always-available `default` workspace.** Rejected: weakens default-deny (grant-free read of
  `default`) — the user dropped it during design.
- **Make filtering apply to API callers too.** Rejected by requirement: an API caller that names
  workspaces must get a `403` rather than silently narrowed results it did not ask outwall to narrow.
  Only browser (cookie) requests, which render `403`s as broken UI, are narrowed.
- **Forward an empty `workspaces` list to mean "all".** Rejected: an empty list upstream means all
  workspaces, the opposite of the intended scoping. Absent lists are rewritten to the concrete
  allowed set instead.

## Consequences

- `serverprofile.Profile` changes shape: `Match` → `Authorize`. The citeck profile and every test
  fake implementing `Profile` are updated in the same commit; alpha has no external implementers to
  migrate.
- The citeck profile owns tier precedence for its rules (previously in `policy.resolveTier` via the
  per-rule loop). Covered by tests including agent-deny-over-any-allow.
- A browser SPA can be opened through outwall with a **concrete** workspace grant: bootstrap queries
  that span other workspaces are narrowed or return empty instead of breaking the UI. An all-workspace
  view still needs a `*` grant.
- API callers are unaffected except that they can now be granted `workspace: "*"`; a partial-workspace
  query still returns `403`.
- The proxy can now return a response **without** calling the upstream (synthetic empty `200`) and can
  **rewrite** a request body. Both are audited; the audit log records the body actually sent upstream.
- Out of scope: batched multi-query payloads (classify handles only the single-`query` shape), write
  workspace filtering, custom/operator-initiated presets.

Covered by tests across `internal/serverprofile` (interface shape; `AllowAny` slot accepts `"*"`),
`internal/serverprofile/citeck` (the full behavior matrix; body-rewrite preserves sibling fields;
synth-empty shape; glob match-vs-inject; tier precedence), `internal/policy` (`decideProfile` maps
`AuthResult`; `Input.Browser` threaded), and `internal/proxy` (`RewriteBody` swaps body +
Content-Length; `EmptyResult` returns synthetic `200` with no upstream call; `viaCookie → Browser`;
audit correctness).

Links: [ADR-0037](0037-presets-first-class.md) (presets first-class; the `AllowAny: false` workspace
guard this ADR supersedes), [ADR-0034](0034-server-profiles-and-citeck-plugin.md) (server-profile
plugin + citeck Records — the `Profile` interface and workspace semantics; the core-stays-citeck-free
boundary), [ADR-0036](0036-browse-rule-and-readonly-preset.md) (browse-rule primitive),
[spec](../../superpowers/specs/2026-06-24-workspace-filtering-and-allow-any-design.md).
