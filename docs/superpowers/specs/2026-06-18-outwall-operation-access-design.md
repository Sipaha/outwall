# outwall — Operation-Access Model Design Spec

**Date:** 2026-06-18
**Status:** Draft (brainstorming) — pending user approval, then implementation plans
**Builds on:** `2026-06-17-outwall-design.md` (HTTP egress gateway, Phase 1). This spec **replaces
the HTTP-side policy model** (method + path-glob rules) with an operation-template + typed-variable
model. The k8s plane (`2026-06-18-outwall-k8s-gateway-design.md`) is unchanged.
**No backward compatibility:** there has been no release and no stored data — the HTTP path-glob
rule mechanism is removed outright, not migrated.

## 1. Purpose

Let an operator **control** an agent's HTTP egress by **approving the requests the agent actually
needs**, instead of hand-authoring a rule for every path up front. The agent asks for access to an
**operation** — a parameterized request shape — stating **why** (purpose) and with its important
inputs pulled out as **typed variables**; the operator approves the shape once and controls **which
variable values** are allowed, extending that set over time. outwall enforces by **parsing the real
request** and checking the extracted values — never by trusting what the agent declares.

Driving example: a fresh Claude Code session (the agent) wants "when did the pipeline at
`gitlab.citeck.ru/infrastructure/helm-releases/enterprise-ecos24` last run". It requests the
operation `GET /api/v4/projects/{project_path:text}/pipelines?updated_after={since:date}` on host
`gitlab.citeck.ru`, purpose "check CI state", with `project_path =
infrastructure/helm-releases/enterprise-ecos24`. The operator approves; outwall injects the GitLab
token (never seen by the agent) and proxies. A later request for a different project is a one-click
new-value approval that extends the same operation's allowed set.

**Core invariant (unchanged):** upstream secrets never leave outwall; the agent sees only a local
URL and its own token. Enforcement is on the **parsed real request**, not on agent self-declaration.

## 2. Model & data

### 2.1 Host = upstream (no name)

An HTTP **upstream is a host**. Routing reuses the first-path-segment scheme:
`https://127.0.0.1:8099/<host>/<path>` → `https://<host>/<path>`. Hosts are registered **lazily**
(they appear when an agent first requests access). The host carries its encrypted **auth** (e.g. a
GitLab token) in the vault, attached by the operator at host-approval time. A host grant by itself
**opens no traffic** — it only lets the agent request operations on that host and lets the operator
attach the credential.

### 2.2 Operation template

The unit of access is an **operation template**:
`(host, method, path-template, query-template)`. Path segments and query values may be **typed
placeholders**: `GET /api/v4/projects/{project_path:text}/pipelines?updated_after={since:date}`.

- Placeholders are **segment-bounded** — `{x:text}` matches exactly one path segment (no `/`), the
  same discipline as the k8s segment parser (`*` within a segment, `**` across). A template never
  silently over-captures extra segments; the full path structure must match.
- A template with **no** placeholders is just a fixed path (this subsumes the old path-glob rules).
- Query placeholders bind to a named query parameter's value. Query parameters **not named in the
  template** are, by default, **denied** (a request carrying undeclared query params does not match)
  — so the operator's approval covers the whole scope-bearing surface. (A small allow-list of
  scope-neutral params — pagination — may be exempted; see §7.)

### 2.3 Operation rule = template + per-variable value policy

An approved operation persists as a **rule**: the template plus, **per variable**, a value policy:

- `date` → **`any`** (auto-allowed; the extracted value must merely parse as a date).
- `text` → an explicit **allowed-set** `{v1, v2, …}` **or** `any` (when the operator toggled "trust
  any value" for that variable).

Approvals **extend** the allowed-set on the **same** template — they do not create a new rule per
value. Two requests with the same `(host, method, path-template, query-template)` map to one rule;
its value-sets grow.

### 2.4 Enforcement — parse, don't trust

The agent sends a **real HTTP request** to the data plane (the proxying contract is unchanged).
Per request, at the proxy's single decision seam:

1. Match the request against the host's approved operation templates (method + path structure +
   declared query params). No match → **deny (403)** ("request access first").
2. On a match, **outwall extracts** each variable's value from the real URL (path segment / query
   value) and validates its type (`date` must parse as a date; a `date` slot with a non-date value →
   blocked as suspicious).
3. Check each extracted value against the rule's policy:
   - all allowed → **proxy** (inject the host's vault credential) + audit.
   - a `text` value not in the set → **require-approval** ("new value") — block via the existing
     long-poll; on approve, the value is added to the set and the request proceeds; on deny → 403.

Because the values are parsed by outwall **from the real request**, an agent cannot widen scope by
mis-declaring; the only thing the agent influences is the **template it proposed**, which the
operator vetted.

## 3. Two channels, two approval entry points

outwall keeps its two planes; access metadata flows on the control plane, traffic on the data plane.

- **MCP control plane — rich requests (new host / new operation).** The agent declares structure +
  intent, which a bare HTTP request cannot carry:
  - `request_host_access(host, purpose)` → tier-1 host card.
  - `request_access(host, method, path_template, query_template, variables[], values, purpose)` →
    tier-2 operation card. `variables[]` are `{name, type}` (`text`|`date`); `values` are the
    concrete values the agent intends to use.
  - Both return `granted | pending | denied`; on `pending` the agent polls `get_access` / retries
    (the MCP call does not block).
  - `list_upstreams` lists hosts and the agent's access; `whoami` unchanged.
- **Data plane — new-value approvals on an already-approved template.** A raw request whose template
  is approved but whose `text` value is new triggers a **value approval** directly (outwall already
  knows the template + types, so no agent metadata is needed). This realizes "let the agent just
  send the request it needs and we approve it."

**Tiers are two explicit steps:** host access is approved on its own (and the token attached) before
operation requests are entertained; operation access is the fine gate.

## 4. Approval UX

All cards land in the existing blocking approval queue + UI.

- **Host card (tier-1):** "Agent A wants to reach `gitlab.citeck.ru`. Purpose: …". Actions:
  **Approve** (+ attach/enter the host credential now or later) / **Deny**.
- **Operation card (tier-2):** shows the operation form, each variable's **type**, a **concrete
  example URL** built from the requested values, and the purpose. Actions: **Approve** (adds the
  requested values to each `text` variable's set) / **Approve + trust any value** (per-variable
  toggle → `any`) / **Deny**.
- **New-value card (data-plane):** "Agent A: new value `X` for variable `project_path` on operation
  `GET projects/{project_path}/pipelines`." Actions: **Approve** (extend the set) / **Approve +
  trust any value** / **Deny**.
- **Safety in the card:** make fixed vs variable segments unmistakable; render the concrete example;
  **warn on broad placeholders** (`any`, or any cross-segment capture) so the operator does not
  blind-approve "any project". The "approve" action is exactly the user's "approve = add the rule".

- **Operations screen:** lists each host's operation templates with their per-variable value-sets;
  the operator can add/remove allowed values and toggle a variable to `any` outside of an approval.

## 5. Variable types (MVP)

- `text` — gated by value (the control unit). Extracted as a single path segment or a query value.
- `date` — auto-allowed (`any`); the extracted value must parse as a date (ISO-8601 / common
  forms). A scope-bearing slot cannot be hidden as a `date`, because a non-date value fails
  validation and is blocked.

Other types (`number`, `enum`, request-body variables) are out of scope for the MVP (§9).

## 6. Relationship to the existing policy engine

The `policy` package currently serves HTTP (method + path-glob) and k8s (namespace/resource/verb).
This spec **rewrites the HTTP path** to the operation-template model; the k8s tuple path is
untouched. Since there is no released data, the path-glob HTTP rule type, its storage, CLI, and UI
are **removed**, not migrated. `policy.Decide` keeps its role as the proxy's single decision seam,
gaining HTTP template matching + value extraction; `require-approval` and the blocking queue are
reused; rate-limit stays per-rule.

## 7. Security considerations

- **Parse-from-request, not declaration** (§2.4) is the central guarantee.
- **Undeclared query params are denied by default** so approval covers the full scope surface; a
  short, configurable exemption list for scope-neutral params (e.g. `page`, `per_page`) avoids
  noise without widening scope. Exempt params are still audited.
- **Type can't be used to dodge gating:** a `date` slot validates as a date, so a `text` scope value
  cannot ride a `date` placeholder.
- **Broad-placeholder warnings** in the operator UI prevent accidental `any`-scope approvals.
- **Host credential** is attached by the operator and injected server-side; never a "variable",
  never seen by the agent (unchanged egress invariant).
- The agent only ever influences the **proposed template** (vetted at approval) and the **values**
  (gated). It cannot alter enforcement.

## 8. Audit

Reuses the Phase-1 journal. Per request: agent, host, the **matched operation template**, the
**extracted variable values**, decision (+ approver/when), status, sizes, req/resp bodies (≤256 KB),
injected credential masked. New-value and operation approvals are recorded with their purpose.

## 9. Scope / delivery plans (next free ADR = 0014)

- **H1 — operation-template policy engine + proxy enforcement + host model.** Lazy host==upstream;
  the template type (typed, segment-bounded placeholders reusing the k8s segment parser); the
  operation rule (template → per-variable value policy: text set/`any`, date `any`); `Match(request)
  → (rule, extracted vars)` + value/type checks; wired into the proxy decision seam (deny /
  proxy+inject / require-approval-new-value); undeclared-query-param denial + exemption list. Remove
  the old path-glob HTTP rule type. Heavy unit tests (matching, extraction, no over-capture, date
  validation, value gating). ADR-0014.
- **H2 — enriched MCP + approval entry points.** `request_host_access`, `request_access`
  (host/method/templates/variables/values/purpose) → pending/granted/denied; `list_upstreams` hosts;
  `get_access`; the two approval entry points feeding the existing queue; approve/deny → create or
  **extend** the operation rule; "approve + trust any value"; host credential attach on host
  approval. ADR-0015.
- **H3 — UI.** Host registration + credential attach at host-approval; operation approval cards
  (form, types, example URL, broad-placeholder warning, approve / approve+trust-any / deny);
  new-value card; an Operations screen (templates + value-sets, add/remove, trust-any toggle).
  Evolve Approvals / Rules / Upstreams. ADR-0016.

## 10. Out of scope (explicit YAGNI)

- Variable types beyond `text` and `date` (`number`/`enum`/regex), and request-**body** variables.
- Per-path differing host credentials (one credential per host for the MVP).
- Cross-segment (`**`) variable capture as a first-class type (a template may still be fixed-multi-
  segment, but a *variable* binds one segment in the MVP).
- Async/ticketed approval fallback beyond the existing long-poll (already deferred in Phase 1).
- Touching the k8s plane's policy (it keeps its own tuple).
