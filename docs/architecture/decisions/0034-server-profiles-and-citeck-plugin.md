# ADR-0034: Server profiles (plugin mechanism) + the Citeck Records plugin

- **Status:** accepted
- **Date:** 2026-06-22

## Context

outwall's policy model is API-shaped: a rule is an HTTP operation template (method + path +
typed body/query variables, ADR-0020) or a k8s tuple (namespace/resource/verb, ADR-0008). That
fits REST-ish APIs and kubectl, but not a *platform* whose API is one endpoint family carrying the
real operation in the POST body. The motivating case is **Citeck ECOS**, whose Records API routes
almost everything through `POST /api/records/{query,mutate,delete}`: read-vs-write and the entities
touched (`sourceId`, `workspace`) live in the JSON body, not the path. An operator wants per-platform
authorization — "this agent may **read** sourceId X in workspace Z, but not **write**" — which the
path-template engine cannot express.

Two shapes were considered for the platform knowledge:

1. A **generic, data-driven profile** the operator configures (where sourceId/workspace live, the
   read/write mapping). Keeps the literal string `citeck` out of the code, but pushes a lot of
   fiddly, error-prone configuration onto the operator.
2. A **purpose-built plugin** that bakes the Records API knowledge into code, so the operator just
   picks "Citeck" and gets a Records-shaped rule editor.

(2) is materially simpler to configure. Its cost is that `citeck` must appear in the code — which
the project's hard rules forbid ("no `citeck` strings/imports/branding anywhere").

## Decision

### Server-profile plugin mechanism (core stays platform-agnostic)

- Each upstream gains a `profile` field: `raw-http` (default; today's behavior) or a registered
  plugin name (e.g. `citeck`). Stored as `upstreams.profile TEXT NOT NULL DEFAULT 'raw-http'`.
- A new core package `internal/serverprofile` defines a `Profile` interface
  (`Name`, `Classify(Request) (Operation, handled, err)`, `Match(Rule, Operation) (outcome, matched, err)`,
  `RuleSchema`) and a **global registry** (`Register`/`Get`/`Names`). A plugin **registers itself**
  in its `init()`. The core imports no plugin.
- **Bundling happens at the binary entrypoints** (`cmd/outwall`, `cmd/outwall-desktop`) via a
  side-effect import. A future platform plugin = a new package + one import line in the cmd bundle.
  The library core never references a plugin. If an upstream names a profile that isn't bundled, the
  policy engine logs a warning and falls back to `raw-http` (no crash).
- Policy integration: rules gain generic `profile` + `profile_params JSON` columns. For a profiled
  upstream, `policy.Decide` calls `profile.Classify`; if the profile **claims** the request
  (`handled=true`), the profile's rules decide (via `profile.Match`, reusing the existing
  agent-tier/any-tier precedence). If the profile does **not** claim it (`handled=false`) — or the
  profile is missing — evaluation falls through to the existing raw-http/k8s engine, which now
  **skips** profile-tagged rules. So a profiled upstream carries **both** rule sets at once: profile
  rules for the paths the profile owns, raw-http rules for everything else (e.g. Citeck's
  `/gateway/observer/…`). `decide.go` talks only to the `Profile` interface and stays citeck-free;
  its audit variables use generic keys (`op`/`resource`/`scope`).
- The control API exposes `GET /profiles` (registered profiles + their `RuleSchema`) so the web UI
  renders the right rule editor per profile; adding a platform is a backend plugin + a UI editor
  component, with no core change.

### Citeck Records plugin (`internal/serverprofile/citeck`)

- Recognizes `POST /api/records/{query,mutate,delete}` and the gateway-prefixed variants
  (suffix match, so `/gateway/.../api/records/query` is handled). Anything else → `handled=false`.
- Classification mirrors the platform's own `readOnly` flag: **query = read**, **mutate/delete =
  write**. Extracted from the body: every `sourceId` (including `query.ecosType`, an alternate DAO
  selector — gating only `sourceId` would be bypassable via `ecosType`), and the workspace, with
  these asymmetries baked in:
  - **query**: `query.workspaces[]`. **Empty/absent = ALL workspaces** (platform default) → a rule
    that names a concrete workspace must REJECT the all-workspaces case, never treat it as safe.
  - **mutate create** (empty localId): workspace from the `_workspace` attribute.
  - **mutate update / delete**: workspace is server-side state, NOT in the body → these are gated by
    `sourceId` only (workspace not enforceable from the request; a concrete-workspace rule does not
    match, so allowing update/delete requires a `workspace: "*"` rule). This is surfaced in the UI.
- Rule params: `{op: "read"|"write", source_id: <glob>, workspace: <glob>}` + the generic `outcome`.
  A rule matches only if **every** touched resource passes (a cross-source batch is not allowed by a
  rule covering one sourceId). Workspace sentinels (all / unknown) are profile-internal and opaque to
  the core. The plugin uses its own glob (segment-aware `*`/`**`, mutex-guarded cache) to avoid
  importing the policy package.

### Relaxation of the "no citeck" hard rule (the deliberate exception)

The previous blanket rule — "no `citeck` strings/imports/branding anywhere" — is **scoped down**:
`citeck` is permitted ONLY inside `internal/serverprofile/citeck` and as the persisted profile-name
value `"citeck"`. The core (serverprofile, policy, proxy, daemon, store, upstream) and the UI chrome
stay citeck-free. This is the reasoned trade-off for choosing the purpose-built plugin (2) over the
generic data-driven profile (1): simpler operator configuration in exchange for a contained,
plugin-local dependency. `AGENTS.md` (the hard-rules section and the project intro) is updated to
record the exception.

## Alternatives considered

- **Generic data-driven profile (option 1).** Rejected as the default: it keeps the code
  citeck-free but makes the operator hand-configure where sourceId/workspace live and the read/write
  mapping — exactly the fiddly setup the user wanted to avoid. The plugin interface keeps that door
  open: a future generic profile could be just another registered plugin.
- **citeck-specific fields on the core `policy.Rule`** (like the k8s dimensions). Rejected: it would
  leak citeck naming into the core. Instead the rule carries an opaque `profile_params` blob the
  plugin owns.
- **Dynamic plugin loading (`plugin`/`.so`).** Rejected: Go plugins need CGO and are fragile, and the
  server binary is CGO-free. "Plugin" here means a compile-time self-registering package bundled at
  the cmd entrypoint.
- **Content/path rewriting to gate Records by URL.** Not applicable — the operation is in the body;
  the profile parses it.

## Consequences

- outwall can authorize a Citeck upstream per read/write × sourceId × workspace, while non-Records
  paths on the same host keep using raw-http rules. The agent's direct Records API calls are gated
  immediately; browser browsing of the Citeck web app is a separate concern (ADR-0035,
  per-upstream origin).
- The core is now genuinely extensible: a new platform is a self-registering plugin package + a cmd
  import line + a UI editor, with no change to the policy engine or proxy.
- The "no citeck" invariant is now scoped, not absolute. Reviewers must check that citeck naming
  stays inside `internal/serverprofile/citeck`; a `citeck` string anywhere else is still a defect.
- Workspace enforcement is honest about its limits: read and create are gated per workspace; update
  and delete are gated by sourceId only (workspace not derivable from the request body).

Covered by tests across `internal/serverprofile` (registry), `internal/serverprofile/citeck`
(ref parsing, classification, matching, registration), `internal/policy` (profile routing +
raw-http skip), `internal/proxy` (end-to-end read-allowed / write-denied through the data plane),
`internal/daemon` (control-API round-trips + `GET /profiles`), and the web UI (server-type selector,
Records-rule editor).
</content>
