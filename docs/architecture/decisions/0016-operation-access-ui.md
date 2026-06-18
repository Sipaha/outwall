# ADR-0016: Operation-access UI

- **Status:** accepted
- **Date:** 2026-06-18

## Context

H1 (ADR-0014) rebuilt the HTTP policy engine around operation templates with typed, per-variable
value policies, and H2 (ADR-0015) added the MCP host/operation request entry points, the
create-or-extend approval resolve path (`trust_any`, host credential attach), and the data-plane
new-value approval. The embedded React console (ADR-0006) still spoke the old method + path-glob
model: a single flat "Approve/Deny" row for every pending, a path-glob "Rules" editor, and an
"Upstreams" list with no credential lifecycle. H3 brings the console up to the operation model so an
operator can actually drive the two-tier flow — approve a host, attach its credential, vet an
operation shape, gate and grow each variable's value-set — without hand-authoring rules.

The console is the only surface where the operator sees what an agent is about to be allowed to do.
The central safety force is therefore **legibility**: the operator must see the concrete request, tell
fixed structure from variables apart at a glance, and be warned before granting `any`-scope.

## Decision

Evolve the three existing screens; add no new screens, no new npm deps (React 19 / Tailwind 4 /
Zustand / Vitest as before). The admin JSON field names are matched exactly (`kind`, `host`,
`op_method`, `op_path_template`, `op_query_template`, `op_variables`, `op_values`, `new_values`,
`template`, `op_value_policies`, and `trust_any` on resolve).

**Approvals — three rich cards (`pages/Approvals.tsx`).** A card list replaces the flat table, keyed
on the approval `kind` / shape:

- **Host card** (`kind:"host-access"`): agent + host + purpose; an inline credential form (static
  header/value or basic); **Approve** posts `resolveApproval(id, true, { auth })` (or no `auth` when
  the operator picks "None — attach later") / **Deny**.
- **Operation card** (`kind:"operation"`): the operation shape with fixed vs `{name:type}` segments
  rendered as distinct spans; a **concrete example URL** built by substituting `op_values` into the
  path + query template; per **text** variable a **"trust any value"** checkbox (date variables show
  "auto", no checkbox); a **broad-placeholder warning banner** shown whenever any variable is checked
  `any`. **Approve** posts `{ trust_any: [...] }`; **Deny**.
- **New-value card** (data-plane, empty `kind` + `new_values`): the matched `template` + each new
  `(var, value)`; **Approve** / **Approve + trust any** (posts `{ trust_any: [vars] }`) / **Deny**.

**Operations screen (`pages/Rules.tsx`).** The path-glob editor becomes an operation-template view:
each http operation rule is a card showing the segmented template and, per text variable, an inline
**value-set editor** — removable value chips, an add-value input, and a "trust any value" toggle.
Each edit recomputes the whole policy client-side and posts it via the new
`POST /rules/{id}/value-policy`. Date variables render as "auto (any date)". k8s tuple rules keep
their own table; the create-operation modal is retained (renamed).

**Hosts screen (`pages/Upstreams.tsx`).** The upstream list becomes a host list with a **credential
status** column (`credential set (type)` vs `no credential`, derived from `auth_type`), a
set/replace-credential modal posting the new `POST /upstreams/{name}/auth`, and a remove-host action
(`DELETE /upstreams/{name}`). Sidebar labels: "Upstreams" → **Hosts**, "Rules" → **Operations**.

**Two minimal admin endpoints were added** (no consumer existed):

- `POST /upstreams/{name}/auth` → `upstream.SetAuth` (set/replace a host credential; write-only —
  secrets never return on the list).
- `POST /rules/{id}/value-policy` → new `policy.Registry.SetVariablePolicy(ruleID, var, policy)`,
  which replaces one variable's policy, **keeps the declared `Type`** (the operator can change
  Mode/Values but not retype a slot), dedupes a `set`'s values, and **rejects an unknown variable**
  so a typo can never silently widen a rule. One uniform endpoint serves add / remove / trust-any
  rather than three. Both stay CGO-free and add no Go dependency.

## Alternatives considered

- **Three endpoints (add-value / remove-value / set-any).** Rejected: more surface, more handlers,
  and the registry already had `AddAllowedValue`/`SetVariableAny` for the approval path — a single
  "replace this variable's policy" endpoint subsumes all three and keeps the client logic trivial
  (recompute + post the whole policy).
- **Modal-per-approval instead of cards.** Rejected: the operator triages a queue; inline cards keep
  the example URL, the variable controls, and the warning visible together without a click.
- **Let the UI send the variable `Type` on value-policy edits.** Rejected: the server owns the
  declared type; accepting it from the client invites a `date`→`text` retype that the engine's
  parse-from-request guarantee is meant to prevent. The registry ignores the posted type.

## Consequences

- The operator can run the whole operation-access loop from the console: approve a host + attach its
  credential, vet an operation (seeing the real example URL and being warned on `any`), and grow or
  trim each variable's value-set or flip it to `any` outside an approval.
- `setRuleVariablePolicy` posts the *recomputed* set, so a stale view could clobber a concurrent
  edit; acceptable single-operator (loopback, single-tenant) tradeoff, and each edit refetches via
  the `rule.updated` SSE counter. A future multi-operator model would need add/remove deltas.
- The webui module doc is updated. No INDEX / current-phase edits (per the H3 plan). A future variable
  type (`number`/`enum`) would extend the value-set editor and the operation card's per-variable row.
