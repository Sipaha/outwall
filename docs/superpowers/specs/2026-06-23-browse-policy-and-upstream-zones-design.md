# Design: browse policy (allow-GET + Citeck ReadOnly) + upstream zones (tabs)

- **Date:** 2026-06-23
- **Status:** approved (brainstorming) — pending implementation plan
- **Companion ADR:** ADR-0036 (browse rule primitive + Citeck ReadOnly preset + Records-path matcher broadening).
- **Builds on:** ADR-0034 (server profiles + citeck plugin), ADR-0035 (per-upstream origin). The
  per-upstream origin is verified live (Playwright reaches `https://enterprise.ecos24.ru.outwall.localhost:8099/`
  through outwall: DNS/SNI/cookie/Host-routing all green; the only gate left is policy default-deny).

## Why

The data plane and per-upstream origin work end-to-end, but **browsing a real web app is blocked by
the policy model**. Rules are per-operation HTTP templates (`method + {var:type}` path) — there is
no way to say "allow all GET on this host", which is what a browser needs (it fetches HTML, then
dozens of asset/page/XHR paths). A live Playwright load of the Citeck app returns
`403 {"error":"access denied"}` for exactly this reason. Operators need:

1. a plain **"allow GET (browse)"** grant for ordinary (non-citeck) HTTP stands, and
2. a **Citeck "ReadOnly"** grant that allows browsing **plus** Records read-queries (POST
   `…/records/query`) but not mutate/delete.

Separately, the UI lumps all http upstreams on one "Hosts" page and k8s on a separate "Clusters"
page; the operator wants the three **zones** (plain HTTP / Citeck stands / Kubernetes) clearly
separated — tabs.

## Part 1 — Browse rule (raw-http allow-method primitive)

A new, deliberately coarse raw-http rule shape that sits alongside the typed operation-template
engine (which stays for fine-grained API control):

- **`(method-set, path-glob) → outcome`**, matched by glob, not by the typed template.
- `policy.Rule` gains two fields: `BrowseMethods string` (comma-separated, e.g. `"GET,HEAD"`; `""`
  or `"*"` = any method) and `BrowsePath string` (a glob like `"/**"`; **non-empty marks the rule as
  a browse rule**). Stored as columns `browse_methods`, `browse_path` (forward migration; defaults
  `''`).
- **Engine** (`decide.go`, raw-http loop): a rule with `BrowsePath != ""` is evaluated as
  `methodMatch(BrowseMethods, req.Method) && MatchGlob(BrowsePath, req.Path)` → `rule.Outcome` (reuse
  the existing `MatchGlob`, which already supports `*`/`**`). A rule with `BrowsePath == ""` keeps
  using `evalHTTPRule` (operation template). Profile rules are still skipped on the raw-http path.
- **Coexistence:** browse rules are raw-http rules (`Profile == ""`), so on a Citeck upstream they are
  evaluated exactly when the citeck profile returns `handled=false` (a non-Records path — HTML,
  assets, `/gateway/observer`, …). Records paths still go through the citeck profile. Both rule sets
  live on the same upstream, as today.
- **Default outcome:** typically `allow`, but `deny`/`require-approval` are valid (a browse rule is
  just a coarse matcher with an outcome).

`methodMatch(set, m)`: `set==""` or any token `== "*"` → match; else case-insensitive membership of
`m` in the comma-split set.

## Part 2 — Citeck "ReadOnly" preset

A UI convenience that creates the right rule(s); the backend stores ordinary rules (no special
endpoint). On a Citeck-profile upstream, **"ReadOnly"** creates **two** rules (both `outcome: allow`,
subject = the chosen agent or "any"):

1. a **browse rule** `{ browse_methods: "GET,HEAD", browse_path: "/**" }` — HTML/assets/pages +
   `/gateway/observer`.
2. a **citeck profile rule** `{ profile: "citeck", profile_params: { op: "read", source_id: "*",
   workspace: "*" } }` — the citeck profile classifies a Records `query` POST as `read`, so this
   covers the read-query without allowing mutate/delete (those stay default-deny).

For a plain HTTP / non-citeck upstream, **"Allow GET (browse)"** creates rule 1 only.

These are normal rules in the rules table; they must be **visible and deletable** in the Rules
screen (a browse rule has `BrowsePath` set and no `op_path_template`/k8s fields, so the existing
operation/k8s/profile filters would hide it — it gets its own visible grouping, see Part 4).

## Part 3 — Records path matcher: confirm live, then broaden

The citeck profile's `recordsOp` currently suffix-matches `/api/records/{query,mutate,delete}` (so
`/gateway/.../api/records/query` matches). The operator reports the app may POST to
`/gateway/records/query` (no `/api/`). **First implementation step:** with the browse-GET rule in
place, drive Playwright to load the Citeck app and **capture the actual Records request path** from
the network log. Then make `recordsOp` robust: match the suffix **`/records/{query,mutate,delete}`**
(covers `/api/records/query`, `/gateway/records/query`, and `/gateway/api/records/query`). A
non-Records path coincidentally ending in `/records/query` is implausible; the broadened matcher is
safe and removes the URL ambiguity. (Recorded in ADR-0036.)

## Part 4 — Upstream zones (tabs)

Restructure upstream management into one tabbed **Upstreams** page with three tabs — **HTTP** |
**Citeck** | **Kubernetes** — replacing the separate Hosts and Clusters nav entries:

- A small reusable `Tabs` component (no new dep; controlled by local state or the URL query).
- **HTTP tab:** raw-http upstreams (`kind != k8s`, `profile != "citeck"`). Add-host form (server
  type `Raw HTTP`), per-host actions including **"Allow GET (browse)"**.
- **Citeck tab:** citeck-profile upstreams (`profile == "citeck"`). Add-host with server type
  `Citeck`, the Records-rule editor, and the **"ReadOnly"** preset button.
- **Kubernetes tab:** the existing `Clusters` view (import/upload, no-auth badges), moved under this
  tab. The `/clusters` route + sidebar entry fold into `/upstreams`.
- Upstreams are filtered into tabs by `(kind, profile)` from `GET /upstreams` (already returns both).
- The **Rules** screen keeps its existing sections but adds a visible **Browse rules** group (browse
  rules rendered with method/path-glob/outcome + delete) so the Part 2 rules aren't invisible. Light
  zone grouping of Rules is otherwise out of scope for v1.

## Components & boundaries

- `internal/policy` — `Rule.BrowseMethods`/`BrowsePath`; `decide.go` browse-match branch; storage
  columns + migration. Self-contained; the typed-template path is untouched.
- `internal/serverprofile/citeck` — `recordsOp` suffix broadened to `/records/{op}`.
- `internal/daemon/admin.go` — control-API carries `browse_methods`/`browse_path` on rule create/list.
- `web/src/components/Tabs.tsx` (new) — reusable tabs.
- `web/src/pages/Upstreams.tsx` — tabbed page (HTTP/Citeck/Kubernetes); preset buttons.
- `web/src/pages/Rules.tsx` — Browse-rules section.
- `web/src/lib/{types,api}.ts` — `browse_methods`/`browse_path` on `Rule`.

## Error handling / edge cases

- A browse rule with an empty `BrowsePath` is NOT a browse rule (it's an operation rule); the UI never
  creates one with an empty glob (defaults to `/**`).
- `methodMatch` with `BrowseMethods == ""` means **any** method — the UI's "Allow GET" sets
  `"GET,HEAD"` explicitly so it does not accidentally allow POST.
- Browse rules and operation rules on the same upstream: most-restrictive-wins precedence is unchanged
  (deny > require-approval > allow), evaluated across all matched raw-http candidates.
- The ReadOnly preset's two rules are independent rows; deleting one leaves the other (the UI may
  offer a combined "remove ReadOnly" later — out of scope v1).

## Testing

- `internal/policy`: browse rule matches GET on any path, rejects POST when methods=`GET,HEAD`;
  browse + operation rules coexist; precedence holds.
- `internal/serverprofile/citeck`: `recordsOp` matches `/gateway/records/query` and the existing
  `/api/records/query` / `/gateway/api/records/query`.
- `internal/daemon`: control-API round-trips `browse_methods`/`browse_path`.
- web: Tabs filter upstreams by zone; "Allow GET (browse)" posts the browse rule; "ReadOnly" posts
  both rules; the Browse-rules section renders + deletes.
- **Live:** after the browse-GET rule, a Playwright load of the Citeck app renders (≥1 real page +
  the captured Records read-query returns 200 through outwall).

## Out of scope (v1)

- A backend "apply preset" transaction (UI makes the two createRule calls).
- Zone-grouping the Rules screen beyond the new Browse-rules section.
- A "remove ReadOnly as a unit" action.
- Auto-trusting the outwall CA in the operator's browser (the agent's Playwright uses
  `ignoreHTTPSErrors` / the CA explicitly; documented in ADR-0033/0035).

## ADRs / sequencing

- **ADR-0036** — the browse-rule primitive (allow-method × path-glob), the Citeck ReadOnly preset
  composition, and the `recordsOp` broadening.
- Build order: (1) browse rule (policy + control-API) and recordsOp broadening + **live URL capture**;
  (2) Citeck ReadOnly/Allow-GET presets + Browse-rules visibility (web); (3) Upstreams tabs (web).
  One spec (this file); one implementation plan.
</content>
