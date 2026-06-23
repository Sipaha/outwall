# Browse policy + upstream zones — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a coarse **browse rule** (`allow GET` by method-set × path-glob) so a browser can load a web app through outwall, a Citeck **ReadOnly** preset (browse + Records read), broaden the citeck Records path matcher, and split the Upstreams UI into **HTTP / Citeck / Kubernetes** tabs.

**Architecture:** A browse rule is a new raw-http rule shape (`BrowseMethods`, `BrowsePath`) matched by glob — distinct from, and coexisting with, the typed operation-template engine. The Citeck ReadOnly preset is a UI convenience that POSTs two ordinary rules (a browse rule + a citeck `op:read` profile rule). The Upstreams page becomes tabbed, filtering upstreams by `(kind, profile)` and folding the existing Clusters view into the Kubernetes tab.

**Tech Stack:** Go (CGO-free server), `modernc.org/sqlite`, `stretchr/testify`; React + Vite + Vitest + Testing Library.

**Companion ADR to write:** ADR-0036 (browse-rule primitive + Citeck ReadOnly preset + `recordsOp` broadening). Spec: `docs/superpowers/specs/2026-06-23-browse-policy-and-upstream-zones-design.md`. Builds on ADR-0034/0035.

## Global Constraints

- Module path exactly `github.com/Sipaha/outwall` in every import.
- **No `citeck`** in the core — only inside `internal/serverprofile/citeck` + the `"citeck"` profile-name value (ADR-0034). This plan touches policy/UI; keep them citeck-free (the UI's `'citeck'` comparison value is the allowed profile-name value).
- No CGO in the server binary (`CGO_ENABLED=0`). No panics in library code (wrapped errors). `log/slog`.
- Alpha: forward-only migrations — edit `schema` AND append a migration step; never edit/reorder a released step (`internal/store/migrate.go` doc comment).
- Commit author MUST be `Sipaha <sipahabk@gmail.com>`: `git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit`. No `Co-Authored-By`. No amend. Branch `main`.
- Gate before each commit: `gofmt -w .`, `go vet ./...`, `go test ./... -race`, `CGO_ENABLED=0 go build ./...`. Web tasks also `cd web && pnpm test && pnpm lint && pnpm build`, then `git checkout -- internal/daemon/webdist/index.html`.

## File structure (created / modified)

- **Modify** `internal/store/migrate.go` — `schema` rules block + a `browse_rules` migration (cols `browse_methods`, `browse_path`).
- **Modify** `internal/policy/rule.go` — `Rule.BrowseMethods` / `Rule.BrowsePath`.
- **Modify** `internal/policy/registry.go` — persist/scan the two columns.
- **Modify** `internal/policy/decide.go` — browse-match branch + `methodMatch` helper.
- **Modify** `internal/serverprofile/citeck/records.go` — `recordsOp` suffix broadened to `/records/{op}`.
- **Modify** `internal/daemon/admin.go` — control-API carries `browse_methods`/`browse_path`.
- **Modify** `web/src/lib/types.ts`, `web/src/lib/api.ts` — `browse_methods`/`browse_path` on `Rule`.
- **Modify** `web/src/pages/Rules.tsx` (+ test) — visible "Browse rules" section.
- **Create** `web/src/components/Tabs.tsx` (+ used in Upstreams) — reusable tabs.
- **Modify** `web/src/pages/Upstreams.tsx` (+ test) — HTTP/Citeck/Kubernetes tabs; preset buttons; embed Clusters in the Kubernetes tab.
- **Modify** `web/src/App.tsx`, `web/src/components/Sidebar.tsx` — drop the separate `/clusters` nav (folded into Upstreams tabs); rename "Hosts" → "Upstreams".
- **Create** `docs/architecture/decisions/0036-browse-rule-and-readonly-preset.md`; **modify** `docs/INDEX.md`.

---

### Task 1: storage — `browse_methods` + `browse_path` columns on rules

**Files:**
- Modify: `internal/store/migrate.go` (`rules` CREATE in `schema` ~30-45; `migrations` list ~115)
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces: columns `rules.browse_methods TEXT NOT NULL DEFAULT ''`, `rules.browse_path TEXT NOT NULL DEFAULT ''`; schema version bumped by one.

- [ ] **Step 1: Write the failing test**

```go
func TestBrowseRuleColumns(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	require.NoError(t, err)
	defer s.Close()
	_, err = s.DB().Exec(`SELECT browse_methods, browse_path FROM rules LIMIT 0`)
	require.NoError(t, err)
	require.Equal(t, len(migrations), userVersion(t, s))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestBrowseRuleColumns -v`
Expected: FAIL — `no such column: browse_methods`.

- [ ] **Step 3: Write minimal implementation**

In the `rules` CREATE inside `schema`, add the two columns after `profile_params` (before `created_at`):

```sql
	profile_params     TEXT NOT NULL DEFAULT '{}',
	browse_methods     TEXT NOT NULL DEFAULT '',
	browse_path        TEXT NOT NULL DEFAULT '',
	created_at         TEXT NOT NULL
);
```

Append a migration step to `migrations` (after the last one):

```go
	{"browse_rules", func(tx *sql.Tx) error {
		for _, stmt := range []string{
			`ALTER TABLE rules ADD COLUMN browse_methods TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE rules ADD COLUMN browse_path TEXT NOT NULL DEFAULT ''`,
		} {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("browse_rules: %w", err)
			}
		}
		return nil
	}},
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS (all store tests).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/store/
git add internal/store/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(store): add browse_methods/browse_path columns on rules"
```

---

### Task 2: policy.Rule browse fields + persistence

**Files:**
- Modify: `internal/policy/rule.go` (struct ~24-43)
- Modify: `internal/policy/registry.go` (`Create` INSERT ~48-55; `ruleCols` ~221; `scanRows` ~193-216)
- Test: `internal/policy/registry_test.go`

**Interfaces:**
- Consumes: storage columns (Task 1).
- Produces: `Rule.BrowseMethods string`, `Rule.BrowsePath string`; persisted to/from `browse_methods`/`browse_path`.

- [ ] **Step 1: Write the failing test**

```go
func TestBrowseRuleRoundTrip(t *testing.T) {
	reg := newReg(t)
	_, err := reg.Create(Rule{UpstreamID: "u1", Outcome: Allow, BrowseMethods: "GET,HEAD", BrowsePath: "/**"})
	require.NoError(t, err)
	got, err := reg.ForUpstream("u1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "GET,HEAD", got[0].BrowseMethods)
	require.Equal(t, "/**", got[0].BrowsePath)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/policy/ -run TestBrowseRuleRoundTrip -v`
Expected: FAIL — `BrowseMethods`/`BrowsePath` undefined.

- [ ] **Step 3: Write minimal implementation**

Add to `Rule` (after the profile fields):

```go
	// Browse rule (raw-http coarse matcher; empty on template/k8s/profile rules): when BrowsePath
	// is non-empty the rule matches by method-set + path-glob instead of an operation template.
	BrowseMethods string // comma-separated, e.g. "GET,HEAD"; "" or "*" = any method
	BrowsePath    string // path glob, e.g. "/**"; non-empty marks this a browse rule
```

In `registry.go` `Create`, extend the INSERT to 18 columns (add `browse_methods, browse_path` before `created_at`):

```go
	_, err = r.store.DB().Exec(
		`INSERT INTO rules (id, subject_agent_id, upstream_id, op_method, op_path_template, op_query_template, op_body_template, op_value_policies, outcome, rate_limit_per_min, k8s_namespace, k8s_resource, k8s_verb, profile, profile_params, browse_methods, browse_path, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.SubjectAgentID, in.UpstreamID, in.OpMethod, in.OpPathTemplate, queryJSON, bodyJSON, policiesJSON,
		in.Outcome, in.RateLimitPerMin,
		in.Namespace, in.Resource, in.Verb,
		in.Profile, string(params),
		in.BrowseMethods, in.BrowsePath,
		in.CreatedAt.Format(time.RFC3339Nano),
	)
```

Extend `ruleCols`:

```go
const ruleCols = `id, subject_agent_id, upstream_id, op_method, op_path_template, op_query_template, op_body_template, op_value_policies, outcome, rate_limit_per_min, k8s_namespace, k8s_resource, k8s_verb, profile, profile_params, browse_methods, browse_path, created_at`
```

In `scanRows`, scan the two new columns (between `profileParam` and `created`):

```go
		if err := rows.Scan(&rule.ID, &rule.SubjectAgentID, &rule.UpstreamID,
			&rule.OpMethod, &rule.OpPathTemplate, &queryJSON, &bodyJSON, &policiesJSON,
			&rule.Outcome, &rule.RateLimitPerMin,
			&rule.Namespace, &rule.Resource, &rule.Verb,
			&rule.Profile, &profileParam, &rule.BrowseMethods, &rule.BrowsePath, &created); err != nil {
			return nil, err
		}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/policy/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/policy/
git add internal/policy/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(policy): browse rule fields on Rule + persistence"
```

---

### Task 3: policy engine — browse-match branch

**Files:**
- Modify: `internal/policy/decide.go` (raw-http loop ~124-145; add `methodMatch`)
- Test: `internal/policy/decide_test.go` (new `decide_browse_test.go`, `package policy`)

**Interfaces:**
- Consumes: `Rule.BrowseMethods`/`BrowsePath` (Task 2), `MatchGlob` (rule.go).
- Produces: a request whose path matches a browse rule's glob and whose method is in the rule's method-set yields the rule's outcome; coexists with operation-template rules; profile rules still skipped.

- [ ] **Step 1: Write the failing test**

```go
package policy

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecideBrowseRuleAllowsGetAnyPath(t *testing.T) {
	reg := newReg(t)
	mk(t, reg, Rule{UpstreamID: "u1", Outcome: Allow, BrowseMethods: "GET,HEAD", BrowsePath: "/**"})

	// GET on any path → allow.
	d, err := reg.Decide(Input{UpstreamID: "u1", Method: "GET", Path: "/static/app.js", Query: url.Values{}})
	require.NoError(t, err)
	require.Equal(t, Allow, d.Outcome)

	// POST is not in the method set → no browse match → default-deny.
	d, err = reg.Decide(Input{UpstreamID: "u1", Method: "POST", Path: "/x", Query: url.Values{}})
	require.NoError(t, err)
	require.Equal(t, Deny, d.Outcome)
}

func TestDecideBrowseAndOperationCoexist(t *testing.T) {
	reg := newReg(t)
	mk(t, reg, Rule{UpstreamID: "u2", Outcome: Allow, BrowseMethods: "GET", BrowsePath: "/**"})
	mk(t, reg, Rule{UpstreamID: "u2", Outcome: Allow, OpMethod: "POST", OpPathTemplate: "/api/echo",
		OpValuePolicies: map[string]ValuePolicy{}})
	// browse covers a GET asset…
	d, _ := reg.Decide(Input{UpstreamID: "u2", Method: "GET", Path: "/page", Query: url.Values{}})
	require.Equal(t, Allow, d.Outcome)
	// …and the operation rule covers the POST API.
	d, _ = reg.Decide(Input{UpstreamID: "u2", Method: "POST", Path: "/api/echo", Query: url.Values{}, Body: []byte("{}")})
	require.Equal(t, Allow, d.Outcome)
}
```

> `mk(t, reg, Rule)` is the existing decide-test helper (`decide_test.go`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/policy/ -run TestDecideBrowse -v`
Expected: FAIL — browse GET denied (no branch yet).

- [ ] **Step 3: Write minimal implementation**

In `decide.go`, change the raw-http `else` branch to handle browse rules first. Replace:

```go
		} else {
			cc, matched, herr := r.evalHTTPRule(rule, in)
			if herr != nil {
				return Decision{}, herr
			}
			if !matched {
				continue
			}
			c = cc
		}
```

with:

```go
		} else if rule.BrowsePath != "" {
			if !methodMatch(rule.BrowseMethods, in.Method) || !MatchGlob(rule.BrowsePath, in.Path) {
				continue
			}
			c = candidate{rule: rule, outcome: rule.Outcome}
		} else {
			cc, matched, herr := r.evalHTTPRule(rule, in)
			if herr != nil {
				return Decision{}, herr
			}
			if !matched {
				continue
			}
			c = cc
		}
```

Add the helper (in `decide.go`):

```go
// methodMatch reports whether m is allowed by a browse rule's method set: "" or a "*" token matches
// any method; otherwise a case-insensitive membership test against the comma-separated set.
func methodMatch(set, m string) bool {
	if set == "" {
		return true
	}
	for _, tok := range strings.Split(set, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "*" || strings.EqualFold(tok, m) {
			return true
		}
	}
	return false
}
```

(`strings` is already imported in `decide.go`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/policy/ -race -v`
Expected: PASS (existing tests unaffected — they set no `BrowsePath`).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/policy/
git add internal/policy/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(policy): browse-rule matching (method-set x path-glob)"
```

---

### Task 4: citeck — broaden recordsOp to `/records/{op}`

**Files:**
- Modify: `internal/serverprofile/citeck/records.go` (`recordsOp`)
- Test: `internal/serverprofile/citeck/records_test.go`

**Interfaces:**
- Produces: `recordsOp` matches the suffix `/records/{query,mutate,delete}` (covers `/api/records/query`, `/gateway/records/query`, `/gateway/api/records/query`).

- [ ] **Step 1: Write the failing test**

```go
func TestRecordsOpPathVariants(t *testing.T) {
	for _, p := range []string{
		"/api/records/query",
		"/gateway/records/query",
		"/gateway/emodel/api/records/query",
	} {
		op, ok := recordsOp(p)
		if !ok || op != "query" {
			t.Fatalf("recordsOp(%q) = (%q,%v), want (query,true)", p, op, ok)
		}
	}
	if _, ok := recordsOp("/gateway/observer/events"); ok {
		t.Fatalf("non-records path must not match")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serverprofile/citeck/ -run TestRecordsOpPathVariants -v`
Expected: FAIL — `/gateway/records/query` not matched.

- [ ] **Step 3: Write minimal implementation**

```go
func recordsOp(path string) (string, bool) {
	for _, op := range []string{"query", "mutate", "delete"} {
		if strings.HasSuffix(path, "/records/"+op) {
			return op, true
		}
	}
	return "", false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/serverprofile/citeck/ -v`
Expected: PASS (existing classify tests still pass — their paths end in `/records/query` etc.).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/serverprofile/citeck/
git add internal/serverprofile/citeck/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(citeck): match Records path by /records/{op} suffix (gateway variants)"
```

---

### Task 5: control-API — browse fields on rule create/list

**Files:**
- Modify: `internal/daemon/admin.go` (`hRuleCreate` ~396-431; `hRuleList` ~433-451)
- Test: `internal/daemon/admin_test.go`

**Interfaces:**
- Consumes: `Rule.BrowseMethods`/`BrowsePath` (Task 2).
- Produces: `POST /rules` accepts `browse_methods`/`browse_path`; `GET /rules` returns them.

- [ ] **Step 1: Write the failing test**

```go
func TestRuleCreateWithBrowseFields(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, 200, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	require.Equal(t, 200, req(t, h, "POST", "/rules",
		`{"upstream_id":"u1","outcome":"allow","browse_methods":"GET,HEAD","browse_path":"/**"}`).Code)
	body := req(t, h, "GET", "/rules", "").Body.String()
	require.Contains(t, body, `"browse_path":"/**"`)
	require.Contains(t, body, `"browse_methods":"GET,HEAD"`)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run TestRuleCreateWithBrowseFields -v`
Expected: FAIL — fields not decoded/returned.

- [ ] **Step 3: Write minimal implementation**

In `hRuleCreate`, add to the body struct and the `policy.Rule{...}`:

```go
		Profile       string          `json:"profile"`
		ProfileParams json.RawMessage `json:"profile_params"`
		BrowseMethods string          `json:"browse_methods"`
		BrowsePath    string          `json:"browse_path"`
```
```go
	rule, err := d.policy.Create(policy.Rule{
		// …existing fields…
		Profile:       body.Profile,
		ProfileParams: body.ProfileParams,
		BrowseMethods: body.BrowseMethods,
		BrowsePath:    body.BrowsePath,
	})
```

In `hRuleList`, add to the per-rule map:

```go
			"browse_methods": rule.BrowseMethods,
			"browse_path":    rule.BrowsePath,
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/daemon/ -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/daemon/
git add internal/daemon/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(controlapi): browse_methods/browse_path on rule create+list"
```

---

### Task 6: web — types + Browse-rules visibility in the Rules screen

**Files:**
- Modify: `web/src/lib/types.ts` (`Rule` interface)
- Modify: `web/src/pages/Rules.tsx` (filters + a new Browse-rules section)
- Test: `web/src/pages/Rules.test.tsx`

**Interfaces:**
- Consumes: `GET /rules` `browse_methods`/`browse_path` (Task 5).
- Produces: `Rule.browse_methods?`, `Rule.browse_path?`; a visible+deletable "Browse rules" section for rules where `browse_path` is set.

- [ ] **Step 1: Write the failing test**

```ts
it('shows a browse rule in its own section', async () => {
  vi.spyOn(api, 'listUpstreams').mockResolvedValue([
    { id: 'up', name: 'be', base_url: 'https://be.test', auth_type: 'none' },
  ])
  vi.spyOn(api, 'listRules').mockResolvedValue([
    { id: 'b1', upstream_id: 'up', subject_agent_id: '', outcome: 'allow', browse_methods: 'GET,HEAD', browse_path: '/**' },
  ])
  vi.spyOn(api, 'listAgents').mockResolvedValue([])
  render(<Rules />)
  expect(await screen.findByText('/**')).toBeInTheDocument()
  expect(screen.getByText(/GET,HEAD/)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && pnpm test -- Rules`
Expected: FAIL — browse rule not rendered.

- [ ] **Step 3: Write minimal implementation**

In `types.ts`, add to `Rule`: `browse_methods?: string` and `browse_path?: string`.

In `Rules.tsx`: add `const isBrowseRule = (r: Rule) => !!r.browse_path`. Exclude browse rules from the operation and k8s filters (add `&& !isBrowseRule(r)` to each, mirroring how `isProfileRule` is excluded). Compute `const browseRules = rules.filter(isBrowseRule)`. Render a new section (only when `browseRules.length > 0`) titled "Browse rules", each row showing the host (`upstreamName(r.upstream_id)`), subject (`agentName(r.subject_agent_id)`), `r.browse_methods` (or "any"), `r.browse_path`, the outcome, and a Delete button wired to the same `setConfirmDelete(r)`/`deleteRule` flow the other sections use.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && pnpm test -- Rules && pnpm lint && pnpm build && cd ..` then `git checkout -- internal/daemon/webdist/index.html`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/types.ts web/src/pages/Rules.tsx web/src/pages/Rules.test.tsx
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(web): browse rule types + visible Browse-rules section"
```

---

### Task 7: web — reusable Tabs component + tabbed Upstreams page

**Files:**
- Create: `web/src/components/Tabs.tsx`
- Modify: `web/src/pages/Upstreams.tsx` (wrap content in tabs; embed Clusters in the Kubernetes tab)
- Modify: `web/src/App.tsx` (drop `/clusters` route), `web/src/components/Sidebar.tsx` (drop the Clusters nav entry; rename "Hosts" → "Upstreams")
- Test: `web/src/pages/Upstreams.test.tsx`

**Interfaces:**
- Consumes: `getProfiles`, `listUpstreams` (each `Upstream` has `kind?`/`profile?`).
- Produces: a `Tabs` component; the Upstreams page renders three tabs — **HTTP** (`kind!=='k8s' && profile!=='citeck'`), **Citeck** (`profile==='citeck'`), **Kubernetes** (renders `<Clusters/>`). The HTTP/Citeck tabs show only their filtered upstream rows.

- [ ] **Step 1: Write the failing test**

```ts
it('filters upstreams into HTTP / Citeck tabs', async () => {
  vi.spyOn(api, 'getProfiles').mockResolvedValue([{ profile: 'citeck', fields: [] }])
  vi.spyOn(api, 'listUpstreams').mockResolvedValue([
    { id: 'h', name: 'plain.test', base_url: 'https://plain.test', auth_type: 'none', kind: 'http', profile: 'raw-http' },
    { id: 'c', name: 'cite.test', base_url: 'https://cite.test', auth_type: 'none', kind: 'http', profile: 'citeck' },
  ])
  render(<Upstreams />)
  // HTTP tab (default): plain shown, citeck not.
  expect(await screen.findByText('plain.test')).toBeInTheDocument()
  expect(screen.queryByText('cite.test')).not.toBeInTheDocument()
  // Switch to Citeck tab.
  fireEvent.click(screen.getByRole('tab', { name: /citeck/i }))
  expect(await screen.findByText('cite.test')).toBeInTheDocument()
  expect(screen.queryByText('plain.test')).not.toBeInTheDocument()
})
```

> Mock `getProfiles` and any other API the page loads (Clusters may call `listAgents`/`importClusters`-related loaders only when the Kubernetes tab renders, so default-tab HTTP keeps them out of the way; if Clusters mounts eagerly, mock its loaders too).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && pnpm test -- Upstreams`
Expected: FAIL — no tabs / both rows shown.

- [ ] **Step 3: Write minimal implementation**

Create `web/src/components/Tabs.tsx`:

```tsx
interface Tab { id: string; label: string }

export function Tabs({ tabs, active, onChange }: { tabs: Tab[]; active: string; onChange: (id: string) => void }) {
  return (
    <div role="tablist" className="flex gap-1 border-b border-border">
      {tabs.map((t) => (
        <button
          key={t.id}
          role="tab"
          aria-selected={active === t.id}
          onClick={() => onChange(t.id)}
          className={
            'px-3 py-1.5 text-sm ' +
            (active === t.id ? 'border-b-2 border-primary text-foreground' : 'text-muted-foreground')
          }
        >
          {t.label}
        </button>
      ))}
    </div>
  )
}
```

In `Upstreams.tsx`: add `const [tab, setTab] = useState('http')`; render `<Tabs tabs={[{id:'http',label:'HTTP'},{id:'citeck',label:'Citeck'},{id:'k8s',label:'Kubernetes'}]} active={tab} onChange={setTab} />`. Filter the upstream rows by tab: HTTP → `u.kind !== 'k8s' && u.profile !== 'citeck'`; Citeck → `u.profile === 'citeck'`. When `tab === 'k8s'`, render `<Clusters/>` (import it) instead of the host table. Keep the add-host form on the HTTP/Citeck tabs.

In `App.tsx`: remove the `<Route path="/clusters" .../>` line (the Kubernetes content now lives under `/upstreams`). In `Sidebar.tsx`: remove the `{ to: '/clusters', label: 'Clusters', ... }` NAV entry and rename the `/upstreams` label from `'Hosts'` to `'Upstreams'`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && pnpm test && pnpm lint && pnpm build && cd ..` then `git checkout -- internal/daemon/webdist/index.html`
Expected: PASS (Clusters' own tests still pass; it's now reachable via the tab).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/Tabs.tsx web/src/pages/Upstreams.tsx web/src/pages/Upstreams.test.tsx web/src/App.tsx web/src/components/Sidebar.tsx
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(web): HTTP/Citeck/Kubernetes tabs on the Upstreams page"
```

---

### Task 8: web — "Allow GET (browse)" + Citeck "ReadOnly" preset buttons

**Files:**
- Modify: `web/src/pages/Upstreams.tsx` (per-host action in the HTTP and Citeck tabs)
- Test: `web/src/pages/Upstreams.test.tsx`

**Interfaces:**
- Consumes: `createRule` (already posts the whole rule object, now with browse/profile fields).
- Produces: an **"Allow GET"** action per HTTP-tab host that POSTs one browse rule; a **"ReadOnly"** action per Citeck-tab host that POSTs a browse rule + a citeck `op:read` rule.

- [ ] **Step 1: Write the failing test**

```ts
it('Allow GET posts a browse rule for an http host', async () => {
  vi.spyOn(api, 'getProfiles').mockResolvedValue([])
  vi.spyOn(api, 'listUpstreams').mockResolvedValue([
    { id: 'h', name: 'plain.test', base_url: 'https://plain.test', auth_type: 'none', kind: 'http', profile: 'raw-http' },
  ])
  const createSpy = vi.spyOn(api, 'createRule').mockResolvedValue({ id: 'r1' })
  render(<Upstreams />)
  fireEvent.click(await screen.findByRole('button', { name: /allow get/i }))
  await waitFor(() =>
    expect(createSpy).toHaveBeenCalledWith(
      expect.objectContaining({ upstream_id: 'h', outcome: 'allow', browse_methods: 'GET,HEAD', browse_path: '/**' }),
    ),
  )
})

it('ReadOnly posts a browse rule and a citeck read rule for a citeck host', async () => {
  vi.spyOn(api, 'getProfiles').mockResolvedValue([{ profile: 'citeck', fields: [] }])
  vi.spyOn(api, 'listUpstreams').mockResolvedValue([
    { id: 'c', name: 'cite.test', base_url: 'https://cite.test', auth_type: 'none', kind: 'http', profile: 'citeck' },
  ])
  const createSpy = vi.spyOn(api, 'createRule').mockResolvedValue({ id: 'r' })
  render(<Upstreams />)
  fireEvent.click(screen.getByRole('tab', { name: /citeck/i }))
  fireEvent.click(await screen.findByRole('button', { name: /read.?only/i }))
  await waitFor(() => expect(createSpy).toHaveBeenCalledTimes(2))
  expect(createSpy).toHaveBeenCalledWith(
    expect.objectContaining({ upstream_id: 'c', outcome: 'allow', browse_methods: 'GET,HEAD', browse_path: '/**' }),
  )
  expect(createSpy).toHaveBeenCalledWith(
    expect.objectContaining({ upstream_id: 'c', outcome: 'allow', profile: 'citeck', profile_params: { op: 'read', source_id: '*', workspace: '*' } }),
  )
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && pnpm test -- Upstreams`
Expected: FAIL — no Allow GET / ReadOnly buttons.

- [ ] **Step 3: Write minimal implementation**

Import `createRule` from `../lib/api`. Add per-row actions:

```ts
async function allowGet(u: Upstream) {
  await createRule({ subject_agent_id: '', upstream_id: u.id, outcome: 'allow', browse_methods: 'GET,HEAD', browse_path: '/**' })
}
async function readOnly(u: Upstream) {
  await createRule({ subject_agent_id: '', upstream_id: u.id, outcome: 'allow', browse_methods: 'GET,HEAD', browse_path: '/**' })
  await createRule({ subject_agent_id: '', upstream_id: u.id, outcome: 'allow', profile: 'citeck', profile_params: { op: 'read', source_id: '*', workspace: '*' } })
}
```

In the host-table columns, add an actions cell: on the HTTP tab a button **"Allow GET"** → `allowGet(u)`; on the Citeck tab a button **"ReadOnly"** → `readOnly(u)`. (Pass the active `tab` into the column render, or render two column variants per tab.) Surface success/failure via the existing toast pattern if the page uses one. The subject is `''` (any agent) for v1 — these are host-wide browse grants.

> If `createRule`'s TS type rejects the extra fields, ensure `Rule` permits `browse_methods`/`browse_path`/`profile`/`profile_params` (Task 6 + earlier work already added them); cast the payload to the create type the existing code uses.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && pnpm test && pnpm lint && pnpm build && cd ..` then `git checkout -- internal/daemon/webdist/index.html`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/Upstreams.tsx web/src/pages/Upstreams.test.tsx
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(web): Allow-GET (HTTP) + ReadOnly (Citeck) preset actions"
```

---

### Task 9: full gate + ADR-0036 + live verification

**Files:**
- Create: `docs/architecture/decisions/0036-browse-rule-and-readonly-preset.md`
- Modify: `docs/INDEX.md`

- [ ] **Step 1: Run the full gate**

```bash
gofmt -w . && go vet ./... && go test ./... -race && CGO_ENABLED=0 go build ./...
go build -tags desktop -o /tmp/owd ./cmd/outwall-desktop
cd web && pnpm test && pnpm lint && pnpm build && cd ..
git checkout -- internal/daemon/webdist/index.html
```
Expected: all green.

- [ ] **Step 2: Write ADR-0036**

Create `docs/architecture/decisions/0036-browse-rule-and-readonly-preset.md` (status accepted, date 2026-06-23). Cover: the browse-rule primitive (`BrowseMethods` × `BrowsePath`, glob-matched, coexists with operation templates, evaluated on the raw-http path so it serves a Citeck upstream's non-Records paths too); the Citeck ReadOnly preset = browse rule + citeck `op:read` rule (UI composes two ordinary rules; no backend preset endpoint); the `recordsOp` broadening to `/records/{op}` and why (gateway path variants, safe because a non-Records path ending in `/records/query` is implausible); the UI zoning into HTTP/Citeck/Kubernetes tabs. Note the residual: browse rules are coarse (allow GET on a path glob) by design — fine-grained control still uses operation templates.

- [ ] **Step 3: Link ADR in INDEX**

Add the ADR-0036 line to the `decisions/` list in `docs/INDEX.md`.

- [ ] **Step 4: Commit + push**

```bash
git add docs/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "docs(adr-0036): browse rule + Citeck ReadOnly preset + records-path broadening"
git push origin main
```

- [ ] **Step 5: Live verification (operator-assisted; record the real result)**

With a rebuilt app (`make run`): on the Upstreams **Citeck** tab, click **ReadOnly** for `enterprise.ecos24.ru` (creates the two allow rules). Then drive Playwright through outwall — a context with `ignoreHTTPSErrors: true`, the `outwall_token` cookie set for `https://enterprise.ecos24.ru.outwall.localhost:<port>`, navigate there — and confirm the app renders. Capture the network log and record the **actual Records request path** the app POSTs to; confirm `recordsOp` matches it (it should, given the `/records/{op}` suffix) and the read-query returns 200 through outwall. If a path slips the matcher or a non-GET/non-Records call is needed, record it as a follow-up. (This step is operator-driven — the daemon can't approve rules or drive the operator's browser; surface the result to the user.)

---

## Self-review notes

- **Spec coverage:** Part 1 (browse rule) → Tasks 1-3, 5; Part 2 (ReadOnly preset) → Tasks 6, 8; Part 3 (recordsOp) → Task 4 + Task 9 live capture; Part 4 (tabs) → Task 7; visibility of browse rules → Task 6; ADR/docs/gate → Task 9.
- **Non-breaking:** browse fields are additive; existing operation/k8s/profile rules set no `BrowsePath` so the engine and UI are unchanged for them. Every Go task keeps the full suite green; every web task reverts `webdist/index.html`.
- **Naming consistency:** `BrowseMethods`/`BrowsePath` (Go) ↔ `browse_methods`/`browse_path` (JSON/TS); `methodMatch`; `recordsOp` suffix `/records/{op}`; preset payloads `{browse_methods:'GET,HEAD', browse_path:'/**'}` and `{profile:'citeck', profile_params:{op:'read', source_id:'*', workspace:'*'}}` — identical across policy, control-API, and web tasks.
- **Placeholder scan:** none — every step has concrete code/commands.
</content>
