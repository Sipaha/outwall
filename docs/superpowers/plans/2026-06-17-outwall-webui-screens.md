# outwall — Plan 6B: Remaining Web UI Screens

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development or superpowers:executing-plans.

**Goal:** Replace the six `ComingSoon` route placeholders from 6A with real screens, in the same
dark-console style: **Upstreams** (CRUD + auth method + secrets), **Agents** (+ lightweight
detail), **Rules** (editor), **Approvals** (pending approvals + access-request intents), **Audit**
(journal + body viewer), **Settings** (audit prune + vault lock). All wire to the existing
`web/src/lib/api.ts` helpers, `types.ts` DTOs, the `useEventStore` counter refresh pattern, and
the `DataTable`/`Modal`/`StatusBadge`/`Toast` primitives from 6A.

**Architecture:** Pure frontend, plus ONE tiny backend add: `POST /vault/lock` (admin) so Settings
can lock the vault. Each page mirrors `web/src/pages/Dashboard.tsx`: fetch on mount + on the
relevant SSE counter, render a `DataTable`, mutate via a `Modal` form, toast on success/error.

**Tech Stack:** as 6A (React 19 / Vite / Tailwind 4 / Zustand / react-router / lucide-react).

## Global Constraints

(All 6A constraints apply.) Plus:
- **Mirror `Dashboard.tsx` exactly** for data-loading idiom: a `load()` callback, `useEffect(load,
  [load])`, and `useEffect(load, [counter])` where `counter = useEventStore(s =>
  s.counters['<event>'])`. Put `setState` in `.then()/.catch()` (the `react-hooks` v7
  `set-state-in-effect` rule — 6A already follows this).
- Reuse the existing `api.ts` helpers verbatim (signatures in `web/src/lib/api.ts`). Do not add
  new daemon endpoints except `POST /vault/lock`.
- No new npm deps unless truly needed; if added, `@latest`.
- No citeck anywhere.

## File Structure

```
Create:  web/src/pages/{Upstreams,Agents,Rules,Approvals,Audit,Settings}.tsx
         web/src/components/{Select,FormField,JsonView}.tsx  (small shared primitives)
         web/src/pages/Upstreams.test.tsx  (+ at least one more page test)
Modify:  web/src/App.tsx                 (route the 6 pages; drop ComingSoon)
         web/src/lib/api.ts              (+ vaultLock())
         internal/daemon/admin.go         (+ POST /vault/lock → vault.Lock + publish vault.locked)
         internal/daemon/admin_test.go    (+ vault lock test)
```

---

### Task 1: vault lock endpoint + Upstreams screen

**Backend (small):** in `admin.go` add `mux.HandleFunc("POST /vault/lock", d.hVaultLock)` to
`apiMux()`; handler calls `d.vault.Lock()`, publishes `vault.locked` (nil-safe `d.publish`),
returns `{"locked":true}`. Add a test: init+unlock, POST /vault/lock → 200, then GET
/vault/status shows `locked:true`. Add `vaultLock()` to `api.ts` (`POST /vault/lock`).

**Upstreams.tsx (/upstreams):**
- `load()` = `listUpstreams()` → rows; refresh on `useEventStore(s => s.counters['upstream.created'])`.
- `DataTable` columns: Name, Base URL (mono), Auth (`auth_type` via `StatusBadge` or plain).
- "Add upstream" button → `Modal` with `onSubmit` calling `createUpstream(name, base_url, auth)`,
  then `load()` + success toast; on `ApiError` → error toast.
- The auth form is the one non-trivial piece — a `<Select>` for the type with conditional fields:
```tsx
// inside the Modal body
const [auth, setAuth] = useState<UpstreamAuthConfig>({ type: 'none' })
// ...
<FormField label="Auth type">
  <Select value={auth.type} onChange={(t) => setAuth({ type: t })}
    options={[
      { value: 'none', label: 'None' },
      { value: 'static', label: 'Static header / API key' },
      { value: 'basic', label: 'Basic' },
      { value: 'oidc-client-credentials', label: 'OIDC client-credentials' },
    ]} />
</FormField>
{auth.type === 'static' && (<>
  <FormField label="Header"><input value={auth.header ?? ''} onChange={e => setAuth({ ...auth, header: e.target.value })} placeholder="Authorization" /></FormField>
  <FormField label="Value"><input value={auth.token ?? ''} onChange={e => setAuth({ ...auth, token: e.target.value })} placeholder="Bearer …" /></FormField>
</>)}
{auth.type === 'basic' && (<>
  <FormField label="Username"><input value={auth.username ?? ''} onChange={e => setAuth({ ...auth, username: e.target.value })} /></FormField>
  <FormField label="Password"><input type="password" value={auth.password ?? ''} onChange={e => setAuth({ ...auth, password: e.target.value })} /></FormField>
</>)}
{auth.type === 'oidc-client-credentials' && (<>
  <FormField label="Token URL"><input value={auth.token_url ?? ''} onChange={e => setAuth({ ...auth, token_url: e.target.value })} /></FormField>
  <FormField label="Client ID"><input value={auth.client_id ?? ''} onChange={e => setAuth({ ...auth, client_id: e.target.value })} /></FormField>
  <FormField label="Client secret"><input type="password" value={auth.client_secret ?? ''} onChange={e => setAuth({ ...auth, client_secret: e.target.value })} /></FormField>
  <FormField label="Scope"><input value={auth.scope ?? ''} onChange={e => setAuth({ ...auth, scope: e.target.value })} /></FormField>
</>)}
```
- `Select.tsx`, `FormField.tsx` — small dark-styled primitives (label + control wrapper; native
  `<select>` styled to the theme). `input`/`select` get a shared themed className (border
  `--color-border`, bg `--color-muted`, focus ring `--color-primary`).

- [ ] Steps: backend test→impl; `Select`/`FormField`/`Upstreams` + a `Upstreams.test.tsx`
  (render with mocked `listUpstreams`, open the modal, switch auth type → asserts conditional
  fields appear, submit → `createUpstream` called). Run `pnpm -C web test` + `go test
  ./internal/daemon/`. Commit `feat(web): Upstreams screen + vault lock endpoint`.

---

### Task 2: Agents + Rules screens

**Agents.tsx (/agents):** `listAgents()` → `DataTable` (Name, Status badge, ID mono). Refresh on
`agent.registered`. Row click → a `Modal` "Agent detail" showing the agent's rules (filter
`listRules()` by `subject_agent_id === agent.id`) and its access requests (filter
`listAccessRequests()` by `agent_id`). Read-only is fine for 6B.

**Rules.tsx (/rules):** `listRules()` joined with `listUpstreams()` + `listAgents()` to render
names. `DataTable` columns: Subject (agent name or "any"), Upstream (name), Method (`*`→"any"),
Path glob (mono), Outcome (`StatusBadge`: allow→success, deny→destructive, require-approval→
warning), Rate (`/min` or "∞" when 0), and a Delete action (`deleteRule(id)` → confirm via Modal
→ refresh). "Add rule" → `Modal`: Subject `<Select>` (Any + each agent), Upstream `<Select>`
(each upstream, required), Method input (default `*`), Path glob input (default `/**`), Outcome
`<Select>` (allow/deny/require-approval), Rate number input. `createRule({subject_agent_id,
upstream_id, method, path_glob, outcome, rate_limit_per_min})` → refresh. Refresh list on
`rule.created`.

- [ ] Steps: build both pages; one vitest (Rules: mock the three lists, assert rows render with
  resolved names; open add-modal, submit → `createRule` called). `pnpm -C web test/lint/build`.
  Commit `feat(web): Agents + Rules screens`.

---

### Task 3: Approvals screen

**Approvals.tsx (/approvals):** two sections.
- **Pending approvals** (live): `listApprovals()` → table (Agent short-id, Upstream short-id,
  Method, Path mono, Purpose, When) + **Approve**(`resolveApproval(id,true)`)/**Deny**
  (`resolveApproval(id,false)`) buttons. Refresh on `approval.enqueued` + `approval.resolved`.
  Empty state "No pending approvals".
- **Access requests** (intents): `listAccessRequests()` → table (Agent name, Upstream name,
  Purpose, Status badge, When) + for `pending` rows: **Grant**(`resolveAccessRequest(id,
  'granted')`), **Deny**(`'denied'`), **Dismiss**(`'dismissed'`). Note inline: "granting marks
  the request handled — actual access is via Rules". Refresh on `access.requested`.

- [ ] Steps: build the page; vitest (mock both lists; click Approve → `resolveApproval(id,true)`
  called). `pnpm -C web test/lint/build`. Commit `feat(web): Approvals + access-requests screen`.

---

### Task 4: Audit screen + Settings

**Audit.tsx (/audit):** `listAudit(200)` → `DataTable`: When (`ts`), Agent (name), Upstream
(name), Method, Path (mono, truncate), Status (`status_code` colored — 2xx success, 4xx warning,
5xx destructive), Dur (`duration_ms`+"ms"), Decision (`StatusBadge`). Refresh on `audit.recorded`.
Row click → `getAudit(id)` → a detail `Modal`/drawer:
- Meta grid (agent, upstream, method, full path+query, status, duration, sizes, decision, rule_id,
  error if any).
- **Masked headers** table (`headers` map).
- **Request** and **Response** body panels via a `JsonView` component:
```tsx
// JsonView.tsx — pretty-print JSON bodies, fall back to raw text; show binary metadata.
export function JsonView({ body }: { body: AuditBody }) {
  if (body.body == null) {
    return <div className="text-muted-foreground text-xs font-mono">
      [{body.content_type || 'binary'}] {body.size} bytes · sha256 {body.sha256.slice(0, 12)}…
      {body.truncated && ' · truncated'}
    </div>
  }
  let text = body.body
  if ((body.content_type || '').includes('json')) {
    try { text = JSON.stringify(JSON.parse(body.body), null, 2) } catch { /* keep raw */ }
  }
  return <pre className="text-xs font-mono overflow-auto max-h-80 bg-background rounded p-2 border border-border">{text}{body.truncated && '\n… [truncated]'}</pre>
}
```

**Settings.tsx (/settings):**
- **Vault**: show initialized/locked (`getVaultStatus`); a **Lock vault** button (`vaultLock()` →
  on success the app returns to the Unlock screen — App re-checks status; simplest: call
  `vaultLock()` then `window.location.reload()`).
- **Audit retention**: a prune control — a number input "older than N days" → `pruneAudit(new
  Date(Date.now() - days*864e5).toISOString())` → toast `{deleted}`.
- **Daemon**: static info (the three listen addresses are not exposed via API; show a short
  "localhost-only" note + the spec's defaults, or omit). Keep minimal.

- [ ] Steps: `JsonView` + both pages; vitest (Audit: mock `listAudit`, row click → `getAudit`
  called + body rendered). Wire all 6 routes in `App.tsx` (remove `ComingSoon`). Full gate:
```bash
grep -rin citeck web/ --include='*.ts' --include='*.tsx'   # empty
pnpm -C web lint && pnpm -C web test && pnpm -C web build
gofmt -l . && go vet ./... && go test ./... && make build
git add -A && git commit -m "feat(web): Audit + Settings screens; route all pages"
```

---

## Verification (supervisor)

Live browser smoke (Playwright): build, run the daemon with seeded data (upstream, rules,
agents, a pending approval, a completed proxied request for an audit row), and screenshot
Upstreams, Rules, Approvals, and Audit (with a body-viewer open). Assess against the dark-console
look. Iterate if off.

## Self-Review

- **Spec coverage:** Upstreams CRUD + auth/secrets ✓; Rules editor ✓; Approvals (with purpose,
  allow/deny) + access requests ✓; Audit journal + body viewer + masked headers ✓; Agent detail
  (lightweight) ✓; Settings (prune + vault lock) ✓.
- **Deferred:** Wails wrapper (Plan 7); light theme toggle; per-upstream secret *editing* (create
  only for now); OIDC authorization-code config UI (Phase 2 feature).
- **Type consistency:** all pages consume the existing `api.ts`/`types.ts` exactly; the only new
  daemon route is `POST /vault/lock` (+ `vaultLock()` client + `vault.locked` event, already in
  the taxonomy).

## ADR + docs (finalize)

No new ADR required (these screens are within ADR-0006). Update `docs/architecture/modules/webui.md`
to list the full screen set. Don't touch INDEX/current-phase.
