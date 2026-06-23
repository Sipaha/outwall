# ADR-0037: Presets as a first-class, agent-requested concept

- **Status:** accepted
- **Date:** 2026-06-23

## Context

ADR-0036 shipped the **browse-rule primitive** (a `BrowseMethods`/`BrowsePath` glob pair on
`policy.Rule`, coexisting with operation templates) and identified the need for pre-composed rights
bundles — a "preset" — that an agent can request without having to construct each rule individually.
That ADR's first draft included two client-side buttons ("Allow GET" and "ReadOnly") that posted rules
directly from the operator UI. A whole-branch review found a critical defect: both buttons hardcoded
`subject_agent_id: ""`, granting to **any agent, host-wide**, with no operator-visible indication and
no mechanism to scope the grant to the requesting agent. Those buttons were removed before merge.

This ADR introduces **presets as a proper concept**: plugin-defined bundles of rights with typed
variable slots, discoverable by agents at runtime, requestable via the MCP control plane, and
approved by the operator — with the resulting rules **bound to the requesting agent**. The agent-scope
fix is the central constraint that drove the design: subject assignment happens at approval time in
the daemon, not at request time in the client.

Two earlier patterns in this codebase shaped the design:

- **ADR-0034 (server-profile plugin mechanism):** the `Profile` interface's self-registering plugin
  model. Presets follow the same plugin-owned, registry-at-cmd approach; the `Profile` interface gains
  `Presets() []Preset` beside the existing `RuleSchema()`.
- **ADR-0029 (k8s multi-grant approval flow):** one approval card → many `policy.Rule`s created on
  approve. The `KindPreset` approval reuses this shape: one card carries the preset + bindings, and
  fan-out creates N agent-scoped rules on approve. The existing `access_requests` log and
  `approval.Queue` are reused with no new tables.

## Decision

### Concept and data model

A **preset** is a named, reusable bundle of related rights declared **in code** by a server-profile
plugin (or the core http catalog). There are no new database tables — a preset expands into ordinary
`policy.Rule`s at approval time.

```go
// internal/serverprofile (alongside RuleSchema)
type Preset struct {
    ID    string        // stable: "browse-get", "citeck-readonly", "citeck-readwrite"
    Label string        // "Browse (GET)", "ReadOnly", "ReadWrite"
    Slots []PresetSlot
    Build func(b Bindings) ([]RuleTemplate, error)
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

`Build` returns `[]RuleTemplate`, not `[]policy.Rule`. **`RuleTemplate` is profile-neutral** — it
carries the logical rule shape (`Outcome`, `BrowseMethods`/`BrowsePath` for browse rules,
`Profile`/`ProfileParams` for profile rules) but has **no** `SubjectAgentID` or `UpstreamID` fields.
This is required because `serverprofile` must not import `policy`: `policy` already imports
`serverprofile` (to call `profile.Classify`/`profile.Match`), so `serverprofile` importing `policy`
would be a direct import cycle. `Build` stays in `serverprofile`, so it must return a type that lives
there too. The daemon (which imports both) maps each `RuleTemplate` to a `policy.Rule` at fan-out,
setting `SubjectAgentID` = the requesting agent and `UpstreamID` = the request's upstream.

```go
type RuleTemplate struct {
    Outcome       string
    BrowseMethods string          // e.g. "GET,HEAD"
    BrowsePath    string          // e.g. "/**"
    Profile       string          // profile name, if a profile rule
    ProfileParams json.RawMessage // profile params blob
}
```

The available presets for an upstream = core presets (when `kind == http`) + `profile.Presets()` of
the upstream's profile. The catalog is assembled at runtime per upstream.

### Control plane — discovery

`list_upstreams` is enriched: each upstream entry now carries a `presets` array with each preset's
slot schema, so an agent learns the upstream's type and requestable presets in a single round-trip:

```jsonc
{ "id": "...", "name": "enterprise.ecos24.ru", "kind": "http", "profile": "citeck",
  "presets": [
    { "id": "browse-get", "label": "Browse (GET)", "slots": [] },
    { "id": "citeck-readonly", "label": "ReadOnly", "slots": [
        { "key": "sourceId",  "label": "Source ID",  "type": "text", "allow_any": true,  "required": true },
        { "key": "workspace", "label": "Workspace",   "type": "text", "allow_any": false, "required": true }
    ]},
    { "id": "citeck-readwrite", "label": "ReadWrite", "slots": [ /* same slots */ ] }
  ] }
```

### Control plane — request

A new MCP tool `request_preset(upstream, preset_id, vars, purpose)` (parallel to `request_access` /
`request_k8s_access`) takes a `Bindings` map for `vars`. `mcpsvc` validates at request time:

- the preset exists for that upstream;
- every `Required` slot is present;
- `"*"` is allowed only when the slot's `AllowAny` is true;
- `enum` values are within `Options`.

Validation failure returns an error to the agent; **no approval card is created**. On success a
`StatusPending` row is written to `access_requests` and a `KindPreset` card is enqueued in
`approval.Queue`.

### Control plane — approval and fan-out

`approval.Pending` gains `KindPreset`, carrying `PresetID`, `Upstream`, `AgentID`, `Bindings`,
`Purpose`. `applyApprovalSideEffects` calls a new `approvePreset` path:

1. The operator may have **edited the bindings** on the approval card (see UI). Final bindings are
   **re-validated** server-side against the slot schema — a slot narrowed outside Options or an
   emptied Required slot blocks the approve with a clear error.
2. Resolve the `Preset` from the upstream's profile or core catalog; call `preset.Build(bindings)` →
   `[]RuleTemplate`.
3. For each `RuleTemplate`, create a `policy.Rule` with `SubjectAgentID = AgentID` and the request's
   `UpstreamID`, then call `d.policy.Create(...)`.
4. Call `d.access.GrantLatest(...)` to mark the `access_requests` row granted.

This mirrors the `KindK8sAccess → K8sGrants → many policy.Rule` path (ADR-0029). `get_access` then
shows the agent its newly granted, agent-scoped rules as usual.

**Fan-out atomicity.** ~~Known limitation — resolved by ADR-0038.~~ Both `approvePreset` and
`approveK8sAccess` now call `policy.Registry.CreateMany`, which writes all rules in a single
SQL transaction (all-or-nothing). A mid-batch failure rolls back the entire batch; no partial
grant can be left in place. See [ADR-0038](0038-atomic-rule-fanout.md).

If the upstream's profile has changed between request and approve (e.g. the profile was re-assigned
and the preset no longer exists), `approvePreset` returns a clear error rather than creating partial
rules.

### Dry-run preview — `POST /presets/preview`

A new control-API endpoint accepts `{upstream, preset_id, bindings}` and returns the `[]RuleTemplate`
that `Build` would produce — the operator sees the concrete rules before committing. The approval
card calls this endpoint whenever slot values change to refresh the preview pane.

### Operator UI — `KindPreset` approval card

The approval card for a preset request shows: agent, upstream, preset label, purpose, and the
requested slot values rendered as **editable fields** per the slot schema (`text` → input,
`enum` → select; if `allow_any`, an explicit `"* (all)"` affordance; `required` slots cannot be
emptied). A preview pane shows the rules `Build` would create with the current values, updated live.
**Approve** commits the (possibly narrowed) bindings and triggers fan-out. **Reject** creates no
rules.

The upstream-zone tabs (HTTP / Citeck / Kubernetes) from ADR-0036 are unchanged; the removed preset
buttons are not re-added. Presets are now agent-initiated and operator-approved.

**Result visibility** needs no new work: fanned-out rules appear in the existing Browse-rules and
Server-profile-rules sections of the Rules page, each showing its subject agent — the operator sees
the grant is agent-scoped and can delete individual rules as before.

### v1 preset catalog

- **core (any `kind == http`): `Browse (GET)`** — one browse rule `GET,HEAD /**`, no slots.
- **citeck profile: `ReadOnly`** — browse rule `GET,HEAD /**` + citeck `op:read` rule with slots
  `{ sourceId (allow_any: true), workspace (allow_any: false) }`.
- **citeck profile: `ReadWrite`** — browse rule + citeck `op:read` + citeck `op:write`, same slots.

The `workspace` slot's `AllowAny: false` on the citeck presets is deliberate. Per ADR-0034, an
empty/`*` workspace in a Citeck query means **all workspaces** (the platform's default), so a rule
that binds `workspace: "*"` is a broad grant. The slot schema forces the agent — or the narrowing
operator — to name a concrete workspace rather than silently granting cross-workspace read access.
`sourceId` may be `"*"` because a source-wide grant is a recognizable, intentional choice; the
operator sees it on the card.

## Alternatives considered

- **Operator-initiated grants (push presets to an agent without a request).** Not in v1: it requires
  the operator to know the agent's ID and to fill in slot values on behalf of the agent. The
  agent-request flow is clearer — the agent names what it needs; the operator approves and may
  narrow. Operator-initiated grants are a future addition.
- **Operator-defined / custom presets (configured in the UI, not in code).** Not in v1: a data-driven
  preset requires the operator to define the template structure (which rule shapes, which slot keys)
  in the UI — higher complexity for the first cut. v1 presets are plugin-defined and cover the
  concrete use cases at hand. Custom presets are a future extension over the same `approval.Queue` +
  fan-out path.
- **Preset buttons that POST rules directly (the removed v1 buttons from ADR-0036 draft).** Rejected:
  they hardcoded `subject_agent_id: ""` — any-agent, host-wide grants — with no approval gate and no
  operator visibility into which agent was being granted access. The request/approve flow exists
  precisely to give the operator this visibility and to scope the grant to the requesting agent.
- **`Build → []policy.Rule` (the spec's initial sketch).** Rejected because of the import cycle:
  `serverprofile.Build` returning `policy.Rule` would require `serverprofile` to import `policy`,
  which already imports `serverprofile`. The `RuleTemplate` indirection keeps each package's import
  graph acyclic; the daemon (which imports both) performs the final mapping.
- **Kubernetes presets.** Not in scope: `request_k8s_access` already has its own multi-grant flow
  with explicit `(namespace, resource, verb)` tuples. Wrapping that in a preset abstraction adds no
  clarity for the k8s case and would need a separate `KindK8sPreset` path.

## Consequences

- Agents can now discover and request pre-composed rights bundles via the MCP control plane, reducing
  multi-step request sequences to a single `request_preset` call.
- All preset grants are agent-scoped by construction: `SubjectAgentID` is set to the requesting agent
  at fan-out; the operator sees the agent on the card; `subject_agent_id: ""` (any-agent) is
  impossible through this path.
- The operator retains full control: every preset grant requires operator approval; the operator may
  narrow any slot value before approving; re-validation server-side ensures narrowed values still
  satisfy the slot schema.
- `serverprofile.Profile` grows a `Presets() []Preset` method. Existing plugin implementations
  (including fakes in tests) must implement it — updated in the same commit as the interface change
  so the tree never fails to build.
- No new database tables: the `access_requests` log and `approval.Queue` handle the full lifecycle;
  fanned-out rules are ordinary `policy.Rule` rows indistinguishable from manually created rules.
  Rule "origin" tagging (which preset created a rule) is out of scope for v1 — rules are independent
  after fan-out.
- `POST /presets/preview` exposes `Build` as a dry-run; a bug in a `Build` implementation is visible
  to the operator before any rule is committed.
- Future work: operator-initiated grants, operator-defined/custom presets, rule-origin tagging.

Covered by tests across `internal/serverprofile` (`Preset`/`PresetSlot`/`Bindings` types; slot
validation; core `Browse (GET)` catalog; `Build` expansion to expected `RuleTemplate`s),
`internal/serverprofile/citeck` (`ReadOnly`/`ReadWrite` `Build` for given bindings),
`internal/mcpsvc` (`RequestPreset` validation + `access_requests` row + enqueue; bad input → error,
no card), `internal/daemon` (`approvePreset` fan-out creates agent-scoped rules; reject creates none;
`list_upstreams` includes `presets`; `POST /presets/preview` returns correct templates),
`internal/mcp` (`request_preset` registered and wired), and `web/src` (approval card renders editable
slots + preview; approve payload carries edited bindings). The `internal/mcpsvc` preset tests use a
fake in-test profile to keep that core package citeck-free; the `internal/daemon` tests exercise the
real citeck `ReadOnly` fan-out end-to-end via the daemon test package's pre-existing citeck
blank-import (the sanctioned ADR-0034 test exception).

Links: [ADR-0034](0034-server-profiles-and-citeck-plugin.md) (server-profile plugin + citeck Records —
profile interface and workspace semantics), [ADR-0036](0036-browse-rule-and-readonly-preset.md) (browse
rule primitive — the `BrowseMethods`/`BrowsePath` shape presets expand to; the removed preset buttons
that motivated this ADR), [ADR-0029](0029-request-k8s-access-multi-grant.md) (k8s multi-grant approval
flow — the structural precedent for one approval card → many rules),
[spec](../../superpowers/specs/2026-06-23-presets-first-class-design.md) (presets-first-class design).
