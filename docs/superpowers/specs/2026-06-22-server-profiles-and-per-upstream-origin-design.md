# Design: server profiles + per-upstream origin

- **Date:** 2026-06-22
- **Status:** approved (brainstorming) — pending implementation plans
- **Companion ADRs:** ADR-0034 (server profiles + first plugin: citeck; relaxes the "no citeck" rule
  in a contained way), ADR-0035 (per-upstream origin / subdomain data plane).

## Why

Two gaps surfaced while trying to drive `enterprise.ecos24.ru` (a Citeck ECOS web app) through
outwall with Playwright:

1. **Policy is API-shaped and platform-blind.** Rules are per-operation HTTP templates
   (method + path + body vars). A real platform API (Citeck's Records API: almost everything is
   `POST /api/records/{query,mutate,delete}`) can't be expressed as path templates — read vs write
   and the entities being touched (sourceId, workspace) live **in the POST body**, not the path.
   The operator wants to grant access *per platform*: "this agent may **read** sourceId X in
   workspace Z, but not **write**."

2. **The data plane can't serve a web app to a browser.** It is a path-prefix proxy on one shared
   origin (`https://127.0.0.1:<port>/<host>/…`). It does not rewrite redirects or HTML/CSS/JS URLs,
   so root-relative links hit the wrong upstream, absolute links/redirects bypass the proxy, and
   `Set-Cookie; Domain=` is dropped. Fine for API clients and kubectl; broken for browsing.
   (ADR-0033 already flagged "a distinct origin per upstream … deferred" — this is that fix.)

This design adds a **server-profile plugin mechanism** (keeps the core platform-agnostic; the first
plugin is Citeck) and a **per-upstream browser origin** (wildcard-subdomain data plane).

---

## Part A — Server profiles (core stays platform-agnostic)

A new per-upstream field **`profile`**: `raw-http` (default; today's behavior) or a registered plugin
name such as `citeck`. The profile specializes **policy classification** for the paths it claims;
everything else falls through to the existing raw-http engine.

### The `Profile` interface (core: `internal/serverprofile`)

```go
// Operation is the profile's normalized view of a request, used for policy + audit.
type Operation struct {
    Kind      string          // "read" | "write" | "" (unknown)
    Resources []ResourceScope // e.g. {SourceID, Workspace} tuples; may be many (batch requests)
    Method    string          // echoed for fallback / audit
    Path      string
}

type ResourceScope struct {
    Resource string // e.g. Citeck sourceId (appName/ prefix stripped)
    Scope    string // e.g. Citeck workspace ("" = unscoped/ALL — see citeck plugin)
}

type Profile interface {
    Name() string
    // Classify reports whether THIS profile handles the request. handled=false → the engine
    // uses the generic raw-http path (operation templates / URL-pattern / browse).
    Classify(req *Request) (op Operation, handled bool, err error)
    // Match evaluates one of this profile's rules against a handled Operation.
    Match(rule ProfileRule, op Operation) (Outcome, error)
    // RuleSchema describes the rule fields so the UI can render the right editor.
    RuleSchema() RuleSchema
}
```

### Registry + self-registration (the plugin mechanism)

- Core `internal/serverprofile` owns a `Registry`: `Register(name string, factory Factory)` and
  `Get(name) (Profile, bool)`. **Core imports no plugin.**
- A plugin **registers itself** in `func init()`:
  `serverprofile.Register("citeck", New)` inside `internal/serverprofile/citeck`.
- **Bundling happens at the binary entrypoints** (`cmd/outwall`, `cmd/outwall-desktop`) via a
  side-effect import: `import _ "github.com/Sipaha/outwall/internal/serverprofile/citeck"`. Adding a
  future platform = a new plugin package + one import line in the cmd bundle. The **library core
  never references `citeck`**; the only `citeck` strings in the tree live inside the plugin package
  (and the `"citeck"` profile-name value persisted as data).
- If an upstream's stored `profile` names a plugin that isn't bundled, the engine falls back to
  `raw-http` and writes an audit warning (no crash).

### Policy integration (`decide.go`)

For an upstream with a non-`raw-http` profile, `Decide` first calls `profile.Classify(req)`:

- **handled = true** → match the request against the upstream's **profile rules** (`profile.Match`).
- **handled = false** → fall through to the existing raw-http evaluation (operation templates,
  URL-pattern, browse) — so a profiled upstream supports **both** rule sets at once (see Part B for
  why Citeck needs raw-http rules for `/gateway/observer/…`).

`decide.go` stays citeck-free: it talks only to the `Profile` interface and the existing raw-http
path. Precedence (deny > require-approval > allow, agent-tier before any-tier) is unchanged and
applies within whichever rule set is selected for the request.

### Storage

- `upstreams.profile TEXT NOT NULL DEFAULT 'raw-http'` (schema + forward migration per ADR-0027).
- Rules gain generic, profile-agnostic columns: `profile TEXT` + `profile_params JSON` (the plugin
  defines the params shape). This mirrors how k8s rules added their own dimensions; here the core
  carries an opaque blob instead of plugin-specific columns. A `raw-http` rule leaves these empty
  and uses the existing op-template / k8s columns.

---

## Part B — Citeck profile (Records policy)

Lives entirely in `internal/serverprofile/citeck`. Knows the Citeck Records API.

### Recognized requests

`POST /api/records/query`, `/api/records/mutate`, `/api/records/delete`, and the gateway-prefixed
variants (`/gateway/api/records/…`, `/gateway/<app>/api/records/…`). Anything else (assets, pages,
`/gateway/observer/…`, other APIs) → `handled=false` → raw-http path.

### Classification (from the request body)

Read/write mirrors the platform's own `readOnly` flag: **query = read**, **mutate / delete = write**.

Extracted per request (the plugin parses the JSON body; `appName/sourceId@localId` ref parsing
replicated from EntityRef semantics):

- **sourceId(s)** — *every* one in the request, normalized (strip optional `appName/`, take the part
  before `@`):
  - query (search): `query.sourceId` **and** `query.ecosType` (ecosType is an alternate DAO selector —
    gating only sourceId would be bypassable via ecosType).
  - query (get-atts): the sourceId of every ref in `records[]`.
  - mutate: the sourceId of every `records[].id`.
  - delete: the sourceId of every ref in `records[]`.
- **workspace(s)** — asymmetric, and this shapes what is enforceable:
  - query: `query.workspaces[]`. **Empty/absent = ALL workspaces** (platform default) — so a rule that
    requires workspace Z must **reject** an empty/absent list, not treat it as safe.
  - mutate **create** (empty localId): `_workspace` in `records[].attributes`.
  - mutate **update** / **delete**: workspace is **server-side state, not in the body** → cannot be
    gated per workspace from the request. These are gated by **sourceId only**.

### Rule model

A Citeck profile rule (`profile = "citeck"`, params JSON):

```
{ op: "read" | "write",
  sourceId: "<glob>",      // e.g. "emodel/type", "*", "ecos-data/*"
  workspace: "<glob>",     // applies to read + create; ignored (with UI note) for update/delete
  outcome: "allow" | "deny" | "require-approval" }
```

Matching rules:

- `op` matches the classified read/write.
- `sourceId` glob must match **every** extracted sourceId (a batch touching an out-of-scope sourceId
  is not allowed by an allow rule).
- `workspace` glob: for **read**, the request's `query.workspaces` must be **non-empty and a subset**
  of the rule's workspace set; for **create**, `_workspace` must match; for **update/delete** the
  workspace dimension is not applied (sourceId only).
- Default-deny stands: a Records request with no matching allow → denied.

### Raw-http rules on a Citeck upstream

`/gateway/observer/…` (and other non-Records paths) are served by the raw-http engine, so a Citeck
upstream **also** carries raw-http / URL-pattern rules (e.g. allow `GET /gateway/observer/**`). Both
sets coexist; the profile decides per request which engine applies.

---

## Part C — UI (host registration + rule editors)

- Host registration form gains a **Server type** selector: `Raw HTTP` | `Citeck` (populated from the
  backend's registered-profiles list + each profile's `RuleSchema`, so a future plugin appears
  without UI-core changes — it only needs its own editor component).
- **Citeck editor:** a Records-rules section — rows of `(op: read/write, sourceId pattern, workspace
  pattern) → allow/deny/approval` — **plus** a raw-http / URL-pattern section (e.g. allow
  `GET /gateway/observer/**`). Update/delete rows show a "workspace not enforced" note.
- `Raw HTTP` keeps the current operation-template / URL-pattern editor.

---

## Part D — Per-upstream origin (browser transport, ADR-0035)

Lets a browser load a full web app through outwall by giving each upstream its own **origin** instead
of a path prefix on a shared one.

- **Same listener, host-based routing.** The data plane keeps listening on `127.0.0.1:<port>`. The
  proxy inspects the `Host` header:
  - `Host == <name>.<base-domain>` (default base `outwall.localhost`) → upstream = `<name>`, the
    **full** request path is forwarded (no prefix strip), so root-relative and relative URLs resolve
    to the same subdomain = the same upstream.
  - `Host` is `127.0.0.1` / `localhost` → the **existing path-prefix** model (`/<name>/…`) —
    API clients and kubectl are unchanged.
- **TLS via per-SNI leaf certs.** A `GetCertificate` callback issues (and caches) a leaf from the
  local CA for the requested SNI when it ends in the base domain; the existing static
  `127.0.0.1`/`localhost` cert serves the IP path. Per-SNI issuance covers dotted upstream names
  (`enterprise.ecos24.ru.outwall.localhost`), which a single `*.outwall.localhost` wildcard would
  not match. Chromium resolves any `*.localhost` to loopback natively, so Playwright needs no
  `/etc/hosts` entry (only to trust the outwall CA, or run with `ignoreHTTPSErrors`).
- **Bounded response rewriting in browse (host-based) mode only:**
  - `Location` header — if it points at the upstream's real origin, rewrite scheme+host to the
    subdomain origin so login redirects stay inside the proxy (relative `Location` is left as-is).
  - `Set-Cookie; Domain=…` — strip the `Domain` attribute so the cookie binds to the subdomain
    origin (a real-host domain would be dropped by the browser). `Path`/`Secure`/`SameSite` kept.
  - **Page content (HTML/CSS/JS) is NOT rewritten** — root-relative/relative URLs already resolve
    correctly under the per-upstream origin; JS-built absolute URLs to the real host remain the known
    residual (rare for same-origin SPAs). Path-prefix mode keeps `ModifyResponse` audit-only.
- **Auth + CSRF unchanged**, and isolation improves: each upstream is now a **distinct origin**, so
  the ADR-0033 single-origin confused-deputy is closed — a page from upstream A cannot read upstream
  B's cookie, and a `fetch` to B's origin is cross-site (the `Sec-Fetch-Site` guard rejects cookie
  auth). The `outwall_token` cookie is set per subdomain origin by the agent.
- **Discovery.** `get_access` (and `list_upstreams`) additionally return a **`BrowseURL`**
  (`https://<name>.<base-domain>:<port>`) for http upstreams, alongside the existing `BasePath`.

---

## Part E — Docs, sequencing, scope

- **ADR-0034** — server profiles + the citeck plugin, **and** the contained relaxation of the "no
  citeck" rule (citeck strings allowed only inside the plugin package + the persisted profile-name
  value). `AGENTS.md` and the memory `project-identity` note are updated to record the exception.
- **ADR-0035** — per-upstream origin (subdomain data plane).
- **Build order:** A + B + C first (the Citeck policy is independently useful for the agent's direct
  API calls), then D (browser transport). One shared spec (this file); separate implementation plans.
- **Out of scope:** dynamic/`.so` plugin loading (Go plugins are CGO/fragile — "plugin" here means a
  compile-time self-registering package); HTML/CSS/JS content rewriting; gating Citeck update/delete
  per workspace (not derivable from the request body).

## Non-obvious facts this design depends on

- Citeck read/write is the platform's `readOnly` flag: query=read, mutate/delete=write.
- `query.workspaces` empty = **ALL** workspaces (not "none") — must be rejected, not allowed.
- `query.ecosType` is an alternate DAO selector to `sourceId` — gate both.
- update/delete carry no workspace in the body — server-side state; only sourceId is enforceable.
- Records requests can be **batches across multiple sourceIds** — extract and check every element.
- A `*.outwall.localhost` wildcard cert matches one label only; dotted upstream names need per-SNI
  certs. Chromium resolves `*.localhost` → loopback regardless of label count.
</content>
</invoke>
