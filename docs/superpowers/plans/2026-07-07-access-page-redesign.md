# Access Page Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Operations + Approvals + Agents pages with a single **Access** page organized around the grant (agent × upstream), and move access-request history into Audit.

**Architecture:** Almost entirely a frontend re-composition over existing endpoints. New backend surface is one gated handler (`POST /grants/revoke`) + one registry method. The frontend adds a `lib/grants.ts` deriver (group rules into grants, compute scope badges + value summaries), an `Access` page (requests panel + grouped grants), an Audit tab, and drops three pages/routes/nav items.

**Tech Stack:** Go (`net/http`, `modernc.org/sqlite`, `log/slog`, `stretchr/testify`) + React 18 / TypeScript / Tailwind / `react-router` / `zustand` / lucide-react / vitest + Playwright.

## Global Constraints

- Module path is exactly `github.com/Sipaha/outwall` in every Go import.
- No `citeck` strings/imports in core; the only allowed profile-name value is `"citeck"` (server-profile plugin). Access page reads `rule.profile === 'citeck'` — that string is permitted (it is the persisted profile-name, ADR-0034), used only to pick the READ/WRITE label.
- No CGO in the server binary (`CGO_ENABLED=0`). No panics/`log.Fatal` in library code — return wrapped `error` (`%w`).
- No `dangerouslySetInnerHTML` (eslint-banned).
- Add new deps at `@latest`; do not bump existing deps.
- Before every commit: `make fmt && make vet && make test`; before merge: `make check` (full gate) must be green.
- Commit messages: NO `Co-Authored-By`; never `git commit --amend`.
- Git workflow: branch off `main`, commit per task, `git merge --ff-only` back, `git branch -d`. Never push. Before committing, drop build churn: `git checkout -- internal/daemon/webdist/index.html`.
- Kill test daemons via `ss -ltnp | grep <port>` → `kill <pid>`; never `pkill -f`.

**Reference:** the approved spec `docs/superpowers/specs/2026-07-07-access-page-redesign.md` and the approved mockups in `.superpowers/brainstorm/*/content/access-layout-v11.html`, `access-values-v2.html`, `access-agent-controls.html`.

**Existing app conventions to mirror:** Tailwind tokens `bg-card`, `bg-muted`, `border-border`, `text-muted-foreground`, `text-foreground`, `bg-primary`, `bg-destructive/15 text-destructive`, `bg-success/15 text-success`; components `DataTable`, `Modal`, `StatusBadge`, `Select`, `FormField` (`web/src/components/`); toasts via `useToastStore().push`; SSE refresh via `useEventStore().counters[...]`; lucide-react icons.

---

## File structure

**Backend**
- `internal/access/registry.go` — add `MarkRevokedBySubjectUpstream`.
- `internal/access/registry_test.go` — test it.
- `internal/daemon/admin.go` — add gated route `POST /grants/revoke` + `hGrantRevoke`.
- `internal/daemon/admin_test.go` (or existing daemon test file) — test the handler.

**Frontend — new**
- `web/src/lib/grants.ts` — grant deriver + scope/value helpers (pure, tested).
- `web/src/lib/grants.test.ts`
- `web/src/lib/accessGrouping.ts` — zustand store for the agent/upstream toggle (mirrors `upstreamsTab.ts`).
- `web/src/pages/Access.tsx` — the page (composes the pieces below).
- `web/src/pages/access/RequestsPanel.tsx` — pending queue (wraps existing approval cards).
- `web/src/pages/access/GrantGroups.tsx` — grouped grants (agent/upstream containers).
- `web/src/pages/access/AgentCard.tsx` — collapsible agent container + kebab + delete.
- `web/src/pages/access/UpstreamGrantCard.tsx` — one grant (upstream sub-card) + rules + revoke.
- `web/src/pages/access/RuleRow.tsx` — one rule row + expandable value-set editor.
- `web/src/pages/access/scope.tsx` — `ScopeBadge` component (READ/WRITE/method/verb colors).
- `web/src/pages/Access.test.tsx`, `web/src/pages/access/*.test.tsx` — component tests.

**Frontend — modified**
- `web/src/lib/api.ts` — add `revokeGrant(agentId, upstreamId)`.
- `web/src/components/Sidebar.tsx` — nav: drop Agents/Operations/Approvals, add Access.
- `web/src/App.tsx` — routes: drop `/agents`,`/rules`,`/approvals`; add `/access`; retarget open-approvals.
- `web/src/lib/useOpenApprovalsRoute.ts` — route to `/access` instead of `/approvals`.
- `web/src/pages/Audit.tsx` — add tabs (Трафик / Запросы прав); the second reuses the moved access-request history.
- Existing `Approvals.tsx` card components are **extracted** into `web/src/pages/access/ApprovalCards.tsx` (moved verbatim, exported) so RequestsPanel reuses them.

**Frontend — deleted (after their logic is relocated)**
- `web/src/pages/Agents.tsx` + `Agents.test.tsx`
- `web/src/pages/Rules.tsx` + `Rules.test.tsx` → the add-operation modal moves into an Access "Выдать вручную" modal component `web/src/pages/access/ManualRuleModal.tsx`; the per-variable editors move into `RuleRow`.
- `web/src/pages/Approvals.tsx` + `Approvals.test.tsx` + `Upstreams.tab-persistence.test.tsx` stays (unrelated).

**Docs**
- `docs/architecture/decisions/ADR-00XX-access-page-grant-first-class.md`
- `docs/architecture/modules/web-ui.md` (or the relevant module doc) + `docs/INDEX.md`.

---

## Task 1: Backend — revoke a grant by (agent, upstream)

**Files:**
- Modify: `internal/access/registry.go`
- Test: `internal/access/registry_test.go`

**Interfaces:**
- Consumes: existing `access.Registry` (has `store.DB()`, `StatusRevoked`, `MarkRevoked`).
- Produces: `func (r *Registry) MarkRevokedBySubjectUpstream(agentID, upstreamID string) (int64, error)` — marks every `granted` request for that pair `revoked`, stamps `resolved_at`, returns count.

- [ ] **Step 1: Write the failing test**

```go
// internal/access/registry_test.go — add to the existing test file (reuse its newTestRegistry helper).
func TestMarkRevokedBySubjectUpstream(t *testing.T) {
	r := newTestRegistry(t) // existing helper; if absent, mirror the setup in the other tests
	if _, err := r.Create("ag1", "up1", "p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GrantLatest("ag1", "up1"); err != nil {
		t.Fatal(err)
	}
	// A second, still-pending request for the same pair must NOT be revoked.
	if _, err := r.Create("ag1", "up1", "p2"); err != nil {
		t.Fatal(err)
	}

	n, err := r.MarkRevokedBySubjectUpstream("ag1", "up1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("revoked count = %d, want 1", n)
	}
	all, _ := r.List()
	var revoked, pending int
	for _, req := range all {
		switch req.Status {
		case StatusRevoked:
			revoked++
		case StatusPending:
			pending++
		}
	}
	if revoked != 1 || pending != 1 {
		t.Fatalf("revoked=%d pending=%d, want 1/1", revoked, pending)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/access/ -run TestMarkRevokedBySubjectUpstream -v`
Expected: FAIL — `r.MarkRevokedBySubjectUpstream undefined`.

- [ ] **Step 3: Implement the method**

```go
// internal/access/registry.go — add after MarkRevoked.

// MarkRevokedBySubjectUpstream marks every currently-granted request for (agentID, upstreamID)
// "revoked" and stamps resolved_at. Used by the operator's grant revoke (which also removes the
// underlying policy rules). Returns the number of requests marked. Pending/denied rows are left
// untouched.
func (r *Registry) MarkRevokedBySubjectUpstream(agentID, upstreamID string) (int64, error) {
	res, err := r.store.DB().Exec(
		`UPDATE access_requests SET status=?, resolved_at=? WHERE agent_id=? AND upstream_id=? AND status=?`,
		StatusRevoked, time.Now().UTC().Format(time.RFC3339Nano), agentID, upstreamID, StatusGranted,
	)
	if err != nil {
		return 0, fmt.Errorf("mark revoked by subject+upstream: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}
```

Verify the constant name for the granted status is `StatusGranted` (grep `internal/access/registry.go`); if it differs, use the actual name. Confirm the table name is `access_requests` (grep the schema/migration).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/access/ -run TestMarkRevokedBySubjectUpstream -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
make fmt && make vet && go test ./internal/access/
git add internal/access/registry.go internal/access/registry_test.go
git commit -m "feat(access): MarkRevokedBySubjectUpstream for grant-scoped revoke"
```

---

## Task 2: Backend — `POST /grants/revoke` handler + api client

**Files:**
- Modify: `internal/daemon/admin.go`
- Test: `internal/daemon/admin_test.go` (append; if no such file, create it mirroring an existing daemon handler test)
- Modify: `web/src/lib/api.ts`

**Interfaces:**
- Consumes: `d.policy.DeleteBySubjectUpstream(agentID, upstreamID) (int64, error)`, `d.access.MarkRevokedBySubjectUpstream(...)` (Task 1), `d.publish`.
- Produces: gated route `POST /grants/revoke` with JSON body `{"agent_id": string, "upstream_id": string}` → `{"ok": true, "rules_removed": N}`; frontend `revokeGrant(agentId, upstreamId): Promise<{ ok: boolean; rules_removed: number }>`.

- [ ] **Step 1: Write the failing Go test**

```go
// internal/daemon/admin_test.go
func TestGrantRevoke(t *testing.T) {
	d := newTestDaemon(t) // existing helper used by other daemon tests
	// Seed: an agent, an upstream, a rule for (agent, upstream), a granted access-request.
	// Reuse whatever seeding helpers the daemon test suite already provides; if none, create
	// rows through d.policy / d.access / d.agents directly.
	agentID, upstreamID := seedGrant(t, d) // returns the ids of a rule-backed grant

	body := strings.NewReader(`{"agent_id":"` + agentID + `","upstream_id":"` + upstreamID + `"}`)
	req := httptest.NewRequest("POST", "/grants/revoke", body)
	rec := httptest.NewRecorder()
	withOperatorSession(t, d, req) // helper the gated-handler tests already use
	d.AdminHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	rules, _ := d.policy.List()
	for _, r := range rules {
		if r.SubjectAgentID == agentID && r.UpstreamID == upstreamID {
			t.Fatal("rule for the revoked grant still present")
		}
	}
}
```

If the daemon test suite lacks `newTestDaemon`/`withOperatorSession`/seed helpers, first grep `internal/daemon/*_test.go` for the real names and reuse them; adapt the test to match. Do not invent helpers that don't exist — read the neighboring gated-handler test (`hAccessRequestRevoke`) and copy its harness.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/daemon/ -run TestGrantRevoke -v`
Expected: FAIL — 404 (route missing) or compile error.

- [ ] **Step 3: Register the gated route + implement the handler**

```go
// internal/daemon/admin.go — in the gated-route section (near gate("POST /access-requests/{id}/revoke", ...))
gate("POST /grants/revoke", d.hGrantRevoke)
```

```go
// internal/daemon/admin.go — new handler near hAccessRequestRevoke.

// hGrantRevoke withdraws a whole grant: it removes every policy rule for (agent, upstream) and
// marks matching granted access-requests "revoked". This is the grant-scoped successor to the
// per-request revoke — the Access UI anchors Revoke to the grant, not a history row. Gated.
func (d *Daemon) hGrantRevoke(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AgentID    string `json:"agent_id"`
		UpstreamID string `json:"upstream_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		adminErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.UpstreamID == "" {
		adminErr(w, http.StatusBadRequest, "upstream_id required")
		return
	}
	removed, err := d.policy.DeleteBySubjectUpstream(body.AgentID, body.UpstreamID)
	if err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := d.access.MarkRevokedBySubjectUpstream(body.AgentID, body.UpstreamID); err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.publish("access.revoked", map[string]any{"agent_id": body.AgentID, "upstream_id": body.UpstreamID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "rules_removed": removed})
}
```

Confirm `json` is imported in `admin.go` (it uses `writeJSON` already; add `"encoding/json"` if not present). Confirm policy `Rule` struct field names (`SubjectAgentID`, `UpstreamID`) via grep before using them in the test.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/daemon/ -run TestGrantRevoke -v`
Expected: PASS.

- [ ] **Step 5: Add the api client method**

```ts
// web/src/lib/api.ts — near revokeAccessRequest.
export function revokeGrant(
  agentId: string,
  upstreamId: string,
): Promise<{ ok: boolean; rules_removed: number }> {
  return req('/grants/revoke', {
    method: 'POST',
    body: JSON.stringify({ agent_id: agentId, upstream_id: upstreamId }),
  })
}
```

Match the existing helper name/shape in `api.ts` (the file uses an internal `req`/`apiFetch` helper — copy the exact pattern from `revokeAccessRequest` right above).

- [ ] **Step 6: Commit**

```bash
make fmt && make vet && go test ./internal/daemon/ ./internal/access/
git checkout -- internal/daemon/webdist/index.html
git add internal/daemon/admin.go internal/daemon/admin_test.go web/src/lib/api.ts
git commit -m "feat(daemon): POST /grants/revoke — revoke a grant by (agent,upstream)"
```

---

## Task 3: Frontend — `lib/grants.ts` deriver + scope/value helpers

**Files:**
- Create: `web/src/lib/grants.ts`
- Test: `web/src/lib/grants.test.ts`

**Interfaces:**
- Consumes: `Rule`, `AccessRequest`, `Upstream` from `./types`.
- Produces:
  - `interface Grant { agentId: string; upstreamId: string; rules: Rule[]; purpose: string; grantedAt: string }`
  - `deriveGrants(rules: Rule[], requests: AccessRequest[]): Grant[]`
  - `scopeOf(rule: Rule): { label: string; kind: 'method' | 'read' | 'write' | 'verb' | 'browse' }`
  - `valueSummary(rule: Rule): string`

- [ ] **Step 1: Write the failing test**

```ts
// web/src/lib/grants.test.ts
import { describe, it, expect } from 'vitest'
import { deriveGrants, scopeOf, valueSummary } from './grants'
import type { Rule, AccessRequest } from './types'

const httpRule: Rule = {
  id: 'r1', subject_agent_id: 'ag1', upstream_id: 'up1',
  op_method: 'GET', op_path_template: '/api/v4/projects/{project_path:text}/pipelines',
  op_value_policies: {
    project_path: { type: 'text', mode: 'set', values: ['infra/helm', 'infra/charts'] },
    page: { type: 'number', mode: 'range', min: 1, max: 50 },
  },
  outcome: 'allow', rate_limit_per_min: 0,
}
const profRule: Rule = {
  id: 'r2', subject_agent_id: 'ag1', upstream_id: 'up2',
  profile: 'citeck', profile_params: { op: 'write' }, outcome: 'allow', rate_limit_per_min: 0,
}
const k8sRule: Rule = {
  id: 'r3', subject_agent_id: 'ag1', upstream_id: 'up3',
  namespace: 'prod', resource: 'pods', verb: 'get', outcome: 'allow', rate_limit_per_min: 0,
}

describe('scopeOf', () => {
  it('http → method', () => expect(scopeOf(httpRule)).toEqual({ label: 'GET', kind: 'method' }))
  it('citeck write → WRITE', () => expect(scopeOf(profRule)).toEqual({ label: 'WRITE', kind: 'write' }))
  it('k8s → verb', () => expect(scopeOf(k8sRule)).toEqual({ label: 'get', kind: 'verb' }))
})

describe('valueSummary', () => {
  it('lists non-any values and ranges', () => {
    expect(valueSummary(httpRule)).toBe('project_path: infra/helm, infra/charts · page: 1–50')
  })
  it('empty when no policies', () => expect(valueSummary(profRule)).toBe(''))
})

describe('deriveGrants', () => {
  it('groups rules by (agent, upstream) and attaches the granted request purpose', () => {
    const reqs: AccessRequest[] = [{
      id: 'req1', agent_id: 'ag1', agent_name: 'claude', upstream_id: 'up1', upstream_name: 'gitlab',
      purpose: 'CI monitoring', status: 'granted', created_at: '2026-06-17T10:00:00Z',
      resolved_at: '2026-06-17T10:05:00Z',
    }]
    const grants = deriveGrants([httpRule, profRule], reqs)
    expect(grants).toHaveLength(2)
    const g1 = grants.find((g) => g.upstreamId === 'up1')!
    expect(g1.rules).toHaveLength(1)
    expect(g1.purpose).toBe('CI monitoring')
    expect(g1.grantedAt).toBe('2026-06-17T10:05:00Z')
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm -C web exec vitest run src/lib/grants.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

```ts
// web/src/lib/grants.ts
import type { Rule, AccessRequest } from './types'

export interface Grant {
  agentId: string
  upstreamId: string
  rules: Rule[]
  purpose: string // from the most-recent granted access-request for the pair, "" if none
  grantedAt: string // that request's resolved_at, "" if none
}

/** scopeOf derives the coloured scope badge (READ/WRITE/method/verb/browse) for a rule. */
export function scopeOf(rule: Rule): {
  label: string
  kind: 'method' | 'read' | 'write' | 'verb' | 'browse'
} {
  if (rule.profile === 'citeck') {
    const op = (rule.profile_params?.['op'] as string | undefined) ?? 'read'
    return op === 'write' ? { label: 'WRITE', kind: 'write' } : { label: 'READ', kind: 'read' }
  }
  if (rule.browse_path) return { label: 'BROWSE', kind: 'browse' }
  if (rule.namespace || rule.resource || rule.verb) {
    return { label: rule.verb || '*', kind: 'verb' }
  }
  return { label: (rule.op_method || '*').toUpperCase(), kind: 'method' }
}

/** valueSummary renders the non-`*` value constraints of a rule as a compact "var: a, b · n: 1–50"
 *  string (for the collapsed rule tail). Variables set to "any" are omitted. Empty if none constrained. */
export function valueSummary(rule: Rule): string {
  const pols = rule.op_value_policies ?? {}
  const parts: string[] = []
  for (const [name, p] of Object.entries(pols)) {
    if (p.mode === 'any') continue
    if (p.type === 'number' && p.mode === 'range') {
      const lo = p.min ?? '−∞'
      const hi = p.max ?? '∞'
      parts.push(`${name}: ${lo}–${hi}`)
    } else if ((p.values ?? []).length > 0) {
      parts.push(`${name}: ${(p.values ?? []).join(', ')}`)
    }
  }
  return parts.join(' · ')
}

/** deriveGrants groups rules by (agent, upstream) into grants and attaches the purpose/date of the
 *  most-recent granted access-request for that pair. */
export function deriveGrants(rules: Rule[], requests: AccessRequest[]): Grant[] {
  const byPair = new Map<string, Grant>()
  const key = (a: string, u: string) => `${a} ${u}`
  for (const rule of rules) {
    const k = key(rule.subject_agent_id, rule.upstream_id)
    let g = byPair.get(k)
    if (!g) {
      g = { agentId: rule.subject_agent_id, upstreamId: rule.upstream_id, rules: [], purpose: '', grantedAt: '' }
      byPair.set(k, g)
    }
    g.rules.push(rule)
  }
  // Attach purpose from the newest granted request per pair (requests already arrive created_at DESC).
  for (const req of requests) {
    if (req.status !== 'granted') continue
    const g = byPair.get(key(req.agent_id, req.upstream_id))
    if (g && !g.purpose) {
      g.purpose = req.purpose
      g.grantedAt = req.resolved_at
    }
  }
  return [...byPair.values()]
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `pnpm -C web exec vitest run src/lib/grants.test.ts`
Expected: PASS (7 assertions).

- [ ] **Step 5: Commit**

```bash
git checkout -- internal/daemon/webdist/index.html
git add web/src/lib/grants.ts web/src/lib/grants.test.ts
git commit -m "feat(web): grants deriver + scope/value helpers"
```

---

## Task 4: Frontend — grouping-toggle store + `ScopeBadge`

**Files:**
- Create: `web/src/lib/accessGrouping.ts`
- Create: `web/src/pages/access/scope.tsx`
- Test: `web/src/pages/access/scope.test.tsx`

**Interfaces:**
- Produces:
  - `useAccessGrouping()` — zustand store `{ by: 'agent' | 'upstream'; setBy(v): void }` (in-memory, mirrors `web/src/lib/upstreamsTab.ts`).
  - `<ScopeBadge scope={{ label, kind }} />` — coloured badge.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/pages/access/scope.test.tsx
import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { ScopeBadge } from './scope'

afterEach(cleanup)

describe('<ScopeBadge>', () => {
  it('renders the label with a write-danger class', () => {
    render(<ScopeBadge scope={{ label: 'WRITE', kind: 'write' }} />)
    const el = screen.getByText('WRITE')
    expect(el.className).toContain('text-warning')
  })
  it('renders a method scope', () => {
    render(<ScopeBadge scope={{ label: 'GET', kind: 'method' }} />)
    expect(screen.getByText('GET')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm -C web exec vitest run src/pages/access/scope.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement both files**

```ts
// web/src/lib/accessGrouping.ts — mirror web/src/lib/upstreamsTab.ts exactly (read it first).
import { create } from 'zustand'

interface AccessGroupingState {
  by: 'agent' | 'upstream'
  setBy: (by: 'agent' | 'upstream') => void
}

export const useAccessGrouping = create<AccessGroupingState>((set) => ({
  by: 'agent',
  setBy: (by) => set({ by }),
}))
```

```tsx
// web/src/pages/access/scope.tsx
interface ScopeBadgeProps {
  scope: { label: string; kind: 'method' | 'read' | 'write' | 'verb' | 'browse' }
}

// Colour maps to the app's semantic tokens: write is the loudest (warning), reads/methods calmer.
const CLASS: Record<ScopeBadgeProps['scope']['kind'], string> = {
  method: 'bg-success/15 text-success',
  read: 'bg-primary/15 text-primary',
  write: 'bg-warning/15 text-warning',
  verb: 'bg-primary/15 text-primary',
  browse: 'bg-muted text-muted-foreground',
}

export function ScopeBadge({ scope }: ScopeBadgeProps) {
  return (
    <span className={`rounded px-2 py-0.5 text-[11px] font-bold tracking-wide ${CLASS[scope.kind]}`}>
      {scope.label}
    </span>
  )
}
```

Verify `--color-warning` / `text-warning` exists in the theme (grep `warning` in `web/src/index.css` or the Tailwind config); the Audit page already uses `text-warning`, so it does.

- [ ] **Step 4: Run to verify it passes**

Run: `pnpm -C web exec vitest run src/pages/access/scope.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git checkout -- internal/daemon/webdist/index.html
git add web/src/lib/accessGrouping.ts web/src/pages/access/scope.tsx web/src/pages/access/scope.test.tsx
git commit -m "feat(web): access grouping store + ScopeBadge"
```

---

## Task 5: Frontend — extract approval cards into a reusable module

**Files:**
- Create: `web/src/pages/access/ApprovalCards.tsx`
- Modify: `web/src/pages/Approvals.tsx` (temporarily import from the new module — Approvals is deleted in Task 11, but keeping it green in between avoids a broken build)

**Interfaces:**
- Produces: `export function ApprovalCard(props: { approval: Approval; onResolve: (id: string, approve: boolean, opts?: ResolveOptions) => void }): JSX.Element` plus the per-kind subcomponents, moved **verbatim** from `Approvals.tsx` (HostCard, OperationCard, K8sAccessCard, PresetCard, NewValueCard, PlainCard, ApprovalCard, and the helpers `shortId`, `exampleURL`, `segmentsOf`).

- [ ] **Step 1: Move the card code**

Cut lines for `shortId`, `exampleURL`, `segmentsOf`, `cardClass`/`approveBtn`/`denyBtn`/`trustBtn`, `HostCard`…`ApprovalCard` from `web/src/pages/Approvals.tsx` into `web/src/pages/access/ApprovalCards.tsx`. Add the imports the moved code needs (`useState`, `useEffect`, `previewPreset`, types, `StatusBadge`, `FormField`, `fieldControlClass`, `Select`). Export `ApprovalCard`, `shortId`, `exampleURL`, `segmentsOf`.

- [ ] **Step 2: Re-point Approvals.tsx**

In `web/src/pages/Approvals.tsx`, replace the removed definitions with:

```ts
import { ApprovalCard, shortId } from './access/ApprovalCards'
```

(remove the now-duplicate `shortId`, keep the rest of `Approvals.tsx` working).

- [ ] **Step 3: Run the existing Approvals test + typecheck**

Run: `pnpm -C web exec vitest run src/pages/Approvals.test.tsx && pnpm -C web exec tsc --noEmit`
Expected: PASS / no type errors (pure move, behaviour unchanged).

- [ ] **Step 4: Commit**

```bash
git checkout -- internal/daemon/webdist/index.html
git add web/src/pages/access/ApprovalCards.tsx web/src/pages/Approvals.tsx
git commit -m "refactor(web): extract ApprovalCard into access/ApprovalCards"
```

---

## Task 6: Frontend — RequestsPanel (pending queue, aggregated)

**Files:**
- Create: `web/src/pages/access/RequestsPanel.tsx`
- Test: `web/src/pages/access/RequestsPanel.test.tsx`

**Interfaces:**
- Consumes: `ApprovalCard` (Task 5), `listApprovals`, `resolveApproval`, `Approval`, `ResolveOptions`, `useToastStore`.
- Produces: `export function RequestsPanel({ approvals, onChanged }: { approvals: Approval[]; onChanged: () => void }): JSX.Element` — renders the `⧗ Запросы прав (N)` header + a list of `ApprovalCard`, wiring approve/deny (deny → reason modal). When `approvals` is empty, renders a muted "Нет запросов прав".

The panel receives `approvals` from the parent Access page (single fetch) and calls `onChanged()` after a resolve so the parent reloads rules+requests+approvals together.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/pages/access/RequestsPanel.test.tsx
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { RequestsPanel } from './RequestsPanel'
import * as api from '../../lib/api'
import type { Approval } from '../../lib/types'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

const hostApproval: Approval = {
  id: 'a1', agent_id: 'ag1', upstream_id: 'up1', method: '', path: '', purpose: 'read logs',
  created_at: '2026-07-07T10:00:00Z', kind: 'host-access', host: 'gitlab.example.com',
}

describe('<RequestsPanel>', () => {
  it('shows the count and renders an approval card', () => {
    render(<RequestsPanel approvals={[hostApproval]} onChanged={() => {}} />)
    expect(screen.getByText('Запросы прав')).toBeInTheDocument()
    expect(screen.getByText('1')).toBeInTheDocument()
    expect(screen.getByText('gitlab.example.com')).toBeInTheDocument()
  })
  it('shows an empty state when there are none', () => {
    render(<RequestsPanel approvals={[]} onChanged={() => {}} />)
    expect(screen.getByText('Нет запросов прав')).toBeInTheDocument()
  })
  it('approves via resolveApproval and calls onChanged', async () => {
    const spy = vi.spyOn(api, 'resolveApproval').mockResolvedValue({ ok: true })
    const onChanged = vi.fn()
    render(<RequestsPanel approvals={[hostApproval]} onChanged={onChanged} />)
    fireEvent.click(screen.getByRole('button', { name: 'Approve' }))
    await waitFor(() => expect(spy).toHaveBeenCalled())
    await waitFor(() => expect(onChanged).toHaveBeenCalled())
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm -C web exec vitest run src/pages/access/RequestsPanel.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

```tsx
// web/src/pages/access/RequestsPanel.tsx
import { useState } from 'react'
import { Clock } from 'lucide-react'
import { resolveApproval, ApiError } from '../../lib/api'
import type { Approval, ResolveOptions } from '../../lib/types'
import { ApprovalCard } from './ApprovalCards'
import { Modal } from '../../components/Modal'
import { FormField, fieldControlClass } from '../../components/FormField'
import { useToastStore } from '../../lib/toast'

export function RequestsPanel({
  approvals,
  onChanged,
}: {
  approvals: Approval[]
  onChanged: () => void
}) {
  const [denyId, setDenyId] = useState<string | null>(null)
  const [denyReason, setDenyReason] = useState('')
  const push = useToastStore((s) => s.push)

  async function decide(id: string, approve: boolean, opts?: ResolveOptions) {
    try {
      await (opts ? resolveApproval(id, approve, opts) : resolveApproval(id, approve))
      push('success', approve ? 'Request approved' : 'Request denied')
      onChanged()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to resolve')
    }
  }

  function confirmDeny(e?: React.FormEvent) {
    e?.preventDefault()
    const id = denyId
    setDenyId(null)
    if (id) void decide(id, false, denyReason.trim() ? { reason: denyReason.trim() } : undefined)
  }

  return (
    <section className="space-y-2">
      <header className="flex items-center gap-2">
        <Clock size={15} className="text-warning" />
        <span className="text-[13px] font-semibold">Запросы прав</span>
        <span className="rounded-full border border-warning/50 px-2 text-[11px] text-warning">
          {approvals.length}
        </span>
      </header>
      {approvals.length === 0 ? (
        <div className="rounded-lg border border-border bg-card px-3 py-6 text-center text-xs text-muted-foreground">
          Нет запросов прав
        </div>
      ) : (
        <div className="space-y-2">
          {approvals.map((a) => (
            <ApprovalCard
              key={a.id}
              approval={a}
              onResolve={(id, approve, opts) => {
                if (!approve) {
                  setDenyId(id)
                  setDenyReason('')
                } else void decide(id, true, opts)
              }}
            />
          ))}
        </div>
      )}

      <Modal
        open={denyId !== null}
        title="Deny request"
        onClose={() => setDenyId(null)}
        onSubmit={confirmDeny}
        footer={
          <>
            <button
              type="button"
              onClick={() => setDenyId(null)}
              className="rounded bg-muted px-3 py-1.5 text-xs font-medium text-muted-foreground hover:text-foreground"
            >
              Cancel
            </button>
            <button type="submit" className="rounded bg-destructive px-3 py-1.5 text-xs font-medium text-white hover:opacity-90">
              Deny
            </button>
          </>
        }
      >
        <FormField label="Reason (optional — shown to the agent)">
          <textarea
            className={fieldControlClass}
            rows={3}
            value={denyReason}
            onChange={(e) => setDenyReason(e.target.value)}
            aria-label="Deny reason"
            autoFocus
          />
        </FormField>
      </Modal>
    </section>
  )
}
```

Note: the mockup's amber-accent "право — герой" restyling of the card interior belongs to `ApprovalCards.tsx`. For this task the extracted cards render as-is; the visual re-skin (scope-badge hero box + readable purpose) is Task 6b below. Keep the panel logic here.

- [ ] **Step 4: Run to verify it passes**

Run: `pnpm -C web exec vitest run src/pages/access/RequestsPanel.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git checkout -- internal/daemon/webdist/index.html
git add web/src/pages/access/RequestsPanel.tsx web/src/pages/access/RequestsPanel.test.tsx
git commit -m "feat(web): Access RequestsPanel (aggregated pending queue)"
```

---

## Task 6b: Frontend — re-skin OperationCard/PresetCard to the "право — герой" layout

**Files:**
- Modify: `web/src/pages/access/ApprovalCards.tsx`

**Interfaces:** unchanged public API (`ApprovalCard`); only the internal JSX of the cards changes to match mockup `access-layout-v11.html`.

Per the spec: header row `agent → upstream · tag · <time> · Approve/Deny`; a bordered **hero box** with a `ScopeBadge` + the resource/path (variables as chips); a readable purpose line under it (foreground text + a `MessageSquare` icon), with no repeated agent name and no inline time.

- [ ] **Step 1: Update OperationCard/PresetCard/HostCard/NewValueCard/K8sAccessCard**

Wrap each card's identity line as the header (agent/host, tag, and move the existing Approve/Deny button group up next to a right-aligned muted `created_at`). Replace the muted operation-shape line with a hero box:

```tsx
import { ScopeBadge } from './scope'
import { scopeOf } from '../../lib/grants'      // for rules; approvals expose op_method directly
import { MessageSquare } from 'lucide-react'

// hero box (reuse inside each card, deriving the scope from the approval kind):
<div className="my-2 flex flex-wrap items-center gap-2 rounded-md border border-border bg-card px-2.5 py-1.5">
  <ScopeBadge scope={{ label: approval.op_method || 'GET', kind: 'method' }} />
  <span className="font-mono text-[13px] font-semibold">{/* segmentsOf(op_path_template) chips */}</span>
</div>
<div className="flex items-start gap-1.5 text-[13px] text-foreground">
  <MessageSquare size={13} className="mt-0.5 shrink-0 text-muted-foreground" />
  <span>{approval.purpose}</span>
</div>
```

For preset cards the scope comes from the preset preview (`previewPreset` already returns human rules); render the preset label + a READ/WRITE badge inferred from the preset id (`*-readonly` → READ else READ/WRITE as today). Keep the existing slot editors and the deny/approve wiring untouched.

- [ ] **Step 2: Keep the Approvals test green**

Run: `pnpm -C web exec vitest run src/pages/Approvals.test.tsx src/pages/access/RequestsPanel.test.tsx && pnpm -C web exec tsc --noEmit`
Expected: PASS. If a query in `Approvals.test.tsx` matched removed muted text, update the assertion to the new hero/purpose text.

- [ ] **Step 3: Commit**

```bash
git checkout -- internal/daemon/webdist/index.html
git add web/src/pages/access/ApprovalCards.tsx web/src/pages/Approvals.test.tsx
git commit -m "feat(web): approval cards — scope-badge hero + readable purpose"
```

---

## Task 7: Frontend — RuleRow (rule + expandable value-set editor)

**Files:**
- Create: `web/src/pages/access/RuleRow.tsx`
- Test: `web/src/pages/access/RuleRow.test.tsx`

**Interfaces:**
- Consumes: `Rule`, `scopeOf`, `valueSummary`, `setRuleVariablePolicy`, `deleteRule`, `ScopeBadge`, the value-set editors moved from `Rules.tsx` (`ValueSetEditor`, `NumberRangeEditor` — **text and number only**; do NOT port `EnumSetEditor`).
- Produces: `export function RuleRow({ rule, onChanged }: { rule: Rule; onChanged: () => void }): JSX.Element` — collapsed shows `ScopeBadge + path (chips) + valueSummary tail + allow + pencil/trash`; a chevron expands the per-variable editors (text chips + trust-any, number range + any); pencil toggles expand; trash calls `deleteRule` then `onChanged`.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/pages/access/RuleRow.test.tsx
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { RuleRow } from './RuleRow'
import * as api from '../../lib/api'
import type { Rule } from '../../lib/types'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

const rule: Rule = {
  id: 'r1', subject_agent_id: 'ag1', upstream_id: 'up1', op_method: 'GET',
  op_path_template: '/api/v4/projects/{project_path:text}/pipelines',
  op_value_policies: { project_path: { type: 'text', mode: 'set', values: ['infra/helm'] } },
  outcome: 'allow', rate_limit_per_min: 0,
}

describe('<RuleRow>', () => {
  it('shows scope, path and the value summary tail', () => {
    render(<RuleRow rule={rule} onChanged={() => {}} />)
    expect(screen.getByText('GET')).toBeInTheDocument()
    expect(screen.getByText(/project_path: infra\/helm/)).toBeInTheDocument()
  })
  it('expands to show the text value-set editor with the chip', () => {
    render(<RuleRow rule={rule} onChanged={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: /expand rule/i }))
    expect(screen.getByText('infra/helm')).toBeInTheDocument()
    expect(screen.getByLabelText('Trust any value for project_path')).toBeInTheDocument()
  })
  it('deletes the rule', async () => {
    const spy = vi.spyOn(api, 'deleteRule').mockResolvedValue({ ok: true })
    const onChanged = vi.fn()
    render(<RuleRow rule={rule} onChanged={onChanged} />)
    fireEvent.click(screen.getByRole('button', { name: /delete rule/i }))
    await waitFor(() => expect(spy).toHaveBeenCalledWith('r1'))
    await waitFor(() => expect(onChanged).toHaveBeenCalled())
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm -C web exec vitest run src/pages/access/RuleRow.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Port the value-set editors, then implement RuleRow**

First copy `ValueSetEditor` and `NumberRangeEditor` (and the `segmentsOf` path renderer) verbatim from `web/src/pages/Rules.tsx` into `RuleRow.tsx` (or a shared `web/src/pages/access/valueEditors.tsx`). Do NOT copy `EnumSetEditor`. Then:

```tsx
// web/src/pages/access/RuleRow.tsx (structure; fill editor bodies from the ported code)
import { useState } from 'react'
import { ChevronRight, Pencil, Trash2 } from 'lucide-react'
import { deleteRule, ApiError } from '../../lib/api'
import type { Rule } from '../../lib/types'
import { scopeOf, valueSummary } from '../../lib/grants'
import { ScopeBadge } from './scope'
import { useToastStore } from '../../lib/toast'
// import { ValueSetEditor, NumberRangeEditor, segmentsOf } from './valueEditors'

export function RuleRow({ rule, onChanged }: { rule: Rule; onChanged: () => void }) {
  const [open, setOpen] = useState(false)
  const push = useToastStore((s) => s.push)
  const scope = scopeOf(rule)
  const summary = valueSummary(rule)
  const pols = rule.op_value_policies ?? {}
  const textVars = Object.entries(pols).filter(([, p]) => p.type === 'text')
  const numberVars = Object.entries(pols).filter(([, p]) => p.type === 'number')

  async function remove() {
    try {
      await deleteRule(rule.id)
      push('success', 'Operation deleted')
      onChanged()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to delete operation')
    }
  }

  const hasEditors = textVars.length + numberVars.length > 0
  return (
    <div className="rounded-md border border-border bg-card">
      <div className="flex items-center gap-2.5 px-2.5 py-1.5">
        {hasEditors && (
          <button
            onClick={() => setOpen((o) => !o)}
            aria-label={open ? 'collapse rule' : 'expand rule'}
            className="text-muted-foreground hover:text-foreground"
          >
            <ChevronRight size={13} className={open ? 'rotate-90 transition' : 'transition'} />
          </button>
        )}
        <ScopeBadge scope={scope} />
        <span className="font-mono text-[12.5px] font-semibold">
          {/* segmentsOf(rule.op_path_template ?? '') with variable chips; for k8s/profile render the resource */}
          {rule.op_path_template || resourceLabel(rule)}
        </span>
        {summary && <span className="ml-1 text-[11px] text-muted-foreground">· {summary}</span>}
        <div className="ml-auto flex items-center gap-1">
          <span className="mr-1 text-[11px] text-success">{rule.outcome}</span>
          {hasEditors && (
            <button onClick={() => setOpen((o) => !o)} aria-label="edit rule"
              className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground">
              <Pencil size={13} />
            </button>
          )}
          <button onClick={remove} aria-label={`delete rule ${rule.id}`}
            className="rounded p-1 text-muted-foreground hover:bg-destructive/15 hover:text-destructive">
            <Trash2 size={13} />
          </button>
        </div>
      </div>
      {open && hasEditors && (
        <div className="space-y-2 border-t border-border px-2.5 py-2">
          {textVars.map(([name, p]) => (
            <ValueSetEditor key={name} ruleID={rule.id} varName={name} policy={p} onChange={onChanged} />
          ))}
          {numberVars.map(([name, p]) => (
            <NumberRangeEditor key={name} ruleID={rule.id} varName={name} policy={p} onChange={onChanged} />
          ))}
        </div>
      )}
    </div>
  )
}

// resourceLabel renders a compact resource string for non-http rules.
function resourceLabel(rule: Rule): string {
  if (rule.profile === 'citeck') {
    const pp = rule.profile_params ?? {}
    return `Records · source ${pp['source_id'] ?? '*'} · ws ${pp['workspace'] ?? '*'}`
  }
  if (rule.namespace || rule.resource || rule.verb) return `${rule.namespace || '*'}/${rule.resource || '*'}`
  if (rule.browse_path) return rule.browse_path
  return rule.op_path_template ?? ''
}
```

Keep `ValueSetEditor`/`NumberRangeEditor` exactly as in `Rules.tsx` — their `aria-label`s (`Trust any value for <name>`, `Allow any number for <name>`) are what the test queries.

- [ ] **Step 4: Run to verify it passes**

Run: `pnpm -C web exec vitest run src/pages/access/RuleRow.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git checkout -- internal/daemon/webdist/index.html
git add web/src/pages/access/RuleRow.tsx web/src/pages/access/valueEditors.tsx web/src/pages/access/RuleRow.test.tsx
git commit -m "feat(web): RuleRow with text/number value-set editors (no enum)"
```

---

## Task 8: Frontend — UpstreamGrantCard (one grant + revoke)

**Files:**
- Create: `web/src/pages/access/UpstreamGrantCard.tsx`
- Test: `web/src/pages/access/UpstreamGrantCard.test.tsx`

**Interfaces:**
- Consumes: `Grant` (Task 3), `Upstream`, `RuleRow`, `revokeGrant`, `useToastStore`.
- Produces: `export function UpstreamGrantCard({ grant, upstream, onChanged }: { grant: Grant; upstream?: Upstream; onChanged: () => void }): JSX.Element` — one-line header (icon by `upstream.kind`, hostname, `· <kind>`, Revoke) + `RuleRow` list.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/pages/access/UpstreamGrantCard.test.tsx
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { UpstreamGrantCard } from './UpstreamGrantCard'
import * as api from '../../lib/api'
import type { Grant } from '../../lib/grants'
import type { Upstream } from '../../lib/types'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

const grant: Grant = {
  agentId: 'ag1', upstreamId: 'up1', purpose: 'CI', grantedAt: '2026-06-17T10:05:00Z',
  rules: [{ id: 'r1', subject_agent_id: 'ag1', upstream_id: 'up1', op_method: 'GET',
    op_path_template: '/x', outcome: 'allow', rate_limit_per_min: 0 }],
}
const up: Upstream = { id: 'up1', name: 'gitlab.example.com', base_url: '', auth_type: '', kind: 'http' }

describe('<UpstreamGrantCard>', () => {
  it('renders the hostname and a rule', () => {
    render(<UpstreamGrantCard grant={grant} upstream={up} onChanged={() => {}} />)
    expect(screen.getByText('gitlab.example.com')).toBeInTheDocument()
    expect(screen.getByText('GET')).toBeInTheDocument()
  })
  it('revokes the grant', async () => {
    const spy = vi.spyOn(api, 'revokeGrant').mockResolvedValue({ ok: true, rules_removed: 1 })
    const onChanged = vi.fn()
    render(<UpstreamGrantCard grant={grant} upstream={up} onChanged={onChanged} />)
    fireEvent.click(screen.getByRole('button', { name: 'Revoke' }))
    await waitFor(() => expect(spy).toHaveBeenCalledWith('ag1', 'up1'))
    await waitFor(() => expect(onChanged).toHaveBeenCalled())
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm -C web exec vitest run src/pages/access/UpstreamGrantCard.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

```tsx
// web/src/pages/access/UpstreamGrantCard.tsx
import { Globe, Database, Boxes } from 'lucide-react'
import { revokeGrant, ApiError } from '../../lib/api'
import type { Grant } from '../../lib/grants'
import type { Upstream } from '../../lib/types'
import { RuleRow } from './RuleRow'
import { useToastStore } from '../../lib/toast'

function kindIcon(kind?: string) {
  if (kind === 'k8s') return Boxes
  if (kind === 'citeck') return Database
  return Globe
}

export function UpstreamGrantCard({
  grant,
  upstream,
  onChanged,
}: {
  grant: Grant
  upstream?: Upstream
  onChanged: () => void
}) {
  const push = useToastStore((s) => s.push)
  const kind = upstream?.kind || upstream?.profile || 'http'
  const Icon = kindIcon(upstream?.profile ? 'citeck' : upstream?.kind)
  const host = upstream?.name ?? grant.upstreamId

  async function revoke() {
    try {
      await revokeGrant(grant.agentId, grant.upstreamId)
      push('success', 'Access revoked')
      onChanged()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to revoke')
    }
  }

  return (
    <div className="overflow-hidden rounded-lg border border-border bg-muted/30">
      <div className="flex items-center gap-2 border-b border-border px-3 py-1.5">
        <Icon size={15} className="text-muted-foreground" />
        <span className="font-mono text-[13px]">{host}</span>
        <span className="text-[11px] text-muted-foreground">· {kind}</span>
        <button
          onClick={revoke}
          className="ml-auto rounded border border-border px-2.5 py-1 text-[11px] font-medium text-muted-foreground hover:border-destructive/60 hover:text-destructive"
        >
          Revoke
        </button>
      </div>
      <div className="space-y-1.5 p-2.5">
        {grant.rules.map((r) => (
          <RuleRow key={r.id} rule={r} onChanged={onChanged} />
        ))}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `pnpm -C web exec vitest run src/pages/access/UpstreamGrantCard.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git checkout -- internal/daemon/webdist/index.html
git add web/src/pages/access/UpstreamGrantCard.tsx web/src/pages/access/UpstreamGrantCard.test.tsx
git commit -m "feat(web): UpstreamGrantCard (grant header + rules + revoke)"
```

---

## Task 9: Frontend — AgentCard (collapsible container, delete, kebab)

**Files:**
- Create: `web/src/pages/access/AgentCard.tsx`
- Test: `web/src/pages/access/AgentCard.test.tsx`

**Interfaces:**
- Consumes: `Agent`, `Grant`, `Upstream`, `UpstreamGrantCard`, `deleteAgent`, `useNavigate` (react-router), `useToastStore`.
- Produces: `export function AgentCard({ agent, grants, upstreams, onChanged }: { agent: Agent; grants: Grant[]; upstreams: Upstream[]; onChanged: () => void }): JSX.Element` — collapsible; header shows avatar/name/id/status/counter/meta + trash (delete agent) + ⋮ kebab (menu: "История запросов в Audit" → `navigate('/audit?tab=requests&agent=' + agent.id)`, "Удалить агента"). Header click toggles collapse; clicks on trash/kebab do not.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/pages/access/AgentCard.test.tsx
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { AgentCard } from './AgentCard'
import * as api from '../../lib/api'
import type { Agent } from '../../lib/types'
import type { Grant } from '../../lib/grants'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

const agent: Agent = { id: 'ag1', name: 'claude', status: 'active', created_at: '2026-06-17T10:00:00Z', last_seen_at: '' }
const grants: Grant[] = [{ agentId: 'ag1', upstreamId: 'up1', purpose: '', grantedAt: '',
  rules: [{ id: 'r1', subject_agent_id: 'ag1', upstream_id: 'up1', op_method: 'GET', op_path_template: '/x', outcome: 'allow', rate_limit_per_min: 0 }] }]
const ups = [{ id: 'up1', name: 'gitlab', base_url: '', auth_type: '', kind: 'http' }]

function renderCard() {
  return render(<MemoryRouter><AgentCard agent={agent} grants={grants} upstreams={ups} onChanged={() => {}} /></MemoryRouter>)
}

describe('<AgentCard>', () => {
  it('toggles collapse when the header body is clicked', () => {
    renderCard()
    expect(screen.getByText('gitlab')).toBeInTheDocument()      // expanded by default
    fireEvent.click(screen.getByText('claude'))
    expect(screen.queryByText('gitlab')).not.toBeInTheDocument() // collapsed
  })
  it('deletes the agent immediately from the trash icon (no modal, no toggle)', async () => {
    const spy = vi.spyOn(api, 'deleteAgent').mockResolvedValue({ ok: true })
    renderCard()
    fireEvent.click(screen.getByRole('button', { name: 'Delete agent claude' }))
    await waitFor(() => expect(spy).toHaveBeenCalledWith('ag1'))
    expect(screen.getByText('gitlab')).toBeInTheDocument()       // still expanded → header didn't toggle
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm -C web exec vitest run src/pages/access/AgentCard.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

```tsx
// web/src/pages/access/AgentCard.tsx
import { useState } from 'react'
import { useNavigate } from 'react-router'
import { ChevronRight, Trash2, MoreVertical, ArrowRight } from 'lucide-react'
import { deleteAgent, ApiError } from '../../lib/api'
import type { Agent, Upstream } from '../../lib/types'
import type { Grant } from '../../lib/grants'
import { UpstreamGrantCard } from './UpstreamGrantCard'
import { useToastStore } from '../../lib/toast'

function fmtTime(iso: string): string {
  if (!iso) return 'Never'
  const d = new Date(iso)
  return isNaN(d.getTime()) ? iso : d.toLocaleString()
}

export function AgentCard({
  agent,
  grants,
  upstreams,
  onChanged,
}: {
  agent: Agent
  grants: Grant[]
  upstreams: Upstream[]
  onChanged: () => void
}) {
  const [open, setOpen] = useState(true)
  const [menu, setMenu] = useState(false)
  const navigate = useNavigate()
  const push = useToastStore((s) => s.push)
  const upstreamOf = (id: string) => upstreams.find((u) => u.id === id)
  const ruleCount = grants.reduce((n, g) => n + g.rules.length, 0)

  async function remove() {
    try {
      await deleteAgent(agent.id)
      push('success', `Agent "${agent.name}" deleted`)
      onChanged()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to delete agent')
    }
  }

  return (
    <div className="relative overflow-hidden rounded-xl border border-border bg-card">
      <div
        onClick={() => setOpen((o) => !o)}
        className="flex cursor-pointer select-none items-center gap-2.5 px-3.5 py-2.5"
      >
        <ChevronRight size={14} className={`text-muted-foreground ${open ? 'rotate-90 transition' : 'transition'}`} />
        <span className="flex h-6 w-6 items-center justify-center rounded bg-primary/15 text-[12px] font-bold text-primary">
          {agent.name.charAt(0).toUpperCase()}
        </span>
        <span className="text-sm font-semibold">{agent.name}</span>
        <span className="font-mono text-[11px] text-muted-foreground">{agent.id.slice(0, 8)}</span>
        <span
          className="inline-block h-1.5 w-1.5 rounded-full"
          style={{ backgroundColor: 'var(--color-status-running)' }}
          title={agent.status}
        />
        <span className="text-xs text-muted-foreground">
          <b className="font-semibold text-foreground">{ruleCount}</b> прав ·{' '}
          <b className="font-semibold text-foreground">{grants.length}</b> ресурсов
        </span>
        <div className="ml-auto flex items-center gap-3 text-[11px] text-muted-foreground">
          <span>активн. <b className="font-medium text-foreground">{fmtTime(agent.last_seen_at)}</b></span>
          <div className="flex items-center gap-1">
            <button
              onClick={(e) => { e.stopPropagation(); void remove() }}
              aria-label={`Delete agent ${agent.name}`}
              className="rounded p-1 text-muted-foreground hover:bg-destructive/15 hover:text-destructive"
            >
              <Trash2 size={13} />
            </button>
            <div className="relative">
              <button
                onClick={(e) => { e.stopPropagation(); setMenu((m) => !m) }}
                aria-label="Agent menu"
                className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
              >
                <MoreVertical size={13} />
              </button>
              {menu && (
                <div
                  onClick={(e) => e.stopPropagation()}
                  className="absolute right-0 top-7 z-10 w-52 rounded-lg border border-border bg-card p-1 shadow-xl"
                >
                  <button
                    onClick={() => { setMenu(false); navigate(`/audit?tab=requests&agent=${agent.id}`) }}
                    className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-xs hover:bg-muted"
                  >
                    <ArrowRight size={13} className="text-muted-foreground" /> История запросов в Audit
                  </button>
                  <button
                    onClick={() => { setMenu(false); void remove() }}
                    className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-xs text-destructive hover:bg-muted"
                  >
                    <Trash2 size={13} /> Удалить агента
                  </button>
                </div>
              )}
            </div>
          </div>
        </div>
      </div>
      {open && (
        <div className="space-y-2.5 border-t border-border px-3.5 py-3">
          {grants.map((g) => (
            <UpstreamGrantCard key={g.upstreamId} grant={g} upstream={upstreamOf(g.upstreamId)} onChanged={onChanged} />
          ))}
        </div>
      )}
    </div>
  )
}
```

Verify the status-dot CSS var name (`--color-status-running`) matches Sidebar's usage (it does — Sidebar uses it).

- [ ] **Step 4: Run to verify it passes**

Run: `pnpm -C web exec vitest run src/pages/access/AgentCard.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git checkout -- internal/daemon/webdist/index.html
git add web/src/pages/access/AgentCard.tsx web/src/pages/access/AgentCard.test.tsx
git commit -m "feat(web): AgentCard (collapsible, delete icon, kebab menu)"
```

---

## Task 10: Frontend — ManualRuleModal ("Выдать вручную")

**Files:**
- Create: `web/src/pages/access/ManualRuleModal.tsx`
- Test: `web/src/pages/access/ManualRuleModal.test.tsx`

**Interfaces:**
- Consumes: `createRule`, `listUpstreams`, `listAgents`, `Upstream`, `Agent`, `Modal`, `FormField`, `Select`.
- Produces: `export function ManualRuleModal({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: () => void }): JSX.Element` — the add-operation form moved from `Rules.tsx` (`DraftRule`, `parseOpValues`, k8s/citeck/http branches). Behaviour identical to the old Operations "Add operation" modal.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/pages/access/ManualRuleModal.test.tsx
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { ManualRuleModal } from './ManualRuleModal'
import * as api from '../../lib/api'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('<ManualRuleModal>', () => {
  it('creates an http operation rule', async () => {
    vi.spyOn(api, 'listUpstreams').mockResolvedValue([{ id: 'up1', name: 'gitlab', base_url: '', auth_type: '', kind: 'http' }])
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    const spy = vi.spyOn(api, 'createRule').mockResolvedValue({ id: 'r1' })
    const onCreated = vi.fn()
    render(<ManualRuleModal open onClose={() => {}} onCreated={onCreated} />)
    await screen.findByLabelText('Operation path-template')
    fireEvent.change(screen.getByLabelText('Operation path-template'), { target: { value: '/x' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(spy).toHaveBeenCalled())
    await waitFor(() => expect(onCreated).toHaveBeenCalled())
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm -C web exec vitest run src/pages/access/ManualRuleModal.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Move the modal out of Rules.tsx**

Copy `DraftRule`, `emptyDraft`, `parseOpValues`, `K8S_VERBS`, the `submit`/`openModal`/`draftIsK8s`/`draftProfile` logic and the entire `<Modal open={open} title="Add operation" …>` JSX from `web/src/pages/Rules.tsx` into `ManualRuleModal.tsx`. Wrap it as a self-contained component that fetches upstreams+agents on open and calls `onCreated()` after `createRule`. Keep every `aria-label` (`Operation path-template`, `Method`, `Allowed values`, `Subject`, `Host`, …) unchanged.

- [ ] **Step 4: Run to verify it passes**

Run: `pnpm -C web exec vitest run src/pages/access/ManualRuleModal.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git checkout -- internal/daemon/webdist/index.html
git add web/src/pages/access/ManualRuleModal.tsx web/src/pages/access/ManualRuleModal.test.tsx
git commit -m "feat(web): ManualRuleModal (Выдать вручную) extracted from Operations"
```

---

## Task 11: Frontend — Access page + GrantGroups + wiring

**Files:**
- Create: `web/src/pages/Access.tsx`
- Create: `web/src/pages/access/GrantGroups.tsx`
- Test: `web/src/pages/Access.test.tsx`

**Interfaces:**
- Consumes: `listRules`, `listAgents`, `listUpstreams`, `listApprovals`, `listAccessRequests`, `deriveGrants`, `useAccessGrouping`, `RequestsPanel`, `AgentCard`, `UpstreamGrantCard`, `ManualRuleModal`, `useEventStore`.
- Produces: `export function Access(): JSX.Element` (the page) and `export function GrantGroups({ grants, agents, upstreams, by, onChanged }): JSX.Element` (renders agent-grouped `AgentCard`s or upstream-grouped cards).

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/pages/Access.test.tsx
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { Access } from './Access'
import * as api from '../lib/api'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

function seed() {
  vi.spyOn(api, 'listAgents').mockResolvedValue([{ id: 'ag1', name: 'claude', status: 'active', created_at: '2026-06-17T10:00:00Z', last_seen_at: '' }])
  vi.spyOn(api, 'listUpstreams').mockResolvedValue([{ id: 'up1', name: 'gitlab', base_url: '', auth_type: '', kind: 'http' }])
  vi.spyOn(api, 'listRules').mockResolvedValue([{ id: 'r1', subject_agent_id: 'ag1', upstream_id: 'up1', op_method: 'GET', op_path_template: '/x', outcome: 'allow', rate_limit_per_min: 0 }])
  vi.spyOn(api, 'listAccessRequests').mockResolvedValue([])
  vi.spyOn(api, 'listApprovals').mockResolvedValue([])
}

describe('<Access>', () => {
  it('renders the requests panel and the agent grant group', async () => {
    seed()
    render(<MemoryRouter><Access /></MemoryRouter>)
    await screen.findByText('Запросы прав')
    expect(await screen.findByText('claude')).toBeInTheDocument()
    expect(screen.getByText('gitlab')).toBeInTheDocument()
    expect(screen.getByText('Выданные права')).toBeInTheDocument()
  })
  it('switches grouping to upstream', async () => {
    seed()
    render(<MemoryRouter><Access /></MemoryRouter>)
    await screen.findByText('claude')
    fireEvent.click(screen.getByText('По upstream'))
    await waitFor(() => expect(screen.getByText('gitlab')).toBeInTheDocument())
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm -C web exec vitest run src/pages/Access.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement GrantGroups then Access**

```tsx
// web/src/pages/access/GrantGroups.tsx
import type { Agent, Upstream } from '../../lib/types'
import type { Grant } from '../../lib/grants'
import { AgentCard } from './AgentCard'
import { UpstreamGrantCard } from './UpstreamGrantCard'

export function GrantGroups({
  grants, agents, upstreams, by, onChanged,
}: {
  grants: Grant[]
  agents: Agent[]
  upstreams: Upstream[]
  by: 'agent' | 'upstream'
  onChanged: () => void
}) {
  if (grants.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-card px-3 py-6 text-center text-xs text-muted-foreground">
        Прав ещё не выдано — действует запрет по умолчанию
      </div>
    )
  }
  if (by === 'agent') {
    const withGrants = agents.filter((a) => grants.some((g) => g.agentId === a.id))
    return (
      <div className="space-y-2">
        {withGrants.map((a) => (
          <AgentCard
            key={a.id}
            agent={a}
            grants={grants.filter((g) => g.agentId === a.id)}
            upstreams={upstreams}
            onChanged={onChanged}
          />
        ))}
      </div>
    )
  }
  // by upstream: one card per upstream, each grant rendered as an UpstreamGrantCard (agent-labelled)
  const byUp = new Map<string, Grant[]>()
  for (const g of grants) byUp.set(g.upstreamId, [...(byUp.get(g.upstreamId) ?? []), g])
  return (
    <div className="space-y-3">
      {[...byUp.entries()].map(([upId, gs]) => {
        const up = upstreams.find((u) => u.id === upId)
        return (
          <div key={upId} className="space-y-1.5">
            <div className="px-1 font-mono text-[13px] font-semibold">{up?.name ?? upId}</div>
            {gs.map((g) => {
              const agent = agents.find((a) => a.id === g.agentId)
              return (
                <div key={g.agentId}>
                  <div className="mb-1 px-1 text-[11px] text-muted-foreground">агент {agent?.name ?? g.agentId}</div>
                  <UpstreamGrantCard grant={g} upstream={up} onChanged={onChanged} />
                </div>
              )
            })}
          </div>
        )
      })}
    </div>
  )
}
```

```tsx
// web/src/pages/Access.tsx
import { useCallback, useEffect, useState } from 'react'
import { Plus } from 'lucide-react'
import {
  listRules, listAgents, listUpstreams, listApprovals, listAccessRequests, ApiError,
} from '../lib/api'
import type { Agent, Upstream, Rule, Approval, AccessRequest } from '../lib/types'
import { deriveGrants } from '../lib/grants'
import { useAccessGrouping } from '../lib/accessGrouping'
import { useEventStore } from '../lib/events'
import { useToastStore } from '../lib/toast'
import { RequestsPanel } from './access/RequestsPanel'
import { GrantGroups } from './access/GrantGroups'
import { ManualRuleModal } from './access/ManualRuleModal'

export function Access() {
  const [rules, setRules] = useState<Rule[]>([])
  const [agents, setAgents] = useState<Agent[]>([])
  const [upstreams, setUpstreams] = useState<Upstream[]>([])
  const [approvals, setApprovals] = useState<Approval[]>([])
  const [requests, setRequests] = useState<AccessRequest[]>([])
  const [manualOpen, setManualOpen] = useState(false)
  const by = useAccessGrouping((s) => s.by)
  const setBy = useAccessGrouping((s) => s.setBy)
  const push = useToastStore((s) => s.push)

  const counters = useEventStore((s) => s.counters)
  const refreshKey =
    (counters['rule.created'] ?? 0) + (counters['rule.updated'] ?? 0) +
    (counters['approval.enqueued'] ?? 0) + (counters['approval.resolved'] ?? 0) +
    (counters['access.requested'] ?? 0) + (counters['access.revoked'] ?? 0) +
    (counters['agent.registered'] ?? 0)

  const load = useCallback(() => {
    Promise.all([listRules(), listAgents(), listUpstreams(), listApprovals(), listAccessRequests()])
      .then(([r, a, u, ap, req]) => {
        setRules(r ?? []); setAgents(a ?? []); setUpstreams(u ?? [])
        setApprovals(ap ?? []); setRequests(req ?? [])
      })
      .catch((err) => push('error', err instanceof ApiError ? err.message : 'Failed to load access'))
  }, [push])

  useEffect(load, [load, refreshKey])

  const grants = deriveGrants(rules, requests)

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-lg font-semibold">Access</h1>

      <RequestsPanel approvals={approvals} onChanged={load} />

      <div className="flex items-center gap-3">
        <span className="text-[13px] font-semibold">Выданные права</span>
        <div className="inline-flex overflow-hidden rounded border border-border bg-card">
          <button onClick={() => setBy('agent')}
            className={`px-3 py-1 text-xs ${by === 'agent' ? 'bg-primary/15 text-primary' : 'text-muted-foreground'}`}>
            По агенту
          </button>
          <button onClick={() => setBy('upstream')}
            className={`px-3 py-1 text-xs ${by === 'upstream' ? 'bg-primary/15 text-primary' : 'text-muted-foreground'}`}>
            По upstream
          </button>
        </div>
        <button onClick={() => setManualOpen(true)}
          className="ml-auto inline-flex items-center gap-1.5 rounded bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:opacity-90">
          <Plus size={13} /> Выдать вручную
        </button>
      </div>

      <GrantGroups grants={grants} agents={agents} upstreams={upstreams} by={by} onChanged={load} />

      <ManualRuleModal open={manualOpen} onClose={() => setManualOpen(false)} onCreated={() => { setManualOpen(false); load() }} />
    </div>
  )
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `pnpm -C web exec vitest run src/pages/Access.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git checkout -- internal/daemon/webdist/index.html
git add web/src/pages/Access.tsx web/src/pages/access/GrantGroups.tsx web/src/pages/Access.test.tsx
git commit -m "feat(web): Access page — requests panel + grouped grants + manual grant"
```

---

## Task 12: Frontend — Audit "Запросы прав" tab

**Files:**
- Modify: `web/src/pages/Audit.tsx`
- Test: `web/src/pages/Audit.test.tsx` (append)

**Interfaces:**
- Consumes: `listAccessRequests`, `AccessRequest`, `useSearchParams` (react-router), `StatusBadge`, `DataTable`.
- Produces: a tabbed Audit page — `Трафик` (existing) and `Запросы прав` (read-only history table). Honours `?tab=requests&agent=<id>` to open the second tab filtered by agent.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/pages/Audit.test.tsx — append
import { MemoryRouter } from 'react-router'

it('shows the access-request history tab (read-only, all statuses)', async () => {
  vi.spyOn(api, 'listAudit').mockResolvedValue([])
  vi.spyOn(api, 'listAccessRequests').mockResolvedValue([
    { id: 'q1', agent_id: 'ag1', agent_name: 'claude', upstream_id: 'up1', upstream_name: 'gitlab',
      purpose: 'p', status: 'revoked', created_at: '2026-06-17T10:00:00Z', resolved_at: '2026-06-18T10:00:00Z' },
  ])
  render(<MemoryRouter initialEntries={['/audit?tab=requests']}><Audit /></MemoryRouter>)
  expect(await screen.findByText('claude')).toBeInTheDocument()
  expect(screen.getByText('revoked')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Revoke' })).not.toBeInTheDocument() // read-only
})
```

If the existing `Audit.test.tsx` renders `<Audit />` without a router, wrap those in `<MemoryRouter>` too (adding `useSearchParams` makes the router a dependency).

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm -C web exec vitest run src/pages/Audit.test.tsx`
Expected: FAIL — no "Запросы прав" tab / status not found.

- [ ] **Step 3: Implement the tabs**

Add a `tab` state seeded from `useSearchParams()` (`traffic` default; `requests` when `?tab=requests`). Render a two-button tab strip. Keep the existing traffic table under `tab === 'traffic'`. Under `tab === 'requests'` fetch `listAccessRequests()` and render a read-only `DataTable`:

```tsx
// columns: Agent, Upstream, Purpose, Status (+ deny reason), Requested (created_at), Resolved (resolved_at)
// filter rows by the ?agent= param when present. NO action column.
```

Use the same `fmtTime` and `StatusBadge` already in the file. The `agent` filter: `const agentFilter = searchParams.get('agent'); rows = agentFilter ? requests.filter(r => r.agent_id === agentFilter) : requests`.

- [ ] **Step 4: Run to verify it passes**

Run: `pnpm -C web exec vitest run src/pages/Audit.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git checkout -- internal/daemon/webdist/index.html
git add web/src/pages/Audit.tsx web/src/pages/Audit.test.tsx
git commit -m "feat(web): Audit — read-only Запросы прав tab with per-agent filter"
```

---

## Task 13: Frontend — nav + routes, delete old pages

**Files:**
- Modify: `web/src/components/Sidebar.tsx`, `web/src/App.tsx`, `web/src/lib/useOpenApprovalsRoute.ts`
- Delete: `web/src/pages/Agents.tsx`, `web/src/pages/Agents.test.tsx`, `web/src/pages/Rules.tsx`, `web/src/pages/Rules.test.tsx`, `web/src/pages/Approvals.tsx`, `web/src/pages/Approvals.test.tsx`
- Test: `web/src/components/Sidebar.test.tsx` (if present; else assert via App)

**Interfaces:** none new. The `desktop.open-approvals` signal now routes to `/access`.

- [ ] **Step 1: Update the nav**

In `web/src/components/Sidebar.tsx` `NAV`, remove the Agents, Operations (`/rules`), and Approvals entries and add:

```ts
import { LayoutDashboard, Server, KeyRound, ScrollText, Settings, Lock } from 'lucide-react'
// ...
{ to: '/access', label: 'Access', icon: KeyRound },
```

Final order: Dashboard · Upstreams · Access · Audit · Settings.

- [ ] **Step 2: Update routes in App.tsx**

Remove the `Agents`, `Rules`, `Approvals` imports and their `<Route>`s; add:

```tsx
import { Access } from './pages/Access'
// ...
<Route path="/access" element={<Access />} />
```

- [ ] **Step 3: Retarget the open-approvals deep link**

In `web/src/lib/useOpenApprovalsRoute.ts`, change the navigation target from `/approvals` to `/access`. Keep the counter-increase gating (verbatim logic; only the path string changes). Rename is optional; leave the filename.

- [ ] **Step 4: Delete the old pages + tests**

```bash
git rm web/src/pages/Agents.tsx web/src/pages/Agents.test.tsx \
       web/src/pages/Rules.tsx web/src/pages/Rules.test.tsx \
       web/src/pages/Approvals.tsx web/src/pages/Approvals.test.tsx
```

Grep for any dangling imports: `grep -rn "pages/Agents\|pages/Rules\|pages/Approvals" web/src` — fix each (there should be none after Steps 1–2). `ApprovalCards.tsx` (Task 5) already owns the card code, so deleting `Approvals.tsx` is safe.

- [ ] **Step 5: Run the web gate**

Run: `pnpm -C web exec tsc --noEmit && pnpm -C web exec vitest run && pnpm -C web run lint`
Expected: PASS — no missing modules, no unused imports (fix any eslint `no-unused-vars` from the deletions).

- [ ] **Step 6: Commit**

```bash
git checkout -- internal/daemon/webdist/index.html
git add -A web/src
git commit -m "feat(web): nav+routes → Access replaces Agents/Operations/Approvals"
```

---

## Task 14: Verify end-to-end + full gate

**Files:** none (verification + fixes only).

- [ ] **Step 1: Build**

Run: `make build`
Expected: builds `dist/bin/outwall`.

- [ ] **Step 2: Launch an isolated daemon + seed data**

Start an isolated daemon on nonstandard ports/paths (never touch the user's real outwall):

```bash
dist/bin/outwall serve \
  --ui-listen 127.0.0.1:18299 --listen 127.0.0.1:18199 \
  --socket /tmp/ow-x.sock --agent-socket /tmp/ow-x-agent.sock \
  --db /tmp/ow-x.db --callback-listen 127.0.0.1:18399 &
curl -sk --unix-socket /tmp/ow-x.sock -X POST http://x/vault/init -d '{"password":"test1234"}'
```

Seed an agent (POST `/register` to the agent socket, then `/whoami`), register an http upstream, create a rule with a non-`*` text value-set + a number range, and enqueue a pending request — via the admin socket handlers (mirror the existing manual-verification recipe in `.agents/*/state.md`).

- [ ] **Step 2b: Drive the UI with Playwright**

Point Playwright at `https://access.outwall.localhost:18299/` (or the UI listen) with the outwall CA / `ignoreHTTPSErrors`. Verify:
- The **Access** nav item exists; Agents/Operations/Approvals are gone.
- **Запросы прав** panel shows the pending request with a scope-badge hero + readable purpose; **Approve** moves it into a grant below.
- The grant shows under **claude** grouped by agent; toggling **По upstream** regroups.
- Expanding the rule shows text chips + number range (no enum); editing a chip persists.
- **Revoke** removes the grant; the trash icon deletes the agent (no modal).
- Header click toggles collapse; clicking the trash icon does not toggle.
- **Audit → Запросы прав** shows the request as `revoked` (read-only, no Revoke button).
Take a screenshot of the Access page.

- [ ] **Step 3: Kill the test daemon**

```bash
ss -ltnp | grep 18199   # find pid=
kill <pid>
```

- [ ] **Step 4: Full gate**

Run: `make check`
Expected: green (gofmt, vet, golangci-lint, go test, eslint, tsc, vitest, build).

- [ ] **Step 5: Commit any fixes**

```bash
git checkout -- internal/daemon/webdist/index.html
git add -A
git commit -m "test(web): Access e2e verification fixes"   # only if fixes were needed
```

---

## Task 15: Docs — ADR + module docs

**Files:**
- Create: `docs/architecture/decisions/ADR-00XX-access-page-grant-first-class.md` (next free number — check the directory)
- Modify: the relevant web-UI module doc under `docs/architecture/modules/` + `docs/INDEX.md`

- [ ] **Step 1: Write the ADR**

Use `docs/workflow/adr-template.md`. Record: the IA change (Operations+Approvals+Agents → Access; history → Audit tab); grant as the first-class object; Revoke relocated onto the grant and made grant-scoped (`POST /grants/revoke`, keyed by (agent,upstream), not a history row); enum value-set editing dropped; agent delete/meta migrated onto Access; `desktop.open-approvals` retargeted to `/access`.

- [ ] **Step 2: Update module docs + INDEX**

Update the web-UI module doc to describe the Access page + Audit tabs; delete stale references to the Operations/Approvals/Agents pages. Add the ADR to `docs/INDEX.md`.

- [ ] **Step 3: Commit**

```bash
git add docs/
git commit -m "docs(adr): Access page — grant as first-class object; pages merged"
```

---

## Self-review

**Spec coverage:**
- IA (remove Agents/Operations/Approvals, add Access; Audit history tab) → Tasks 12, 13. ✅
- Requests panel (aggregated pending, право-герой, readable purpose, time/agent in header) → Tasks 6, 6b. ✅
- Granted section (toggle agent/upstream + persistence, collapsible agent, upstream sub-card, rule rows, scope badges, value-set text/number, enum hidden, revoke grant, edit/delete rule) → Tasks 3,4,7,8,9,11. ✅
- Agent controls (meta, status, delete icon, kebab→Audit history) + header-click collapse → Task 9. ✅
- Manual "Выдать вручную" → Task 10. ✅
- Grant-scoped revoke (delete (agent,upstream) rules + mark requests revoked) → Tasks 1, 2. ✅
- Audit read-only history + per-agent filter → Task 12. ✅
- Docs/ADR → Task 15. ✅
- Verification (make check + Playwright) → Task 14. ✅

**Placeholder scan:** value-set editor bodies in Task 7 are ported verbatim from `Rules.tsx` (named files/functions given, not "similar to"); the Audit request-table columns in Task 12 are enumerated. No TBD/TODO. ✅

**Type consistency:** `Grant` (`agentId/upstreamId/rules/purpose/grantedAt`), `scopeOf → {label, kind}`, `revokeGrant(agentId, upstreamId)`, `deriveGrants(rules, requests)` are used identically across Tasks 3, 7, 8, 9, 11. `MarkRevokedBySubjectUpstream` (Task 1) is consumed only by Task 2. ✅

## Notes for the executor

- The web `req`/fetch helper name in `api.ts` — copy the exact pattern from the sibling `revokeAccessRequest`; don't assume `req`.
- Go daemon test harness helpers (`newTestDaemon`, `withOperatorSession`, seed fns) — grep the existing gated-handler tests and reuse; adapt Task 2's test to the real names before implementing.
- `--color-warning`, `--color-status-running` tokens are already used elsewhere (Audit, Sidebar) — safe to reuse.
- Keep every `aria-label` copied from `Rules.tsx`/`Agents.tsx` unchanged — tests query them.
