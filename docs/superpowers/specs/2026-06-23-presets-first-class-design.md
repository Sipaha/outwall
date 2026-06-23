# Design: presets as a first-class, agent-requested concept

- **Date:** 2026-06-23
- **Status:** approved (brainstorming) — pending implementation plan
- **Companion ADR:** ADR-0037 (presets as first-class: plugin-defined, agent-requested, typed slots, fan-out to the requesting agent).
- **Builds on:** ADR-0034 (server profiles + `RuleSchema` + citeck plugin), ADR-0036 (browse-rule
  primitive — the `BrowseMethods`/`BrowsePath` raw-http shape a preset reuses), ADR-0029 (k8s
  multi-grant approval flow — the structural precedent for "one approval card → many rules").

## Why

The browse-rule primitive, the citeck Records gating, and the HTTP/Citeck/Kubernetes tabs all
shipped (ADR-0036). The first cut also shipped two **client-side preset buttons** ("Allow GET",
"ReadOnly") that POSTed rules directly from the UI. A whole-branch review found they hardcoded
`subject_agent_id: ""` — every grant was **host-wide, for ANY agent**, with no operator-visible
indication. Those buttons were removed; this design replaces them with a real concept.

What we want instead:

1. A **preset** is a named, reusable bundle of related rights (e.g. citeck "ReadOnly" = browse +
   Records read) with **typed variable slots** (`workspace`, `sourceId`) that resolve to `*` or
   concrete values.
2. The **agent** discovers, per upstream, its **type** and the **presets it may request** (with each
   preset's slot schema), then **requests** a preset with concrete slot values.
3. The **operator** approves (optionally narrowing slot values first); approval **fans out** into
   ordinary `policy.Rule`s **bound to the requesting agent** — never host-wide-by-default.

This keeps default-deny intact (operator is the gatekeeper) and makes the grant's scope explicit and
agent-scoped.

## Concept & data model

A **preset** is declared **in code** by a server-profile plugin (and a small core catalog for
generic http presets). There are **no new database tables** — a preset expands into ordinary
`policy.Rule`s at approval time, and the request/approval reuse the existing `access_requests` log
and the in-memory `approval.Queue`.

```go
// internal/serverprofile (alongside RuleSchema)
type Preset struct {
    ID    string                                 // stable: "browse-get", "citeck-readonly", "citeck-readwrite"
    Label string                                 // "Browse (GET)", "ReadOnly", "ReadWrite"
    Slots []PresetSlot
    Build func(b Bindings) ([]policy.Rule, error) // expands to rules (subject/upstream left unset)
}

type PresetSlot struct {
    Key      string   // "workspace", "sourceId"
    Label    string
    Type     string   // "text" | "enum"
    Options  []string // for enum
    AllowAny bool     // is "*" a permitted value for this slot?
    Required bool
}

type Bindings map[string]string // slot key -> value ("*" or concrete)
```

- **Available presets for an upstream** = core presets (for `kind == http`) **+** `Profile.Presets()`
  of the upstream's profile. The `Profile` interface gains `Presets() []Preset` next to
  `RuleSchema()`.
- **`Build`** returns rules of mixed shape: a browse rule (core `BrowseMethods`/`BrowsePath`) and/or a
  profile rule (`Profile` + `profile_params`). Slot values are substituted into the rule fields.
  `SubjectAgentID` and `UpstreamID` are **left unset by Build** — set at fan-out (subject = the
  requesting agent; upstream = the request's upstream).
- Presets are pure in-code definitions; the catalog is assembled at runtime per upstream.

## Control plane (MCP)

**Discovery — enrich `list_upstreams`.** Each upstream entry already carries `profile`; add a
`presets` array describing each requestable preset and its slot schema:

```jsonc
{ "id": "...", "name": "enterprise.ecos24.ru", "kind": "http", "profile": "citeck",
  "presets": [
    { "id": "browse-get", "label": "Browse (GET)", "slots": [] },
    { "id": "citeck-readonly", "label": "ReadOnly", "slots": [
        { "key": "sourceId",  "label": "Source ID", "type": "text", "allow_any": true,  "required": true },
        { "key": "workspace", "label": "Workspace", "type": "text", "allow_any": false, "required": true } ] },
    { "id": "citeck-readwrite", "label": "ReadWrite", "slots": [ /* same slots */ ] }
  ] }
```

The agent thus learns the upstream's type and which presets (with slot types/allowed values) it can
request — a single round-trip on a tool it already calls.

**Request — new MCP tool `request_preset(upstream, preset_id, vars, purpose)`** (parallel to
`request_access` / `request_k8s_access`). `vars` is a `Bindings` map. `mcpsvc` validates: the preset
exists for that upstream; every `Required` slot is present; `*` is allowed only when the slot's
`AllowAny` is true; `enum` values are within `Options`. On failure the agent gets an error and **no
card is created**.

**Approval — new `Kind = KindPreset` on `approval.Pending`**, carrying `PresetID`, `Upstream`,
`AgentID`, `Bindings`, `Purpose`. As with operation/k8s requests, a `StatusPending` row is written to
`access_requests` and a card is enqueued in `approval.Queue`.

**Fan-out — `applyApprovalSideEffects` → new `approvePreset`:**

1. The operator may have **edited the bindings** within the slot schema (see UI).
2. Resolve the `Preset` by `PresetID` from the upstream's profile (or core catalog); **re-validate**
   the final bindings against the slot schema.
3. `preset.Build(bindings)` → `[]policy.Rule`; set `SubjectAgentID = AgentID` and `UpstreamID` on
   each.
4. `d.policy.Create(...)` per rule; `d.access.GrantLatest(...)` to mark the log row granted.

This mirrors the existing `KindK8sAccess → K8sGrants → many policy.Rule` path. `get_access` then
shows the agent its newly granted (agent-scoped) rules as usual.

## Operator UI

**Preset approval card (`KindPreset`)** — the main new UI, in the existing approvals area:

- shows agent, upstream, preset label, and `purpose`;
- renders requested slot values as **editable fields** per the slot schema: `text` → input, `enum` →
  select; if `allow_any`, an explicit "`*` (all)" affordance; `required` slots cannot be emptied;
- shows a **preview** of the rules `Build` would create with the current values, so the operator sees
  the concrete grant before approving;
- **Approve / Reject** (like the operation/k8s cards). Approve commits the (possibly edited) bindings
  and runs the fan-out.

**Result visibility** needs no new work: the fanned-out rules appear (and are deletable) in the Rules
screen via the existing Browse-rules and Server-profile-rules sections; each rule shows its subject
agent, so the operator sees the grant is agent-scoped.

**Upstreams tabs** (HTTP/Citeck/Kubernetes) are unchanged; the removed preset buttons are not
re-added — presets are now agent-initiated and operator-approved.

## v1 preset catalog

- **core (any `kind == http`):** `Browse (GET)` — one browse rule `GET,HEAD /**`, no slots.
- **citeck profile:** `ReadOnly` — browse rule `GET,HEAD /**` + citeck `op:read` rule with slots
  `{ sourceId (allow_any), workspace (concrete-only) }`.
- **citeck profile:** `ReadWrite` — browse rule + citeck `op:read` + citeck `op:write`, same slots.
  (Per ADR-0034, workspace is enforceable on read/create only; mutate/delete are gated by sourceId —
  the preset just sets the values, the engine enforces what it can.)

## Components & boundaries

- `internal/serverprofile/serverprofile.go` — `Preset`/`PresetSlot`/`Bindings` types; `Profile`
  interface `Presets()`; slot-validation helper; core preset catalog (`Browse (GET)`).
- `internal/serverprofile/citeck/` — `ReadOnly` / `ReadWrite` presets + their `Build`.
- `internal/mcpsvc/service.go` — `RequestPreset(...)` (validate + log + enqueue).
- `internal/mcp/server.go` — register `request_preset`; enrich `list_upstreams` output with `presets`.
- `internal/approval/queue.go` — `KindPreset` + the new pending fields.
- `internal/daemon/admin.go` — `approvePreset` fan-out; expose presets where `list_upstreams` is built.
- `web/src/...` — the `KindPreset` approval card (editable slots + preview).
- `docs/architecture/decisions/0037-*.md`, `docs/INDEX.md`.

## Error handling / edge cases

- Unknown `preset_id` for the upstream, missing required slot, disallowed `*`, enum out of options →
  validation error at request time (no card) **and** re-checked at approve time (operator edits can
  also violate the schema → block approve with a clear message).
- A preset whose profile is no longer present on the upstream at approve time (profile changed
  between request and approval) → approve fails with a clear error rather than creating partial rules.
- Fan-out is all-or-nothing in intent: if one `policy.Create` fails, surface the error; do not leave
  the request half-granted (create rules within one logical step; report failure).
- `*` semantics carry the existing meaning: citeck empty/`*` workspace = ALL workspaces (a broad
  read) — which is exactly why `workspace.AllowAny = false` for the citeck presets, forcing the agent
  (or operator) to pick a concrete workspace.

## Testing

- `internal/serverprofile` — `Build` expands to the expected `policy.Rule`s for given bindings; slot
  validation (missing required / `*` when `AllowAny=false` / enum outside options → error); core
  `Browse (GET)` catalog.
- `internal/serverprofile/citeck` — `ReadOnly`/`ReadWrite` `Build` produce browse + `op:read`
  (+`op:write`) with substituted `sourceId`/`workspace`.
- `internal/mcpsvc` — `RequestPreset` validation + `access_requests` row + enqueue; bad input → error,
  no card.
- `internal/daemon` — `approvePreset` fan-out creates agent-scoped rules with final bindings; reject
  creates none; `list_upstreams` returns `presets`.
- `internal/mcp` — `request_preset` registered and wired to the service.
- web — approval card renders editable slots + preview; approve payload carries edited bindings.
- e2e (manual, operator-assisted, like the prior plan): agent `request_preset(enterprise.ecos24.ru,
  citeck-readonly, {sourceId:"*", workspace:<concrete>})` → operator approves → Playwright loads the
  app through outwall; read-query returns 200, mutate/delete stay denied.

## Out of scope (v1)

- Operator-initiated preset grants (apply a preset to an agent without a request) — future.
- Operator-defined / custom presets in the UI (the data-driven variant) — future; v1 is plugin-defined.
- Kubernetes presets — k8s already has `request_k8s_access` with its own multi-grant flow.
- Rule "origin" tagging (which preset created a rule) — after fan-out, rules are independent.

## ADRs / sequencing

ADR-0037 records the decision (plugin-defined, agent-requested presets with typed slots, fan-out to
the requesting agent; why not operator-defined for v1; relation to ADR-0034/0036/0029). The
implementation plan follows from this spec via the writing-plans skill.
