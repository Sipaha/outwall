# module: web (+ internal/daemon/webui.go)

The embedded desktop UI: a Vite + React 19 + TypeScript + Tailwind 4 + Zustand + react-router
app under `web/`, built into `internal/daemon/webdist` and served by the daemon's `UIListen`
bind. See ADR-0006 for the rationale (embed, `/api` prefix, SSE CSRF exemption, reused theme).

## Build + embed

- `web/vite.config.ts` sets `build.outDir = ../internal/daemon/webdist` with `emptyOutDir: true`,
  so `pnpm build` writes the bundle straight into the Go embed location. Dev server proxies
  `/api` → `http://localhost:8182` (the daemon's `UIListen`).
- `internal/daemon/webui.go` embeds it: `//go:embed all:webdist`. `staticUI()` serves existing
  files directly and falls back to `index.html` for unknown paths (SPA client-side routing). If
  `fs.Sub` ever fails it returns a 500 handler rather than panicking (no-panic-in-library rule).
- A committed placeholder `webdist/index.html` lets `go build`/`go test` compile before any web
  build; `webdist/assets/` and `web/node_modules/`, `web/dist/` are gitignored.
- `make build` runs `build-web` (`pnpm -C web install && pnpm -C web build`) then the Go build;
  `make build-fast` skips the web rebuild.

## Frontend structure (`web/src`)

- `lib/api.ts` — typed control-API client. `API_BASE='/api'`, one `X-Outwall-CSRF` header on
  every call, `ApiError {status, message}` thrown on non-2xx (parsing the daemon's `{error}`
  body), `fetchWithTimeout`. One helper per endpoint (`getVaultStatus`, `vaultInit/Unlock`,
  `listAgents`/`deleteAgent`, `listUpstreams`/`createUpstream`/`deleteUpstream`/`setUpstreamAuth` (H3
  host credential set/replace), `listRules`/`createRule`/`deleteRule`/`setRuleVariablePolicy` (H3
  value-set add/remove/trust-any), `listApprovals`/`resolveApproval` (H3 takes an optional
  `{ auth?, trust_any? }`), `listAccessRequests`/`resolveAccessRequest`/`revokeAccessRequest`
  (per-request revoke — kept for API/test coverage but no longer called from the UI, see ADR-0042),
  `revokeGrant(agentId, upstreamId)` (grant-scoped revoke → `POST /grants/revoke`, the Access page's
  only revoke path, ADR-0042), `listAudit`/`getAudit`/`pruneAudit`, `vaultLock`, and the K4 cluster
  helpers `createCluster`/`importClusters`/`getKubeconfig`).
- `lib/types.ts` — TS interfaces mirroring the Go admin JSON field names (Agent, Upstream — with
  the K4 `k8s_auth`/`k8s_insecure` cluster fields, ClusterAuthConfig, ClusterImportResult, Rule +
  ValuePolicy, Approval — H3 adds the `kind`/`host`/`op_*`/`new_values`/`template` control-plane
  fields plus `OpVariable`/`NewValue`/`ResolveOptions`, AccessRequest,
  AuditEntry/AuditBody/AuditDetail, VaultStatus, OutwallEvent).
- `lib/grants.ts` — `Grant{agentId, upstreamId, rules[], purpose, grantedAt}`; `deriveGrants(rules,
  requests)` groups rules by `(subject_agent_id, upstream_id)` and back-fills purpose/date from the
  most recent `granted` access request for that pair (a grant is a derived view, no new storage,
  ADR-0042); `scopeOf(rule)` → `{label, kind}` colored scope badge (`READ`/`WRITE` for the citeck
  profile, `BROWSE`, k8s verb, else the HTTP method); `valueSummary(rule)` → the compact non-`*`
  value-constraint tail (`"var: a, b · n: 1–50"`).
- `lib/accessGrouping.ts` — Zustand store for the Access page's grants grouping toggle (`by:
  'agent' | 'upstream'`, default `agent`).
- `lib/events.ts` — Zustand store wrapping one `EventSource('/api/events')`. Tracks `connected`
  and a per-event-type counter (`counters['approval.enqueued']`, …) bumped on each event, so a
  screen `useEffect`-refetches on the relevant event instead of polling. `connect()`/`disconnect()`.
- `lib/toast.ts` — Zustand transient-notification store (auto-dismiss after 5s).
- `index.css` — `@import "tailwindcss"` + the Darcula/Lens `@theme` token block (dark-only in
  6A): `--color-background:#1e1f22`, the `--color-status-*` palette, JetBrains-Mono `--font-mono`.
  Sets `color-scheme: dark` on `:root`/`body` (+ an `option` background) so WebKitGTK renders
  native form controls — the `<select>` and its option popup — dark instead of light (ADR-0011).
- `components/` — `Sidebar` (wordmark + nav + live SSE connection dot; nav is **Dashboard ·
  Upstreams · Access · Audit · Settings**, ADR-0042 — the earlier `Agents`/`Operations`
  (`/rules`)/`Approvals` items are gone), `StatusBadge` (status → color pill), `Modal`, `Toast`
  (`ToastContainer`), `DataTable` (compact dense table), `FormField` (labelled control wrapper +
  shared themed `fieldControlClass`), `Select` (themed native `<select>`), `Tabs` (controlled,
  URL-query-backed tab bar; used by the Upstreams zone tabs and the Audit Трафик/Запросы-прав
  tabs), `JsonView` (pretty-prints captured JSON bodies; metadata for binary/absent).
- `pages/` — the full screen set:
  - `Unlock` — init/unlock master-password card (`vaultInit`/`vaultUnlock`).
  - `Dashboard` — Agents table + live Approval queue (refetched on `agent.registered`,
    `approval.enqueued`, `approval.resolved`; Approve/Deny → `resolveApproval`) — a quick-glance
    summary, distinct from the Access page's full pending-queue-plus-grants view.
  - `Upstreams` — `HTTP` / `Citeck` / `Kubernetes` zone tabs (ADR-0036) over `listUpstreams`;
    per-zone add/credential/remove/kubeconfig-import flows.
  - `Access` (`/access`) — a single scrolling page composed from `pages/access/`:
    - `RequestsPanel` — the aggregated pending-approval queue ("Запросы прав"), rendering
      `ApprovalCards.tsx`'s per-`kind` cards (host/operation/preset/new-value/plain-k8s) with a
      scope-badge "hero" box for the concrete right and a Deny-with-reason modal.
    - `GrantGroups` — the granted-rights list ("Выданные права"), grouping `lib/grants.ts`'s
      `deriveGrants(rules, requests)` output either by agent (`AgentCard` → `UpstreamGrantCard`
      per grant, default) or by upstream (transposed), toggle persisted via
      `lib/accessGrouping.ts`.
    - `AgentCard` — one agent's collapsible group: header (chevron, avatar, name, short id,
      status dot, `N прав · M ресурсов`, last-active meta, a **trash icon** → `deleteAgent`, a
      **⋮ kebab** with "История запросов в Audit" → `/audit?tab=requests&agent=<id>` and a
      duplicate delete). Header click anywhere toggles collapse; the trash/kebab
      `stopPropagation`.
    - `UpstreamGrantCard` — one grant (agent×upstream): header (kind icon, hostname, kind label,
      **Revoke** → `revokeGrant(agentId, upstreamId)`) followed by its `RuleRow`s.
    - `RuleRow` — one rule: scope badge (`scopeOf`), path/resource (variable segments as chips),
      `valueSummary` tail, outcome, edit/delete icons. Expandable per-variable editors from
      `valueEditors.tsx` — `ValueSetEditor` (text: chips + add + trust-any) and
      `NumberRangeEditor` (min–max + trust-any) only; **enum has no editor** (enforced, but not
      user-editable here); `date` shows a static "auto" note.
    - `ManualRuleModal` — the `+ Выдать вручную` entry point: a `createRule` flow (http
      method+path+var lines, k8s tuple, or citeck Records op/sourceId/workspace).
  - `Audit` — tabbed (`Tabs`): **Трафик** — `listAudit(200)` table (status colored by class)
    refetched on `audit.recorded`, row "View" → `getAudit(id)` detail modal (meta grid,
    masked-headers table, request/response body panels via `JsonView`); **Запросы прав** —
    the read-only access-request history (agent, upstream, purpose, status badge + deny reason,
    requested-at, resolved-at), no Revoke/Dismiss here, filterable by `?agent=<id>` (the Access
    agent-card kebab link) and tab-selected via `?tab=requests`.
  - `Settings` — vault status + Lock vault (`vaultLock` then reload → Unlock screen); audit
    prune control (`pruneAudit` older-than-N-days); a localhost-only daemon note.
- `App.tsx` — on mount `getVaultStatus()`: not-initialized → Unlock(init); locked →
  Unlock(unlock); else the shell (Sidebar + routed `<main>`), connecting the SSE store. Routes:
  `/`, `/upstreams`, `/access`, `/audit`, `/settings`. `useOpenApprovalsRoute` routes to
  `/access` on a `desktop.open-approvals` signal.

## Tests

- `lib/api.test.ts` — `vaultUnlock` posts to `/api/vault/unlock` with the CSRF header; throws
  `ApiError` (status + daemon message) on a 401; GET helpers send the header and parse arrays.
- `pages/Unlock.test.tsx` — typing + submit calls `vaultUnlock`/`vaultInit`; bad password shows
  the daemon error and skips `onDone`; init mode requires a matching confirmation.
- `pages/Upstreams.test.tsx` + `pages/Upstreams.tab-persistence.test.tsx` — rows render credential
  status (set/none); per-host Set credential → `setUpstreamAuth(name, auth)`; Remove → confirm →
  `deleteUpstream(name)`; the add-host modal switches conditional auth fields and submits
  `createUpstream`; the HTTP/Citeck/Kubernetes zone tab persists across navigation.
- `pages/Clusters.test.tsx` — kind=k8s rows render (and the insecure cluster shows the "insecure"
  badge) while an http upstream is filtered out; selecting a file on the hidden import input reads
  it and calls `importKubeconfigContent`, toasting added/skipped; a backend `added:null` still
  toasts success (null-guard), never "Failed to import"; the add form reveals exec fields when
  auth=exec.
- `pages/Access.test.tsx` — renders the "Запросы прав" panel and, from `listRules`+`listAgents`+
  `listUpstreams`, the "Выданные права" agent group (grant's upstream visible under its agent);
  clicking "По upstream" switches the grouping and still shows the grant.
- `pages/access/RequestsPanel.test.tsx` — shows the "Запросы прав (N)" count and renders a card
  (a host-kind fixture) with its purpose/host; shows "Нет запросов прав" when empty; Approve calls
  `resolveApproval` then `onChanged`. `ApprovalCards.tsx`'s other per-`kind` shapes (operation
  example-URL, broad-placeholder warning, new-value trust-any, plain/k8s) and the Deny
  reason-modal path have no dedicated test.
- No standalone test for `GrantGroups`; its by-agent/by-upstream rendering is exercised indirectly
  through `Access.test.tsx` plus `AgentCard.test.tsx`/`UpstreamGrantCard.test.tsx`.
- `pages/access/AgentCard.test.tsx` — header click toggles collapse (expanded by default); the
  trash icon calls `deleteAgent(id)` immediately without a confirmation modal, and does not also
  toggle the header (still expanded after).
- `pages/access/UpstreamGrantCard.test.tsx` — renders the hostname + a rule; Revoke calls
  `revokeGrant(agentId, upstreamId)` and then `onChanged`.
- `pages/access/RuleRow.test.tsx` — renders the scope badge, path, and value-summary tail;
  expanding a text-policy rule shows its `ValueSetEditor` chip + "Trust any value" control;
  delete calls `deleteRule(id)` and then `onChanged`.
- `pages/access/scope.test.tsx` — `ScopeBadge` renders the right class per `kind`.
- `pages/access/ManualRuleModal.test.tsx` — filling the path-template and submitting creates an
  http operation rule via `createRule`, then calls `onCreated`.
- `pages/Audit.test.tsx` — the Трафик tab loads the journal; row "View" calls `getAudit(id)` and
  renders the masked header + pretty-printed JSON body; `?tab=requests` selects the Запросы-прав
  tab, which renders `listAccessRequests()` read-only (no Revoke button) including a `revoked`
  row. (The `?agent=` filter the Access agent-card kebab links to is implemented but not asserted
  by a test yet.)
- `test/setup.ts` polyfills `HTMLDialogElement.showModal/close` (jsdom lacks them) for the
  modal-driven page tests.

Run with `pnpm -C web test` / `lint` / `build`.
