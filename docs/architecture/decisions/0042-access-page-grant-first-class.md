# ADR-0042: Access page — grant as first-class object; Operations/Approvals/Agents merged

- **Status:** accepted
- **Date:** 2026-07-07

## Context

The operator UI smeared three distinct concepts across three separate pages, and the boundaries
leaked into each other:

1. **Pending queue** — agent requests awaiting an operator decision (approve/deny).
2. **Request history** — an immutable log of who asked for what and how it was resolved
   (`pending / granted / denied / revoked / dismissed`).
3. **Live grants** — the policy rules that say what an agent may do on an upstream *right now*
   (mutable: edit a value-set, delete a rule, revoke a whole grant).

Concretely: **Approvals** mixed the pending queue (1) with an "Access requests" history table (2)
that carried a live **Revoke** button (3). That Revoke called
`DeleteBySubjectUpstream(agent, upstream)` — it deleted *every* rule for that (agent, upstream)
pair and marked the one clicked-on request row `revoked`, even though the grant it destroyed was
usually the product of several resolved requests. A button sitting in what read as "history"
performed a coarse, live policy mutation — the source of real operator confusion ("what does
Revoke even do, and to which row?"). Separately, **Operations** showed live rules as raw rows
disconnected from the agent/request that produced them, and **Agents** was a fourth, unrelated
list with its own metadata + delete action.

The root cause: a **grant** — the set of rules for one (agent, upstream) pair — was never a
first-class object anywhere in the UI, even though Revoke, the value-set editors, and "what can
this agent do" all operate on exactly that object.

## Decision

**Information architecture.** Nav before: Dashboard · Upstreams · Agents · Operations ·
Approvals · Audit · Settings. Nav after: **Dashboard · Upstreams · Access · Audit · Settings.**
The `Agents`, `Operations`, `Approvals` pages and their `/agents`, `/rules`, `/approvals` routes
are deleted outright (alpha; no compatibility to preserve). A single **Access** page (`/access`)
absorbs all three:

- **Запросы прав** (top section) — the aggregated pending-approval queue, unchanged in substance
  (the existing per-kind resolve cards — host/operation/preset/k8s/new-value — are reused, only
  relocated and restyled as `pages/access/ApprovalCards.tsx` + `RequestsPanel.tsx`), now scoped by
  a hero "concrete right" box (scope badge + resource/path) so the operator judges the actual
  permission, not just the stated purpose.
- **Выданные права** (below) — the grant as the organizing object. `lib/grants.ts`
  `deriveGrants(rules, requests)` groups `listRules()` by `(subject_agent_id, upstream_id)` into a
  `Grant{agentId, upstreamId, rules[], purpose, grantedAt}`, back-filling `purpose`/`grantedAt`
  from the most recent `granted` `listAccessRequests()` row for that pair. No new persistence: a
  grant is a **view**, not a stored row. `pages/access/GrantGroups.tsx` renders the grants either
  grouped by agent (`AgentCard` → `UpstreamGrantCard` per grant, default, matches the old
  Upstreams-tab persistence pattern) or by upstream (transposed), toggled and persisted via
  `lib/accessGrouping.ts`.
- **Agent metadata + delete** move onto `AgentCard`: name, short id, status dot, `N прав ·
  M ресурсов` counter, `последн. активн.`/`создан` meta, a **trash icon** that calls `DELETE
  /agents/{id}` directly (no confirmation modal — matches the pre-existing Agents-page behavior),
  and a **kebab** menu duplicating delete plus a new **"История запросов в Audit"** link
  (`/audit?tab=requests&agent=<id>`). Header click anywhere (not only the chevron) toggles
  collapse; the trash icon and kebab `stopPropagation` so they don't also toggle it.
- **Manual grant** — `+ Выдать вручную` opens `ManualRuleModal` (Task 10), the same `createRule`
  flow the old Operations "Add operation" modal used, moved onto Access as the entry point for
  granting a right without an agent request.

**Revoke is relocated onto the grant and made grant-scoped.** A new gated endpoint,
`POST /grants/revoke {agent_id, upstream_id}` (`hGrantRevoke` in `internal/daemon/admin.go`),
deletes every policy rule for the pair (`policy.Registry.DeleteBySubjectUpstream`, already existed)
and marks every currently-`granted` access request for that pair `revoked` (new
`access.Registry.MarkRevokedBySubjectUpstream`, a `WHERE agent_id=? AND upstream_id=? AND
status='granted'` bulk update — pending/denied rows are left untouched). It returns
`{ok, rules_removed}` and publishes `access.revoked{agent_id, upstream_id}`. The `UpstreamGrantCard`
Revoke button calls this via the new `revokeGrant(agentId, upstreamId)` client helper. This
**supersedes the per-request revoke semantics for the UI**: the pre-existing
`POST /access-requests/{id}/revoke` (`hAccessRequestRevoke`, keyed by one request row) is left in
place as a superset case for external/API callers and its test coverage, but nothing in the web UI
calls it anymore — `revokeGrant`/`hGrantRevoke` is the only revoke path a human operator can reach.

**Enum value-set editing was intentionally dropped from the UI.** `RuleRow`'s expandable editor
now covers `text` (chip list + add + trust-any) and `number` (min–max range + trust-any) value
policies only; `date` is a static "auto (any date)" note. An `enum` value policy is still enforced
by the policy engine exactly as before — only the editor UI is gone, because an enum's domain is
fixed by the upstream/preset schema (e.g., a Records `op` of `read`/`write`) and is not something
an operator meaningfully edits value-by-value the way a free-text or numeric range is.

**Audit gains a "Запросы прав" tab.** `pages/Audit.tsx` is now tabbed (`Трафик` | `Запросы
прав`, `components/Tabs.tsx`, URL query `?tab=`). The second tab renders the same
`listAccessRequests()` history — agent, upstream, purpose, status badge (with deny reason),
requested-at, resolved-at — that used to live on Approvals, now **read-only**: no Revoke/Dismiss
button here, since those are live mutations and now live on the grant (Revoke) or the pending
queue (approve/deny). It supports `?agent=<id>` filtering, which is how the Access agent-card
kebab's "История запросов в Audit" link scopes the view.

**Deep-link retarget.** The desktop's `desktop.open-approvals` SSE/notification signal
(`useOpenApprovalsRoute`) now routes to `/access` instead of the deleted `/approvals`.

## Alternatives considered

- **Keep three pages, just rename "Operations" rules as visually grouped by agent.** Rejected —
  the Revoke-on-a-history-row confusion is structural (the wrong object owns the action), not a
  labeling problem; splitting the pages perpetuates it.
- **A `Grants`/`Log` two-tab page (an earlier brainstormed iteration).** Rejected in favor of the
  single scrolling Access page (pending queue on top, grants below) plus the separate Audit
  history tab — fewer clicks to see "what's pending" and "what's granted" together, while history
  still gets its own read-only home under Audit rather than a third tab competing for the same
  page.
- **A transactional `DELETE /grants` backend concept with its own storage.** Rejected (YAGNI) — a
  grant is fully derivable from existing `rules` + `access_requests` rows; introducing a stored
  "grant" entity would require keeping it in sync with both tables for no behavioral gain in
  alpha.
- **Delete `hAccessRequestRevoke`/`POST /access-requests/{id}/revoke` outright.** Rejected for this
  increment — no call site remained in the web UI, but removing a still-tested, still-documented
  endpoint was out of scope for a UI-composition change; it can be pruned in a later cleanup once
  it is confirmed no external caller depends on the per-request semantics.

## Consequences

- The operator has one page to answer "what is pending" and "what can each agent do right now,"
  with Revoke sitting directly on the grant it destroys instead of on an ambiguous history row.
- Agent lifecycle (delete) and request history both moved without any backend schema change —
  grants remain a pure UI-side derivation over `rules` + `access_requests`; there is nothing new
  to keep consistent on the server.
- **Known limitation, pre-existing but newly visible:** a **host-access-only** grant (`request
  -host-access`, approved without ever adding a rule) creates no policy rule at all — the approval
  flow attaches a credential but writes nothing to `policy.Rule`. Such a grant appears as
  `granted` in the Audit "Запросы прав" history but **does not appear** in "Выданные права",
  because `deriveGrants` groups by rule, and there is no rule to group. This was already true of
  host-access semantics before this redesign (Approvals' old history table showed it as granted
  too); the new grants view simply makes the gap more visible by putting "what's granted" front
  and center. Not fixed here — fixing it would mean either synthesizing a rule-less pseudo-grant
  from host-access requests or giving host access its own row shape in `deriveGrants`, either of
  which is a separate, deliberate follow-up.
- Enum value-sets are enforced identically; only manual editing through the UI is gone. An
  operator who needs to change an enum's allowed set does so at the point the rule/preset is
  created, not after the fact through the rule-row editor.
- `POST /access-requests/{id}/revoke` remains reachable (API/tests) but is dead code from the web
  UI's perspective; a future cleanup pass may remove it once no caller needs single-request-scoped
  revoke.
- A future change that wants a *stored* grant (e.g., to record a grant-level note independent of
  any one request) would need to migrate off the derive-on-read model this ADR establishes.

Links: [spec](../../superpowers/specs/2026-07-07-access-page-redesign.md) (Access page redesign —
grant-first-class UI), [plan](../../superpowers/plans/2026-07-07-access-page-redesign.md).
