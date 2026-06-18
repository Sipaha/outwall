# ADR-0015: Operation-access MCP + approval entry points

- **Status:** accepted
- **Date:** 2026-06-18

## Context

ADR-0014 (H1) built the operation-template policy engine and wired it into the proxy decision
seam: the proxy parses a real request, matches it against the upstream's approved operation
templates, and on a new text value parks a one-click data-plane new-value approval that **extends**
the matched rule. H1 left two gaps the operation-access design (§3, §4) requires:

1. There was no way for an agent to **declare a new host or a new operation up front** over the
   control plane — the rich request a bare HTTP call cannot carry (structure + intent). H1's
   `request_access(host, purpose)` only logged an access intent against an already-registered
   upstream; it could neither register a host lazily nor describe an operation shape.
2. There was no path from an **operator approval** to **creating** an operation rule — H1's proxy
   path could only *extend* a rule that already existed, and the rule had to be hand-authored.

H2 fills both, reusing the existing pieces: `internal/optemplate` (the template type), the
operation `policy.Rule` + `AddAllowedValue` (the engine), and the blocking `approval.Queue` (the
operator queue). No new dependency; the MCP go-sdk stays at its current version; k8s is untouched.

## Decision

**Host = upstream, registered lazily.** `upstream.Registry.GetOrCreateByHost(host) (*Upstream,
bool, error)` returns the http upstream for a host, creating a credential-less one
(name = host, `BaseURL = "https://"+host`, auth `none`) when absent; the bool reports creation. It
is idempotent. `Registry.SetAuth(id, AuthConfig)` re-encrypts and attaches the operator-entered
credential to that upstream — masked at rest exactly like `Create` (vault-encrypted blob).

**Two MCP channels (SDK-free service + thin adapter, unchanged split).**
- `request_host_access(host, purpose)` (tier-1) lazily registers the host, logs the access
  intent, and — unless a rule already grants the host — enqueues a `KindHostAccess` approval. On
  approve the operator optionally attaches the host credential.
- `request_access(host, method, path_template, query_template, variables[], values, purpose)`
  (tier-2) validates the proposed template with `optemplate.Parse` (**a malformed template is a
  tool error, not a pending**), then enqueues a `KindOperation` approval carrying the parsed shape
  + the requested values + purpose.
- Both are **non-blocking**: the service parks the `Pending` on the queue from a background
  goroutine and returns `granted | pending | denied` immediately; the agent polls `get_access`.
  `AccessResult.Status` for the not-yet-granted case is now `pending` (was `pending-approval`).

**`approval.Pending` gains a `Kind` discriminator** (`host-access` / `operation`; empty = the
pre-H2 data-plane new-value / k8s approval) plus `Host` and the operation fields (`OpMethod`,
`OpPathTemplate`, `OpQueryTemplate`, `OpVariables`, `OpValues`). `Queue.Get(id)` lets the daemon
inspect a pending before resolving it.

**Resolve → create or extend the rule (daemon).** `hApprovalResolve` runs side effects **before**
unparking the waiter (a failed write reports an error instead of silently approving):
- `KindHostAccess` + `auth` → `SetAuth` attaches the credential.
- `KindOperation` → `approveOperation`: parse the template, look up the upstream's rule whose
  `optemplate.Template.Key()` equals the pending's (the H1 identity). **Absent** → `policy.Create`
  an `allow` rule with a per-variable value policy (date → `any`; text → a `set` seeded with the
  requested value, or `any` when the per-variable `trust_any` flag is passed). **Present** →
  extend it: `AddAllowedValue` for each requested value, or the new `SetVariableAny` for a trusted
  variable. Deny → no rule change.

`policy.Registry` gains `SetVariableAny(ruleID, var)` (flip a text var to `any`, drop its set) on a
shared `updatePolicies` load-mutate-save helper that `AddAllowedValue` now also uses.

## Alternatives considered

- **A parallel non-blocking pending store for the MCP approvals.** Rejected — the design says
  reuse the one blocking queue. We park `Submit` in a background goroutine instead: the pending
  shows up in `List()` and resolves through the same path, with no second mechanism to keep
  consistent.
- **Do the rule create/extend inside the parked goroutine on approve.** Rejected — the side
  effect must run whether or not a goroutine is still parked, and an error must reach the operator.
  Running it in `hApprovalResolve` before `Resolve` gives a synchronous, reportable result; the
  goroutine just discards the unblocked bool.
- **Trust the agent-declared `variables`/`values` as the enforced scope.** Rejected (restates
  ADR-0014) — the declared values only seed the approval card and the allowed-set; enforcement
  still parses the real request.

## Consequences

- An agent bootstraps access end-to-end over MCP: `request_host_access` → operator approves +
  attaches the token → `request_access` describes the operation → operator approves → the rule
  exists; later requests for new values are the H1 one-click data-plane extension. The two MCP
  cards and the data-plane new-value card all feed one queue and one rule per template.
- `request_access` changed shape (host/purpose → the full operation payload) and the host channel
  moved to `request_host_access`; `AccessResult.Status` uses `pending`. Alpha, no released
  clients — no compatibility path (ADR-0014's stance).
- The rich approval **cards** (form, types, example URL, broad-placeholder warning,
  approve / approve+trust-any) and the Operations screen are **H3** (ADR-0016). H2 ships the
  service + resolve wiring; the admin API already carries the fields (`kind`, `host`, `op_*`,
  `trust_any`).
- `findRuleByTemplateKey` reparses each candidate rule's template on resolve (resolve is rare; not
  hot). A future "edit a rule's template in place" must keep `Template.Key()` as the rule identity
  or this lookup would mismatch.
