# Design: workspace `*` grants + mode-aware read-query workspace filtering

- **Date:** 2026-06-24
- **Status:** draft for review
- **Touches:** `internal/serverprofile`, `internal/serverprofile/citeck`, `internal/policy`,
  `internal/proxy`; ADR-0039 (amends ADR-0037).

## Problem

Two pain points surfaced while opening a Citeck SPA (`enterprise.ecos24.ru`) through outwall:

1. **`workspace: *` is unrequestable.** The citeck presets declare the `workspace` slot
   `AllowAny: false` (ADR-0037), so neither an agent (`request_preset`) nor the operator (approval
   card) can bind `workspace: "*"` — even though the rule layer already treats `""`/`"*"` as "any
   workspace". The concrete-workspace constraint was a deliberate guard, but it is too rigid: there
   is no way to express a legitimate all-workspaces read grant.

2. **A read query that names several workspaces is denied wholesale when only some are granted.**
   `citeck.Match` requires *every* touched `(source, workspace)` pair to pass, so a browser SPA whose
   bootstrap queries span workspaces the agent cannot see gets a `403`, which it renders as
   "Server connection error". For a browser the friendlier behavior is to **narrow** the query to the
   workspaces the agent may read and forward that; for a non-browser API caller the strict `403` is
   correct (it must not silently get partial results it did not ask to be narrowed).

This design addresses both. The earlier idea of an always-available `default` workspace was dropped:
it weakened default-deny (any authenticated agent could read `default` with no grant) for too little
benefit.

## Requirements (agreed)

- **R1.** An agent can request, and the operator can approve/edit, `workspace: "*"` (= all
  workspaces). `*` keeps meaning "all".
- **R2.** Read-query workspace handling becomes **mode-aware** (browser vs API), per the matrix below.
- **R3.** Filtering/rewriting applies **only to read** (`/records/query`). `mutate`/`delete`/get-atts
  keep the existing all-or-nothing semantics.
- **R4.** A read query with **no** `workspaces` field (currently "all workspaces") is rewritten to the
  agent's allowed concrete workspaces (both modes) — unless the agent holds an all-workspaces (`*`)
  read grant, in which case it is forwarded unchanged.

"Browser" = the request authenticated via the `outwall_token` **cookie** (already computed as
`viaCookie` in `proxy.go`); "API" = the `Authorization: Bearer` header.

## Behavior matrix — read `query`

Let **A** = the agent's set of allowed read workspaces, derived from its citeck read-allow rules:
- if any read-allow rule has `workspace` `""` or `"*"` → **A = all** (wildcard);
- otherwise **A = { the workspaces each read-allow rule permits }** (glob rules match explicit values
  but contribute no enumerable value for injection — see "empty-injection" below).

| Request `query.workspaces` | A = all? | Browser (cookie) | API (Bearer) |
|---|---|---|---|
| `[a,b]`, all ∈ A | — | forward unchanged | forward unchanged |
| `[a,b]`, some ∉ A | no | rewrite to `[…∩A]`, forward | **403** |
| `[a,b]`, none ∈ A | no | synth empty `200` | **403** |
| absent | yes (`*`) | forward unchanged | forward unchanged |
| absent | no, A non-empty (concrete) | rewrite to `[…A]`, forward | rewrite to `[…A]`, forward |
| absent | no, A empty (no read grants) | synth empty `200` | **403** |

- **Rewrite** = replace `query.workspaces` in the JSON body with the narrowed list, re-marshal,
  forward upstream; the audit tee captures the rewritten body (what was actually sent).
- **Synth empty `200`** = outwall returns `{"records":[],"hasMore":false,"totalCount":0,"messages":[],"version":1}`
  with `Content-Type: application/json`, **without** contacting the upstream. Audited as an
  allow/filtered outcome. This keeps a browser SPA rendering instead of erroring.
- **Empty-injection** (absent → list): only **concrete** workspace values from read-allow rules are
  injectable. A pure-glob grant (e.g. `user$*`) cannot be enumerated; it still authorizes explicit
  requested workspaces that match it, but contributes nothing to an injected list. If A has no
  concrete values and is not wildcard, "absent" is treated as "none ∈ A" (synth empty / 403).

Non-read operations (`mutate`, `delete`, get-atts read) are unchanged: existing all-or-nothing
`Match` outcome (single allow/deny), no body rewrite.

## Mechanism — `Profile.Authorize` (replaces `Match`)

The filtering is citeck-specific (it knows the body shape and the `query.workspaces` field) and is
**cross-rule** (the allowed set is the union over the agent's rules). The per-rule
`Match(rule, op) → (outcome, matched)` cannot express it. The core must stay platform-agnostic, so
the logic lives in the plugin.

We replace the `Profile.Match` method with a single richer entry point:

```go
// internal/serverprofile

// AuthInput carries everything a profile needs to authorize a classified operation, including the
// raw body (for rewrite), the request mode, and the agent's candidate rules split by tier.
type AuthInput struct {
    Op      Operation     // the classified operation (from Classify)
    Body    []byte        // raw request body, for rewrite
    Browser bool          // request authenticated via browser cookie (Playwright)
    Agent   []Rule        // this profile's rules for the requesting agent (agent tier)
    Any     []Rule        // this profile's any-agent rules (subject "")
}

// AuthResult is a profile's full authorization decision for one operation.
type AuthResult struct {
    Outcome     string // Allow | Deny | RequireApproval
    Rule        *Rule  // the rule that decided, for audit (optional; nil for synthesized/empty)
    RewriteBody []byte // if non-nil, the proxy forwards this body instead of the original
    EmptyResult bool   // if true, the proxy returns a synthetic empty 200 (no upstream call)
}

type Profile interface {
    Name() string
    Classify(req Request) (op Operation, handled bool, err error)
    Authorize(in AuthInput) (AuthResult, error)   // was: Match(rule, op)
    RuleSchema() RuleSchema
    Presets() []Preset
}
```

Tier precedence is now the profile's responsibility, but stays simple and is shared via small helpers
in the citeck package:
- **deny beats allow** within a tier; **agent tier beats any tier** (mirrors `resolveTier` today).
- For read filtering, the allowed set A is built from **allow** rules of both tiers; an **agent-tier
  deny** that matches a workspace removes it from A (any-tier deny likewise, lower precedence). v1
  presets create only allow rules, so deny interaction is covered by tests but not exercised in
  practice.

`policy.decideProfile` becomes a thin adapter: classify → split the profile's rules into agent/any
tiers → `prof.Authorize(AuthInput{...})` → map `AuthResult` into `policy.Decision`.

```go
// internal/policy
type Decision struct {
    Outcome     string
    Rule        *Rule
    Vars        map[string]string
    NewValues   []VarValue
    RewriteBody []byte // NEW: non-nil → proxy forwards this instead of the original body
    EmptyResult bool   // NEW: true → proxy returns a synthetic empty 200
}
```

`policy.Input` gains `Browser bool`, threaded from the proxy's `viaCookie`.

### Proxy application

`proxy.go` already (a) reads the full body before `Decide` and (b) knows `viaCookie`. After `Decide`:
- `dec.EmptyResult` → write the synthetic empty `200` JSON, record an allow/filtered audit entry,
  return (no upstream round-trip).
- `dec.RewriteBody != nil` → set `r.Body`, `r.ContentLength`, and the `Content-Length` header to the
  rewritten body before the existing forward + audit-tee path (so the upstream and the audit log see
  the narrowed body).
- otherwise unchanged.

## R1 — `workspace: "*"`

Flip the citeck presets' `workspace` slot to `AllowAny: true`. `ValidateBindings` then accepts `"*"`
for both the agent request (`mcpsvc.RequestPreset`) and operator approval; the rule layer already
treats `"*"`/`""` as all. No other change. ADR-0037's rationale for `AllowAny: false` is superseded
by ADR-0039.

## Isolation / boundaries

- **Core stays citeck-free.** All workspace/filtering/rewrite logic is in
  `internal/serverprofile/citeck`. The core learns only two opaque, profile-agnostic signals on the
  decision (`RewriteBody []byte`, `EmptyResult bool`) and one input (`Browser bool`) — none mention
  workspaces or citeck.
- **`serverprofile` ↔ `policy` import direction is unchanged** (`policy` imports `serverprofile`).
  `Authorize` lives in `serverprofile`; `policy` adapts.
- **Single responsibility:** `classify` (body → operation) and `authorize` (operation + rules + mode →
  decision/rewrite) are separate functions in the citeck package; the rewrite/synth-body helpers are
  their own small file.

## Testing

- `internal/serverprofile`: `Authorize` interface shape; `ValidateBindings` accepts `"*"` for an
  `AllowAny` slot (already covered — confirm citeck slot flip path).
- `internal/serverprofile/citeck`: the full behavior matrix — explicit list all/partial/none allowed
  × browser/API; absent workspaces × (`*` grant / concrete grants / no grants) × browser/API; write &
  delete unchanged; rewrite body preserves sibling fields (attributes, language, page); synth-empty
  shape; glob-grant explicit-match yes / injection no; tier precedence (agent deny over any allow).
- `internal/policy`: `decideProfile` maps `AuthResult` → `Decision` (incl. `RewriteBody`/`EmptyResult`);
  `Input.Browser` threaded.
- `internal/proxy`: `RewriteBody` swaps the forwarded body and Content-Length; `EmptyResult` returns
  the synthetic `200` without an upstream call; both produce a correct audit entry; `viaCookie` →
  `Input.Browser`.

## Out of scope

- Batched multi-query `/records/query` payloads (classify already handles only the single-`query`
  shape; document, do not extend here).
- Workspace handling for writes/deletes (unchanged by agreement).
- Operator-initiated grants and custom presets (ADR-0037 future work, untouched).
