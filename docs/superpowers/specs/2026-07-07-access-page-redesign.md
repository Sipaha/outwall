# Access page redesign — grant-first-class UI

Status: approved design (brainstormed 2026-07-07). Supersedes the split
**Operations** + **Approvals** + **Agents** pages with a single **Access** page.

## Problem

The current UI smears three distinct concepts across three pages, and the boundaries
leak:

1. **Pending queue** — agent requests awaiting an operator decision (approve/deny).
2. **Request history** — an immutable log of who asked for what and how it was answered
   (`pending / granted / denied / revoked / dismissed`).
3. **Live grants** — the policy rules that say what an agent may do on an upstream *right now*
   (mutable: edit value-sets, delete a rule, revoke a whole grant).

Today:

- **Approvals** mixes the pending queue (1) with a "Access requests" history table (2) that
  carries a **live Revoke button** (3). That Revoke calls
  `DeleteBySubjectUpstream(agent, upstream)` — it deletes **every** rule for that
  (agent, upstream) pair and marks the request `revoked`. So a button sitting in what reads as
  "history" performs a coarse, live mutation on the policy — the exact source of the operator's
  confusion ("what does Revoke even do?").
- **Operations** shows the live rules (3) as raw rows, disconnected from which agent/request
  produced them.
- **Agents** is a separate list with agent metadata + delete.

The root cause: a **grant** — the set of rules for one (agent, upstream) — is not a
first-class object anywhere, yet Revoke acts on exactly that object.

## Goal

Make the **grant (agent × upstream)** the organizing concept, and give each of the three
concepts one unambiguous home:

- **Pending queue** → a "Запросы прав" panel at the top of the Access page (actionable, always
  visible, aggregated across all agents so requests are never buried).
- **Live grants** → the body of the Access page, grouped by agent (or upstream via a toggle),
  where Revoke and per-rule edit/delete live *next to the thing they change*.
- **Request history** → moves out to the **Audit** page as a new tab (read-only; dates,
  decisions, statuses).

Priority is operator convenience. Breaking compatibility is acceptable (alpha; no stored-format
guarantees).

## Information architecture

Left-nav **before**: Dashboard · Upstreams · Agents · Operations · Approvals · Audit · Settings.

Left-nav **after**: Dashboard · Upstreams · **Access** · Audit · Settings.

- **Agents**, **Operations**, **Approvals** nav items are **removed**.
- **Access** (`/access`) replaces them. Its agent cards absorb everything the old Agents page
  did (metadata, status, delete).
- **Audit** gains a tab: **Трафик** (the existing HTTP request/response audit) and **Запросы
  прав** (the access-request history that used to live on Approvals).
- Routes `/agents`, `/rules`, `/approvals` are dropped; `desktop.open-approvals` deep-link
  routing retargets to `/access` (scroll to / highlight the requests panel).

## The Access page

Single scrolling page, two sections.

### (a) Запросы прав — the pending queue

A section at the top, header `⧗ Запросы прав (N)`. Aggregates **all** pending approvals across
every agent so the operator never hunts through agent groups.

Each request is a card (amber left-accent, neutral background):

- **Header row**: `agent → upstream` + a type tag (`preset citeck-readonly`, `operation`,
  `host`, `k8s`, `new-value`) · request time (right, muted) · **Approve** / **Deny** buttons
  (right; subtle tinted `success/15` and `destructive/15`, matching the rest of the app — not
  filled/saturated).
- **The concrete right — the hero**: a bordered, bold box showing what the grant actually
  permits, led by a colored **scope badge** and the resource/path:
  - `READ` / `WRITE` (Records-profile) — blue / amber,
  - HTTP method `GET` / `POST` / … — method-colored,
  - k8s verb, browse scope, etc.
  - Variable placeholders render as `{name:type}` chips; concrete requested values render inline.
  - This is what the operator judges. It must never be muted: a benign purpose ("хочу почитать
    логи") must not hide a `WRITE prod` right.
- **Purpose — clearly readable** (foreground text, quote icon), directly under the right. The
  agent name is **not** repeated here (already in the header); the time is **not** here (it's in
  the header).

`Approve` opens the existing per-kind resolve flow (preset slot edits, operation trust-any,
host credential attach, k8s, new-value trust) — the current `Approvals.tsx` card logic is
reused, only relocated and restyled. `Deny` opens the reason modal.

Empty state: a muted "Нет запросов прав".

### (b) Выданные права — live grants

**Single-line header bar**: title `Выданные права` · toggle `По агенту / По upstream`
(persisted like the current Upstreams sub-tab, default **По агенту**) · `+ Выдать вручную`
(pushed right) — the manual rule-authoring flow from the old Operations "Add operation" modal.

Then a list of **collapsible group containers**.

**Grouped by agent (default):** each container is one agent.

- **Agent header** (one line): chevron · avatar (initial) · name · short id · status dot ·
  counter `N прав · M ресурсов` · meta `активн. <last_seen> · создан <created>` · a **trash
  icon** (delete agent) · a **⋮ kebab** menu.
  - The kebab holds the low-frequency **История запросов в Audit** action (link to the Audit
    "Запросы прав" tab filtered to this agent) and a duplicate **Удалить агента** (decision:
    keep the duplicate; the visible trash icon is the primary affordance).
  - **Collapse/expand toggles on a click anywhere in the header** (empty space included), not
    only the chevron. Clicks on the trash icon and the kebab do not toggle (stop propagation).
- **Agent body**: the agent's **upstream sub-cards** (its grants), stacked (no left rail).
  - **Upstream sub-card header** (one line): resource icon · hostname (mono) · `· <kind>`
    (`http` / `citeck` / `k8s`) · **Revoke** (ghost button, reddens on hover; revokes the whole
    grant = all rules for this agent×upstream).
  - **Upstream sub-card body**: the grant's **rule rows**. Each rule row:
    - scope badge (`GET` / `READ` / verb / …) · the path/resource (mono, bold, variables as
      chips) · a compact muted tail summarizing origin + non-`*` value-sets
      (`· project_path: infra/helm, infra/charts · page: 1–50` — actual values inline when they
      fit; the grant's purpose/date can be part of this dim tail) · `allow` · edit (pencil) /
      delete (trash) ghost icon-buttons.
    - The purpose of an already-granted right is **dim** (needed only for the approval decision,
      not for day-to-day scanning).
    - A rule with per-variable value-sets is **expandable** (its own chevron): expanding reveals
      the value-set editor — the current `Rules.tsx` editors, simplified to one clean line per
      variable:
      - **text**: allowed-value chips (each removable ×) + `+ значение` input + a `любое`
        (trust-any) toggle.
      - **number**: `min – max` range inputs + a `любое` toggle.
      - **date**: auto (any date) — shown as a note, no controls.
      - **enum**: **not shown** — its domain is fixed by the upstream/preset schema and the
        operator does not edit it. (If an enum value-set exists it is enforced but not surfaced
        as an editor here.)

**Grouped by upstream:** the same, transposed — each container is one upstream; its body lists
agent sub-cards, each with that agent's rules for the upstream and a Revoke (of that
agent×upstream grant). Agent-level controls (delete/kebab) stay agent-scoped and are not shown
in this mode's upstream headers.

Empty state: a muted "Прав ещё не выдано — действует запрет по умолчанию".

## Audit — new "Запросы прав" tab

Audit becomes tabbed:

- **Трафик** — the existing HTTP request/response audit table + detail modal (unchanged).
- **Запросы прав** — the immutable access-request history moved off Approvals. Columns: agent ·
  upstream · purpose · status badge (`granted / denied / revoked / dismissed`, with the deny
  reason) · requested-at · resolved-at. **Read-only** — no Revoke/Dismiss here (those were the
  live actions and now live on grants / are unnecessary). Supports the per-agent filter the
  Access kebab links to.

The old Approvals "Access requests" table and its live Revoke/Dismiss buttons are **removed**;
their state transitions are now driven from the Access grants (Revoke) and the pending queue
(approve/deny/dismiss).

## Data & backend

Largely a **frontend re-composition** over existing endpoints; no new persistence concepts.

- **Grants** are derived: group `listRules()` by `(subject_agent_id, upstream_id)`. A grant's
  purpose/created date come from the originating access request (`listAccessRequests()`,
  `status = granted`, matched on agent+upstream; when several requests map to one pair, show the
  most recent, and keep per-rule origin in the dim tail where available).
- **Pending queue** = `listApprovals()` (unchanged shape and per-kind resolve calls).
- **Revoke grant** = the existing `POST /access-requests/{id}/revoke` **or** a
  rules-by-(agent,upstream) delete. Because Revoke is now anchored to the grant (not a history
  row), prefer a direct **`DELETE /grants?agent=&upstream=`**-style call that removes the rules
  and marks any matching granted access-requests `revoked` — folding today's
  `hAccessRequestRevoke` logic so it is keyed by the grant, not by a specific request id. (Exact
  endpoint shape is an implementation-plan decision; the behavior — delete the (agent,upstream)
  rules + mark history `revoked` — is fixed.)
- **Delete agent** = existing `DELETE /agents/{id}` (cascades the agent's rules). Now invoked
  from the Access agent card; no confirmation modal (already removed for Agents).
- **Scope badges** (`READ` / `WRITE` / method / verb) are computed from the rule/approval shape
  (profile `op`, HTTP method, k8s verb, browse). No schema change.
- **Manual "Выдать вручную"** reuses the current `createRule` flow (the Operations add-modal),
  moved onto Access.
- All list endpoints already sort `created_at DESC`.

No change to the vault, the agent socket/CLI, the operator-session gate, or policy evaluation.
Gated mutations (revoke, delete agent, create/delete rule, set value policy, resolve approval)
keep their operator-session gate.

## What is explicitly out of scope / dropped

- The `Grants` / `Log` sub-tabs idea (an earlier iteration) — collapsed into the single Access
  page + the Audit history tab.
- Enum value-set editing UI.
- Any change to how requests are made by agents (CLI unchanged).
- The Operations-vs-Approvals conceptual confusion (resolved by this IA).

## Testing

- **vitest**: Access page — request panel renders pending approvals aggregated; approve/deny
  wire to the resolve calls; grant grouping by agent and by upstream; toggle persistence; rule
  value-set expand shows text-chips/number-range and hides enum; agent delete (no modal); revoke
  grant hits the grant-scoped endpoint; header click toggles collapse while control clicks do
  not. Audit — tab switch; "Запросы прав" is read-only and renders all statuses; per-agent
  filter.
- **go test**: the grant-scoped revoke handler (delete (agent,upstream) rules + mark granted
  requests `revoked`); scope-badge derivation if any lives server-side.
- **Playwright**: seed an agent + a pending request + a granted rule with a non-`*` value-set;
  verify the full Access flow (approve moves a request into a grant; revoke removes it; value
  chips edit) and the Audit history tab, against an isolated daemon.
- `make check` green throughout.

## Docs

- New ADR: "Access page — grant as first-class object; Operations/Approvals/Agents merged"
  (records the IA decision, the Revoke-relocation, and the Audit history move).
- Update `docs/architecture/modules/` web docs and `docs/INDEX.md`; delete stale references to
  the Operations/Approvals/Agents pages.
