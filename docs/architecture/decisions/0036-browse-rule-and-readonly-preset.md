# ADR-0036: Browse rule primitive + Citeck ReadOnly preset + Records-path broadening

- **Status:** accepted
- **Date:** 2026-06-23

## Context

After ADR-0034 (server-profile plugin + Citeck Records) and ADR-0035 (per-upstream origin for
browser browsing), the data plane can serve a full web app on its own subdomain origin and the
Citeck Records API is gated per operation × sourceId × workspace. But **browsing is still blocked**:
the policy default-deny applies to every request, and the only rule shape is the operation template
(method + typed path-variable set, ADR-0014/0020) or the Citeck profile rule (Records op). A browser
fetching an SPA makes dozens of requests for HTML, CSS, JS, font, and XHR assets, none of which match
a named operation. A live Playwright load of the Citeck app returns `403 access denied` on every asset
because no rule covers them.

Two needs arise simultaneously:

1. **Plain HTTP upstreams**: an operator wants "allow all GET on this host" — a coarse browse grant
   for ordinary HTTP stands, without having to enumerate every asset path as an operation template.
2. **Citeck upstreams**: the operator wants "ReadOnly" — browse the app AND execute Records read-queries
   (POST `…/records/query`) but not mutate or delete. This is two concerns composed: a coarse browse
   grant (HTML/assets) plus a Records profile rule (op=read).

Separately, the `recordsOp` suffix matcher in the Citeck plugin was verified to need broadening: the
Citeck app can POST to `/gateway/records/query` (no `/api/` segment), which the existing suffix
`/api/records/{op}` does not match. A broader suffix `/records/{op}` covers all observed variants.

The UI also grouped all http upstreams on a single Hosts page and k8s clusters on a separate Clusters
page; operators navigating a mixed environment find it easier when the three natural zones (plain HTTP /
Citeck / Kubernetes) are explicit tabs.

## Decision

### Browse rule primitive

`policy.Rule` gains two new fields: `BrowseMethods string` (comma-separated HTTP methods, e.g.
`"GET,HEAD"`; empty or `"*"` = any method) and `BrowsePath string` (a glob, e.g. `"/**"`; an
**empty `BrowsePath` means not a browse rule**). Stored as new columns `browse_methods TEXT NOT NULL
DEFAULT ''` and `browse_path TEXT NOT NULL DEFAULT ''` (additive forward migration, per ADR-0022/0023;
existing rules default to `''` and are unaffected).

Engine evaluation in `decide.go`, raw-http loop:

- A rule with `BrowsePath != ""` is a **browse rule**: matched as
  `methodMatch(BrowseMethods, req.Method) && MatchGlob(BrowsePath, req.Path)` → `rule.Outcome`.
  `methodMatch` treats `""` or any token `"*"` as any method; otherwise case-insensitive membership
  in the comma-split set. `MatchGlob` is the existing glob helper (supports `*`/`**`).
- A rule with `BrowsePath == ""` keeps the existing path: `evalHTTPRule` (operation template).
- Profile rules (`rule.Profile != ""`) are still skipped on the raw-http path (unchanged from ADR-0034).

Browse rules coexist with operation-template rules on the same upstream. On a Citeck upstream, browse
rules are evaluated when the citeck profile returns `handled=false` — i.e., for every non-Records path
(HTML, assets, `/gateway/observer`, …). Records paths go through the citeck profile as before. The
most-restrictive-wins precedence (deny > require-approval > allow) applies across all matching rules,
whether browse or template.

The control API (`POST /upstreams/{name}/rules`) accepts and returns `browse_methods`/`browse_path`
in the rule JSON payload. The Rules page gains a **Browse rules** section (browse rules have
`browse_path != ""` and no operation-template or k8s fields) so operators can see and delete them.

### Citeck ReadOnly preset (UI only; no backend preset endpoint)

On a Citeck-profile upstream, the operator clicks **ReadOnly**; the web client posts two ordinary
`createRule` calls — no special backend endpoint (YAGNI: two sequential POSTs is simpler and requires
no backend change; the backend already stores and evaluates both rule kinds):

1. A **browse rule**: `{ browse_methods: "GET,HEAD", browse_path: "/**", outcome: "allow" }` —
   covers HTML/assets/pages and `/gateway/observer`.
2. A **Citeck profile rule**: `{ profile: "citeck", profile_params: { op: "read", source_id: "*",
   workspace: "*" }, outcome: "allow" }` — the citeck profile classifies a Records `query` POST as
   `op=read`; this rule allows it while mutate/delete (op=write) remain default-deny.

For a plain HTTP upstream, the operator clicks **Allow GET (browse)**; the web client posts rule 1
only. Both preset buttons target the upstream's currently selected subject (agent or "any"). The two
rules created by ReadOnly are independent rows: deleting one leaves the other (a combined "remove
ReadOnly" action is out of scope for v1).

### Records-path broadening (`recordsOp` suffix)

The Citeck plugin's `recordsOp` function previously matched the suffix `/api/records/{query,mutate,delete}`
(so `/gateway/.../api/records/query` was handled). The suffix is broadened to `/records/{op}` —
matching any path ending in `/records/query`, `/records/mutate`, or `/records/delete`, regardless of
what precedes it. This covers the three observed variants:

- `/api/records/query` (base path)
- `/gateway/api/records/query` (gateway-prefixed)
- `/gateway/records/query` (gateway, no `/api/`)

A non-Records path coincidentally ending in `/records/query` is implausible in practice, making this
broadening safe. The change is a pure suffix-test simplification with no new external dependency.

### Upstream zones (HTTP / Citeck / Kubernetes tabs)

The UI restructures upstream management into one **Upstreams** page with three tabs: **HTTP** |
**Citeck** | **Kubernetes**. The previous separate Hosts and Clusters nav entries are replaced.
Upstreams are filtered into tabs by `(kind, profile)` from `GET /upstreams`:

- **HTTP tab**: raw-http upstreams (`kind != k8s`, `profile != "citeck"`). Add-host form, per-host
  **Allow GET (browse)** preset button.
- **Citeck tab**: Citeck-profile upstreams (`profile == "citeck"`). Add-host with server type Citeck,
  Records-rule editor, and the **ReadOnly** preset button.
- **Kubernetes tab**: the existing Clusters view (import/upload, insecure-skip-tls-verify badge) folded
  under this tab. The `/clusters` route and sidebar entry merge into `/upstreams`.

A small reusable `Tabs` component manages tab state (no new dependency; controlled by URL query
parameter for bookmarkability).

## Alternatives considered

- **No browse rule; enumerate asset paths as operation templates.** Rejected: impractical — an SPA
  fetches dozens of asset hashes that change every build. The typed-template engine is for API
  operations with predictable structure, not for browser asset fetches.
- **A backend `POST /upstreams/{name}/apply-preset` endpoint for ReadOnly.** Rejected (YAGNI): the
  UI can POST two ordinary rules sequentially with no backend change. If a transactional "apply preset"
  becomes necessary (e.g., atomicity or template-driven presets), it can be added later.
- **Broaden `recordsOp` to match any POST (no suffix).** Rejected: too coarse — it would classify
  non-Records POSTs as profile-handled (and likely deny them via no-matching-rule) instead of falling
  through to raw-http rules. Suffix match retains specificity.
- **Separate loopback port per zone (HTTP/Citeck/Kubernetes) in the UI.** Not meaningful — zones are
  a classification concern for the UI; the backend already unifies them under one upstream table with
  `kind` and `profile` columns.

## Consequences

- Operators can grant coarse browse access ("GET on any path") with a single rule, enabling real web
  app browsing through outwall without enumerating every asset path.
- The Citeck ReadOnly preset (two rules) unblocks a common read-only analyst workflow: browse the app
  and issue Records read-queries, while mutate/delete stays default-deny.
- Browse rules and operation-template rules coexist on the same upstream with no precedence change;
  adding a browse rule cannot weaken an existing deny/require-approval outcome (most-restrictive-wins).
- Browse rules are deliberately coarse — they allow a method on a glob, not a typed operation. Operators
  who need fine-grained POST control still use operation templates.
- The `recordsOp` broadening removes a latent URL-variant mismatch for Citeck deployments that route
  Records through `/gateway/records/` without an `/api/` segment.
- The Upstreams tabs replace the previous Hosts + Clusters split; existing `/clusters` bookmarks
  redirect to `/upstreams` (Kubernetes tab).

Covered by tests in `internal/policy` (browse match, methodMatch, browse+operation coexistence,
precedence), `internal/serverprofile/citeck` (`recordsOp` matches all three path variants),
`internal/daemon` (control-API round-trips `browse_methods`/`browse_path`), and
`web/src` (Tabs zone filtering, Allow-GET and ReadOnly preset POSTs, Browse-rules section render +
delete).

Links: [ADR-0034](0034-server-profiles-and-citeck-plugin.md) (server-profile plugin + citeck Records),
[ADR-0035](0035-per-upstream-origin.md) (per-upstream origin for browser browsing),
[spec](../../superpowers/specs/2026-06-23-browse-policy-and-upstream-zones-design.md) (browse policy
+ upstream zones design).
