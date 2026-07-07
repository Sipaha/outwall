# Grant TTL (expiry) and renewal — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the operator hand out time-limited grants (a per-rule expiry) from both the approval card and the manual-grant modal, enforce expiry (an expired rule stops granting), and mark/renew expired rules in the UI instead of deleting them.

**Architecture:** Expiry is a new `expires_at` column on `rules` (RFC3339Nano; `''` = never), mirrored as `policy.Rule.ExpiresAt time.Time` (zero = never). It is enforced in exactly one place — `policy.Registry.Decide` filters expired rules right after loading, so both the raw and server-profile decision paths see only live rules; `ForUpstream`/`List` stay unfiltered so the UI can show and renew expired rules. The operator picks a duration (a `ttl_seconds` integer, `0` = never); the daemon computes `expires_at = now + ttl` server-side and stamps it on every rule an approval/manual-create makes. A new `POST /rules/{id}/renew` resets a rule's expiry. The web adds a reusable `DurationSelect` and expiry chips/badges on rule rows and grant cards.

**Tech Stack:** Go (`modernc.org/sqlite`, `net/http`, `log/slog`, `stretchr/testify`), React + TypeScript + Vite + Tailwind, Vitest.

## Global Constraints

- Module path is exactly `github.com/Sipaha/outwall` in every import.
- No `citeck` strings/imports in core; the sanctioned exception is `internal/serverprofile/citeck` only. This feature adds nothing citeck-specific.
- No CGO in the server binary (`CGO_ENABLED=0`); SQLite via `modernc.org/sqlite`.
- No panics / `log.Fatal` in library code — return wrapped `error` (`%w`).
- New deps (if any) at `@latest`; do NOT bump existing deps.
- Alpha: no released-format compat to preserve; the additive column + migration is the only schema change.
- TDD: failing test → minimal code → green → commit. `gofmt` (tabs), `go vet` clean.
- Commit messages: no `Co-Authored-By`; never `git commit --amend`.
- Before any commit that touches Go: `make fmt && make vet && make test`. Web tasks: `make lint-web && make test-web`. Do NOT commit `internal/daemon/webdist/index.html` churn — if `build-web` ran, reset it: `git checkout -- internal/daemon/webdist/index.html` (its committed form is an inert placeholder).
- Duration options (label → seconds), single source of truth: `1 час→3600`, `2 часа→7200`, `8 часов→28800`, `24 часа→86400`, `2 дня→172800`, `7 дней→604800`, `1 месяц→2592000` (30d), `1 год→31536000` (365d), `Бессрочно→0`. Default = `3600` (1 час).

---

## Task 1: Store column + `policy.Rule.ExpiresAt` round-trip

**Files:**
- Modify: `internal/store/migrate.go` (schema `rules` block; append migration step)
- Modify: `internal/policy/rule.go` (add `ExpiresAt` field)
- Modify: `internal/policy/registry.go` (`insertRule` INSERT; `scanRows`; `ruleCols`)
- Test: `internal/store/store_test.go` (migration), `internal/policy/registry_test.go` (round-trip)

**Interfaces:**
- Produces: `policy.Rule.ExpiresAt time.Time` (zero = never), persisted by `insertRule`/`CreateMany`/`Create` and read by `scanRows`/`List`/`ForUpstream`. Column `rules.expires_at TEXT NOT NULL DEFAULT ''`.

- [ ] **Step 1: Write the failing policy round-trip test**

In `internal/policy/registry_test.go` add (reuse the file's existing `newRegistry(t)` / store-open helper — match the pattern already there):

```go
func TestRuleExpiresAtRoundTrip(t *testing.T) {
	r := newRegistry(t)
	exp := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Nanosecond)
	out, err := r.Create(policy.Rule{UpstreamID: "u1", Outcome: policy.Allow, BrowsePath: "/**", ExpiresAt: exp})
	require.NoError(t, err)
	require.WithinDuration(t, exp, out.ExpiresAt, time.Millisecond)

	got, err := r.ForUpstream("u1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.WithinDuration(t, exp, got[0].ExpiresAt, time.Millisecond)

	// Zero value persists as "" and reads back zero (never expires).
	out2, err := r.Create(policy.Rule{UpstreamID: "u2", Outcome: policy.Allow, BrowsePath: "/**"})
	require.NoError(t, err)
	require.True(t, out2.ExpiresAt.IsZero())
	got2, _ := r.ForUpstream("u2")
	require.True(t, got2[0].ExpiresAt.IsZero())
}
```

If `newRegistry`/import names differ, adapt to the existing test helpers in that file (do NOT invent a new harness).

- [ ] **Step 2: Run it — expect FAIL** (unknown field `ExpiresAt`)

Run: `go test ./internal/policy/ -run TestRuleExpiresAtRoundTrip`
Expected: build error `unknown field 'ExpiresAt'`.

- [ ] **Step 3: Add the field** to `internal/policy/rule.go`, in the `Rule` struct right after `CreatedAt       time.Time`:

```go
	// ExpiresAt is the grant expiry: the zero value means never expires; otherwise Decide treats
	// the rule as absent once now >= ExpiresAt (see ADR-0045). Persisted as RFC3339Nano ("" = never).
	ExpiresAt time.Time
```

- [ ] **Step 4: Persist + read it.** In `internal/policy/registry.go`:

`ruleCols` — append `, expires_at`:
```go
const ruleCols = `id, subject_agent_id, upstream_id, op_method, op_path_template, op_query_template, op_body_template, op_value_policies, outcome, rate_limit_per_min, k8s_namespace, k8s_resource, k8s_verb, profile, profile_params, browse_methods, browse_path, created_at, expires_at`
```

`insertRule` — add the column to the INSERT (19 columns / 19 placeholders) and a formatted value. Replace the INSERT block:
```go
	in.ID = newID()
	in.CreatedAt = time.Now().UTC()
	expiresStr := ""
	if !in.ExpiresAt.IsZero() {
		expiresStr = in.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	_, err = exec.Exec(
		`INSERT INTO rules (id, subject_agent_id, upstream_id, op_method, op_path_template, op_query_template, op_body_template, op_value_policies, outcome, rate_limit_per_min, k8s_namespace, k8s_resource, k8s_verb, profile, profile_params, browse_methods, browse_path, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.SubjectAgentID, in.UpstreamID, in.OpMethod, in.OpPathTemplate, queryJSON, bodyJSON, policiesJSON,
		in.Outcome, in.RateLimitPerMin,
		in.Namespace, in.Resource, in.Verb,
		in.Profile, string(params),
		in.BrowseMethods, in.BrowsePath,
		in.CreatedAt.Format(time.RFC3339Nano), expiresStr,
	)
```

`scanRows` — add an `expires` string var and Scan target, and parse. In the `var (...)` block add `expires string`; append `&expires` to the `rows.Scan(...)` after `&created`; after `rule.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)` add:
```go
		if expires != "" {
			rule.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
		}
```

- [ ] **Step 5: Add the schema column + migration** in `internal/store/migrate.go`.

In the `CREATE TABLE IF NOT EXISTS rules (...)` block, add after `browse_path        TEXT NOT NULL DEFAULT '',`:
```sql
	expires_at         TEXT NOT NULL DEFAULT '',
```
(Keep `created_at         TEXT NOT NULL` as the last column.)

Append to the `migrations` slice (after the `access_request_edits` step added earlier this session):
```go
	{"rule_expiry", func(tx *sql.Tx) error {
		if _, err := tx.Exec(`ALTER TABLE rules ADD COLUMN expires_at TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("rule_expiry: %w", err)
		}
		return nil
	}},
```

- [ ] **Step 6: Write the failing migration test** in `internal/store/store_test.go`, modelled on `TestBrowseRuleColumns`:

```go
func TestRuleExpiresAtColumn(t *testing.T) {
	// Fresh DB from current schema has the column.
	s, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	_, err = s.DB().Exec(`SELECT expires_at FROM rules LIMIT 0`)
	require.NoError(t, err)
	require.Equal(t, len(migrations), userVersion(t, s))
}
```

- [ ] **Step 7: Run the tests**

Run: `go test ./internal/policy/ ./internal/store/`
Expected: PASS. (If an existing store test seeds an OLD `rules` fixture and stamps a low `user_version`, it must still pass — the `rules` table exists in those fixtures, so `ALTER TABLE rules` succeeds. Verify `TestServerProfileColumns` and `TestAgentLastSeenColumn` still pass; they seed a `rules` table so the new step applies cleanly.)

- [ ] **Step 8: Commit**

```bash
make fmt && make vet && go test ./internal/policy/ ./internal/store/
git add internal/store/migrate.go internal/policy/rule.go internal/policy/registry.go internal/store/store_test.go internal/policy/registry_test.go
git commit -m "feat(policy): persist per-rule expires_at (rule_expiry migration)"
```

---

## Task 2: Enforce expiry in `Decide`

**Files:**
- Modify: `internal/policy/decide.go` (`Decide`, right after `ForUpstream`)
- Test: `internal/policy/decide_test.go`

**Interfaces:**
- Consumes: `policy.Rule.ExpiresAt` (Task 1).
- Produces: expired rules (`!ExpiresAt.IsZero() && ExpiresAt.Before(now)`) are invisible to both the raw and server-profile decision paths.

- [ ] **Step 1: Write the failing test** in `internal/policy/decide_test.go` (reuse the file's existing setup helpers; if it builds a Registry via a temp store, follow that; a browse `allow /**` rule is the simplest to assert on):

```go
func TestDecideSkipsExpiredRule(t *testing.T) {
	r := newRegistry(t) // same helper Task 1 used
	past := time.Now().UTC().Add(-time.Hour)
	_, err := r.Create(policy.Rule{UpstreamID: "u1", Outcome: policy.Allow, BrowseMethods: "GET", BrowsePath: "/**", ExpiresAt: past})
	require.NoError(t, err)

	// Expired allow → default-deny.
	dec, err := r.Decide(policy.Input{UpstreamID: "u1", Method: "GET", Path: "/x"})
	require.NoError(t, err)
	require.Equal(t, policy.Deny, dec.Outcome)

	// A live (future) rule still grants.
	_, err = r.Create(policy.Rule{UpstreamID: "u2", Outcome: policy.Allow, BrowseMethods: "GET", BrowsePath: "/**", ExpiresAt: time.Now().UTC().Add(time.Hour)})
	require.NoError(t, err)
	dec, err = r.Decide(policy.Input{UpstreamID: "u2", Method: "GET", Path: "/x"})
	require.NoError(t, err)
	require.Equal(t, policy.Allow, dec.Outcome)
}
```

Match `policy.Input`/`Decision` field names to the file's existing tests (e.g. how they set `Method`/`Path`/`UpstreamID` and read `dec.Outcome`). Do not invent fields.

- [ ] **Step 2: Run it — expect FAIL** (`Deny` expected, got `Allow` — the expired rule still grants)

Run: `go test ./internal/policy/ -run TestDecideSkipsExpiredRule`
Expected: FAIL on the first assertion.

- [ ] **Step 3: Filter expired rules in `Decide`.** In `internal/policy/decide.go`, immediately after:
```go
	rules, err := r.ForUpstream(in.UpstreamID)
	if err != nil {
		return Decision{}, err
	}
```
insert:
```go
	// Expired rules are treated as absent (default-deny) for BOTH the raw and server-profile paths.
	// They are never deleted — the operator sees and renews them in the UI (ADR-0045).
	now := time.Now().UTC()
	live := make([]*Rule, 0, len(rules))
	for _, rule := range rules {
		if !rule.ExpiresAt.IsZero() && rule.ExpiresAt.Before(now) {
			continue
		}
		live = append(live, rule)
	}
	rules = live
```
(`time` is already imported in this package.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/policy/`
Expected: PASS (new test + all existing decide/profile tests).

- [ ] **Step 5: Commit**

```bash
make fmt && make vet && go test ./internal/policy/
git add internal/policy/decide.go internal/policy/decide_test.go
git commit -m "feat(policy): expired rules are ignored at decision time (default-deny)"
```

---

## Task 3: `policy.Registry.Renew`

**Files:**
- Modify: `internal/policy/registry.go` (new `Renew` method near `Delete`)
- Test: `internal/policy/registry_test.go`

**Interfaces:**
- Produces: `func (r *Registry) Renew(id string, expiresAt time.Time) error` — sets `expires_at` (zero → `''` = never). Consumed by the daemon renew endpoint (Task 4).

- [ ] **Step 1: Write the failing test**

```go
func TestRenew(t *testing.T) {
	r := newRegistry(t)
	past := time.Now().UTC().Add(-time.Hour)
	out, err := r.Create(policy.Rule{UpstreamID: "u1", Outcome: policy.Allow, BrowsePath: "/**", ExpiresAt: past})
	require.NoError(t, err)

	future := time.Now().UTC().Add(24 * time.Hour)
	require.NoError(t, r.Renew(out.ID, future))
	got, _ := r.ForUpstream("u1")
	require.WithinDuration(t, future, got[0].ExpiresAt, time.Millisecond)

	// Zero time clears expiry (make permanent).
	require.NoError(t, r.Renew(out.ID, time.Time{}))
	got, _ = r.ForUpstream("u1")
	require.True(t, got[0].ExpiresAt.IsZero())
}
```

- [ ] **Step 2: Run it — expect FAIL** (`Renew` undefined)

Run: `go test ./internal/policy/ -run TestRenew`
Expected: build error `r.Renew undefined`.

- [ ] **Step 3: Implement `Renew`** in `internal/policy/registry.go` (place it just above `Delete`):

```go
// Renew sets a rule's expiry to expiresAt (zero time ⇒ "" = never expires). Used by the operator's
// renew action so an expired-but-valuable rule can be extended instead of recreated (ADR-0045).
func (r *Registry) Renew(id string, expiresAt time.Time) error {
	expiresStr := ""
	if !expiresAt.IsZero() {
		expiresStr = expiresAt.UTC().Format(time.RFC3339Nano)
	}
	if _, err := r.store.DB().Exec(`UPDATE rules SET expires_at=? WHERE id=?`, expiresStr, id); err != nil {
		return fmt.Errorf("renew rule: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/policy/ -run TestRenew`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
make fmt && make vet && go test ./internal/policy/
git add internal/policy/registry.go internal/policy/registry_test.go
git commit -m "feat(policy): Registry.Renew sets/clears a rule's expiry"
```

---

## Task 4: Thread `ttl_seconds` through the daemon (approve, manual create, renew, list)

**Files:**
- Modify: `internal/daemon/admin.go` (`hApprovalResolve`, `applyApprovalSideEffects`, `approveOperation`, `approveK8sAccess`, `hRuleCreate`, `hRuleList`; new `hRuleRenew` + route; new `expiryFromTTL` helper)
- Modify: `internal/daemon/admin_preset.go` (`approvePreset` gains an `expiresAt` param)
- Test: `internal/daemon/admin_test.go` (or the existing daemon test file that drives approvals/rules)

**Interfaces:**
- Consumes: `policy.Rule.ExpiresAt`, `policy.Registry.Renew` (Tasks 1–3).
- Produces:
  - `expiryFromTTL(ttlSeconds int) time.Time` — `time.Time{}` when `ttlSeconds <= 0`, else `time.Now().UTC().Add(time.Duration(ttlSeconds) * time.Second)`.
  - `hApprovalResolve` body gains `TTLSeconds int json:"ttl_seconds"`.
  - `applyApprovalSideEffects(p, auth, trustAny, bindings, expiresAt)` — new trailing `expiresAt time.Time` param.
  - `approvePreset(p, bindings, expiresAt)`, `approveOperation(p, trustAny, expiresAt)`, `approveK8sAccess(p, expiresAt)` — new trailing `expiresAt time.Time`.
  - `hRuleCreate` body gains `TTLSeconds int json:"ttl_seconds"`.
  - `POST /rules/{id}/renew` → `hRuleRenew` (operator-gated), body `{ttl_seconds int}`.
  - `hRuleList` JSON gains `"expires_at": <RFC3339 or "">`.

- [ ] **Step 1: Write the failing daemon test.** In the daemon test file that already builds a `*Daemon` with an unlocked vault + registered agent/upstream (follow the existing helper; e.g. `newTestDaemon`/`buildDaemon`), add:

```go
func TestManualRuleCreateWithTTL(t *testing.T) {
	d := newTestDaemon(t) // existing helper
	u := d.mustUpstream(t, "api.github.com") // adapt to existing helpers for creating an upstream
	body := `{"upstream_id":"` + u.ID + `","outcome":"allow","browse_path":"/**","browse_methods":"GET","ttl_seconds":7200}`
	// call hRuleCreate through the operator-gated admin handler the other tests use
	res := d.adminPOST(t, "/rules", body) // adapt to the existing admin test-request helper
	require.Equal(t, 200, res.Code)

	rules, err := d.policy.ForUpstream(u.ID)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	require.False(t, rules[0].ExpiresAt.IsZero())
	require.WithinDuration(t, time.Now().UTC().Add(2*time.Hour), rules[0].ExpiresAt, time.Minute)
}

func TestRuleRenewEndpoint(t *testing.T) {
	d := newTestDaemon(t)
	u := d.mustUpstream(t, "api.github.com")
	rule, err := d.policy.Create(policy.Rule{UpstreamID: u.ID, Outcome: policy.Allow, BrowsePath: "/**", ExpiresAt: time.Now().UTC().Add(-time.Hour)})
	require.NoError(t, err)
	res := d.adminPOST(t, "/rules/"+rule.ID+"/renew", `{"ttl_seconds":86400}`)
	require.Equal(t, 200, res.Code)
	got, _ := d.policy.ForUpstream(u.ID)
	require.WithinDuration(t, time.Now().UTC().Add(24*time.Hour), got[0].ExpiresAt, time.Minute)
	// ttl_seconds:0 makes it permanent.
	res = d.adminPOST(t, "/rules/"+rule.ID+"/renew", `{"ttl_seconds":0}`)
	require.Equal(t, 200, res.Code)
	got, _ = d.policy.ForUpstream(u.ID)
	require.True(t, got[0].ExpiresAt.IsZero())
}
```

Adapt `newTestDaemon`/`mustUpstream`/`adminPOST` to the ACTUAL helpers in the daemon test package (read the top of the existing daemon test file first and reuse its request/harness helpers; the operator gate is opened by the same helper the other `/rules` tests use).

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/daemon/ -run 'TestManualRuleCreateWithTTL|TestRuleRenewEndpoint'`
Expected: FAIL (`ttl_seconds` ignored → `ExpiresAt` zero; `/rules/{id}/renew` → 404).

- [ ] **Step 3: Add the `expiryFromTTL` helper** (top of `internal/daemon/admin_preset.go` or `admin.go`, near the approve helpers):

```go
// expiryFromTTL converts an operator-chosen ttl_seconds into an absolute expiry. ttl <= 0 means the
// grant never expires (zero time). Server-authoritative time (ADR-0045).
func expiryFromTTL(ttlSeconds int) time.Time {
	if ttlSeconds <= 0 {
		return time.Time{}
	}
	return time.Now().UTC().Add(time.Duration(ttlSeconds) * time.Second)
}
```
(Add `"time"` to that file's imports if missing.)

- [ ] **Step 4: Thread `expiresAt` into the approve fan-out.** In `internal/daemon/admin.go`:

`hApprovalResolve` body struct — add:
```go
		// TTLSeconds is the operator's chosen grant duration for the rules this approval creates
		// (0 = never expires). Ignored for a deny.
		TTLSeconds int `json:"ttl_seconds"`
```
In `hApprovalResolve`, where it calls `applyApprovalSideEffects(p, body.Auth, body.TrustAny, body.Bindings)`, change to:
```go
			if err := d.applyApprovalSideEffects(p, body.Auth, body.TrustAny, body.Bindings, expiryFromTTL(body.TTLSeconds)); err != nil {
```

`applyApprovalSideEffects` signature + calls:
```go
func (d *Daemon) applyApprovalSideEffects(p approval.Pending, auth *upstream.AuthConfig, trustAny []string, bindings map[string]string, expiresAt time.Time) error {
```
- `case approval.KindOperation:` → `return d.approveOperation(p, trustAny, expiresAt)`
- `case approval.KindK8sAccess:` → `return d.approveK8sAccess(p, expiresAt)`
- `case approval.KindPreset:` → `return d.approvePreset(p, bindings, expiresAt)`

`approveK8sAccess(p approval.Pending, expiresAt time.Time)` — set expiry on each created rule:
```go
		missing = append(missing, policy.Rule{
			SubjectAgentID: p.AgentID, UpstreamID: p.UpstreamID, Outcome: policy.Allow,
			Namespace: g.Namespace, Resource: g.Resource, Verb: g.Verb, ExpiresAt: expiresAt,
		})
```

`approveOperation(p approval.Pending, trustAny []string, expiresAt time.Time)` — two places:
1. The create path: add `ExpiresAt: expiresAt` to the `policy.Rule{...}` passed to `d.policy.Create(...)`.
2. The extend path (an existing rule matched by template Key): after growing its value set, refresh its expiry so a re-approval extends the grant's life. Just before the function returns on the extend branch add:
```go
	if err := d.policy.Renew(rule.ID, expiresAt); err != nil {
		return fmt.Errorf("renew operation rule: %w", err)
	}
```
(Place it after the value-set extension loop, on the path where `rule != nil`.)

- [ ] **Step 5: `approvePreset` gains `expiresAt`.** In `internal/daemon/admin_preset.go` change the signature to `func (d *Daemon) approvePreset(p approval.Pending, bindings map[string]string, expiresAt time.Time) error` and set `ExpiresAt: expiresAt` on the `policy.Rule{...}` built inside the `for _, t := range tmpls` loop:
```go
		r := policy.Rule{
			SubjectAgentID: p.AgentID, UpstreamID: p.UpstreamID, Outcome: t.Outcome,
			BrowseMethods: t.BrowseMethods, BrowsePath: t.BrowsePath,
			Profile: t.Profile, ProfileParams: t.ProfileParams, ExpiresAt: expiresAt,
		}
```
Note: the idempotent `isDup` check ignores `ExpiresAt` (compares outcome/browse/profile only) — that is intentional; a duplicate rule is skipped rather than re-dated. Leave `isDup` unchanged.

- [ ] **Step 6: Manual create honors `ttl_seconds`.** In `hRuleCreate`, add to the body struct:
```go
		TTLSeconds int `json:"ttl_seconds"`
```
and add `ExpiresAt: expiryFromTTL(body.TTLSeconds),` to the `policy.Rule{...}` passed to `d.policy.Create(...)`.

- [ ] **Step 7: Add the renew endpoint.** In `internal/daemon/admin.go`, register the route next to the other gated `/rules` routes (near `gate("POST /rules", d.hRuleCreate)`):
```go
	gate("POST /rules/{id}/renew", d.hRuleRenew)
```
and add the handler:
```go
func (d *Daemon) hRuleRenew(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TTLSeconds int `json:"ttl_seconds"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if err := d.policy.Renew(r.PathValue("id"), expiryFromTTL(body.TTLSeconds)); err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.publish("rule.renewed", map[string]any{"id": r.PathValue("id")})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
```

- [ ] **Step 8: Expose `expires_at` in `hRuleList`.** In the `out = append(out, map[string]any{...})` block, add:
```go
			"expires_at": func() string {
				if rule.ExpiresAt.IsZero() {
					return ""
				}
				return rule.ExpiresAt.UTC().Format(time.RFC3339Nano)
			}(),
```
(`time` is already imported in admin.go.)

- [ ] **Step 9: Run tests**

Run: `go test ./internal/daemon/`
Expected: PASS (new + existing). Fix any other in-package callers of the changed signatures (`applyApprovalSideEffects`, `approvePreset`, `approveOperation`, `approveK8sAccess`) the compiler flags — pass `time.Time{}` only where a caller genuinely has no ttl (there should be none besides `hApprovalResolve`).

- [ ] **Step 10: Commit**

```bash
make fmt && make vet && go test ./internal/daemon/ ./internal/policy/
git add internal/daemon/admin.go internal/daemon/admin_preset.go internal/daemon/*_test.go
git commit -m "feat(daemon): grant TTL on approve/manual-create + POST /rules/{id}/renew"
```

---

## Task 5: Web transport — `DurationSelect`, `types`, `api`

**Files:**
- Create: `web/src/pages/access/DurationSelect.tsx`
- Create: `web/src/pages/access/DurationSelect.test.tsx`
- Modify: `web/src/lib/types.ts` (`Rule.expires_at`; `ResolveOptions.ttl_seconds`)
- Modify: `web/src/lib/api.ts` (`renewRule`; `createRule` payload already spreads the rule — `ttl_seconds` rides along)
- Test: `web/src/lib/api.test.ts`

**Interfaces:**
- Produces:
  - `DURATION_OPTIONS: { label: string; seconds: number }[]` and `DEFAULT_TTL_SECONDS = 3600`.
  - `<DurationSelect value={number} onChange={(seconds:number)=>void} />` — a controlled `<select>`.
  - `renewRule(id: string, ttlSeconds: number): Promise<{ ok: boolean }>` → `POST /rules/{id}/renew`.
  - `Rule.expires_at?: string` (RFC3339 or `''`); `ResolveOptions.ttl_seconds?: number`.

- [ ] **Step 1: Write the failing `DurationSelect` test** `web/src/pages/access/DurationSelect.test.tsx`:

```tsx
import { render, screen, fireEvent } from '@testing-library/react'
import { describe, it, expect, vi } from 'vitest'
import { DurationSelect, DURATION_OPTIONS, DEFAULT_TTL_SECONDS } from './DurationSelect'

describe('DurationSelect', () => {
  it('lists every option incl. Бессрочно and defaults to 1h', () => {
    expect(DEFAULT_TTL_SECONDS).toBe(3600)
    expect(DURATION_OPTIONS.map((o) => o.seconds)).toEqual([3600, 7200, 28800, 86400, 172800, 604800, 2592000, 31536000, 0])
    render(<DurationSelect value={3600} onChange={() => {}} />)
    expect(screen.getByRole('option', { name: 'Бессрочно' })).toBeInTheDocument()
  })
  it('emits the chosen seconds as a number', () => {
    const onChange = vi.fn()
    render(<DurationSelect value={3600} onChange={onChange} />)
    fireEvent.change(screen.getByRole('combobox'), { target: { value: '86400' } })
    expect(onChange).toHaveBeenCalledWith(86400)
  })
})
```

- [ ] **Step 2: Run — expect FAIL** (module not found)

Run: `pnpm -C web exec vitest run src/pages/access/DurationSelect.test.tsx`
Expected: FAIL (cannot resolve `./DurationSelect`).

- [ ] **Step 3: Create `web/src/pages/access/DurationSelect.tsx`:**

```tsx
export const DURATION_OPTIONS: { label: string; seconds: number }[] = [
  { label: '1 час', seconds: 3600 },
  { label: '2 часа', seconds: 7200 },
  { label: '8 часов', seconds: 28800 },
  { label: '24 часа', seconds: 86400 },
  { label: '2 дня', seconds: 172800 },
  { label: '7 дней', seconds: 604800 },
  { label: '1 месяц', seconds: 2592000 },
  { label: '1 год', seconds: 31536000 },
  { label: 'Бессрочно', seconds: 0 },
]

export const DEFAULT_TTL_SECONDS = 3600

/** DurationSelect is the shared grant-duration dropdown (approval card, manual grant, renew).
 *  value/onChange are ttl_seconds (0 = Бессрочно / never expires). */
export function DurationSelect({
  value,
  onChange,
  className,
}: {
  value: number
  onChange: (seconds: number) => void
  className?: string
}) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(Number(e.target.value))}
      className={
        className ??
        'rounded-md border border-border bg-card px-2 py-1 text-[12.5px] text-foreground'
      }
      aria-label="grant duration"
    >
      {DURATION_OPTIONS.map((o) => (
        <option key={o.seconds} value={o.seconds}>
          {o.label}
        </option>
      ))}
    </select>
  )
}
```

- [ ] **Step 4: Add types.** In `web/src/lib/types.ts`:
- In `interface Rule { ... }` add: `expires_at?: string // RFC3339Nano or '' (never)`.
- In `interface ResolveOptions { ... }` add: `ttl_seconds?: number // grant duration in seconds (0 = never); rides the approve payload`.

- [ ] **Step 5: Add `renewRule`.** In `web/src/lib/api.ts`, next to `deleteRule`:
```ts
export function renewRule(id: string, ttl_seconds: number): Promise<{ ok: boolean }> {
  return request('POST', `/rules/${encodeURIComponent(id)}/renew`, { ttl_seconds })
}
```

- [ ] **Step 6: Write the failing `renewRule` api test** in `web/src/lib/api.test.ts` (follow the file's existing fetch-mock pattern):
```ts
it('renewRule POSTs ttl_seconds to /rules/{id}/renew', async () => {
  const spy = mockFetchOnce({ ok: true }) // adapt to the file's helper
  await renewRule('r1', 86400)
  expect(spy).toHaveBeenCalledWith(expect.stringContaining('/rules/r1/renew'), expect.objectContaining({
    method: 'POST',
    body: JSON.stringify({ ttl_seconds: 86400 }),
  }))
})
```
Adapt the mock/assertion to the existing helpers in `api.test.ts` (do not invent `mockFetchOnce` if the file uses another pattern).

- [ ] **Step 7: Run web tests**

Run: `pnpm -C web exec vitest run src/pages/access/DurationSelect.test.tsx src/lib/api.test.ts`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
make lint-web && make test-web
git checkout -- internal/daemon/webdist/index.html 2>/dev/null || true
git add web/src/pages/access/DurationSelect.tsx web/src/pages/access/DurationSelect.test.tsx web/src/lib/types.ts web/src/lib/api.ts web/src/lib/api.test.ts
git commit -m "feat(web): DurationSelect + renewRule api + Rule.expires_at"
```

---

## Task 6: RuleRow expiry chip + expired badge + renew; grant-card aggregate; expired colour

**Files:**
- Modify: `web/src/pages/access/RuleRow.tsx`
- Modify: `web/src/lib/grants.ts` (add `grantExpiry(rules): 'expired'|'expiring'|null` aggregate helper)
- Modify: `web/src/pages/access/UpstreamGrantCard.tsx`, `web/src/pages/access/UpstreamGroupCard.tsx` (render aggregate badge)
- Modify: `web/src/index.css` + `web/src/components/StatusBadge.tsx` (add an `expired` colour token/variant if missing)
- Test: `web/src/pages/access/RuleRow.test.tsx`

**Interfaces:**
- Consumes: `Rule.expires_at`, `renewRule`, `DurationSelect`, `DEFAULT_TTL_SECONDS`, `RelTime`.
- Produces: `grantExpiry(rules: Rule[]): 'expired' | 'expiring' | null` (expired if any rule past; else 'expiring' if the soonest future expiry is < 3600s away; else null).

- [ ] **Step 1: Write the failing RuleRow test** additions in `web/src/pages/access/RuleRow.test.tsx` (follow the file's existing render helper + `vi.mock('../../lib/api', ...)`):

```tsx
it('shows an expired badge for a past expires_at and renews on click', async () => {
  const renew = vi.fn().mockResolvedValue({ ok: true })
  // extend the existing api mock so renewRule is spied (match how deleteRule is mocked in this file)
  const past = new Date(Date.now() - 3600_000).toISOString()
  render(<RuleRow rule={{ id: 'r1', upstream_id: 'u1', outcome: 'allow', browse_path: '/**', expires_at: past } as any} onChanged={() => {}} />)
  expect(screen.getByText('истекло')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: 'Продлить' }))
  // pick a duration then confirm → renewRule(id, seconds)
  fireEvent.change(screen.getByRole('combobox', { name: 'grant duration' }), { target: { value: '86400' } })
  fireEvent.click(screen.getByRole('button', { name: 'ОК' }))
  await waitFor(() => expect(renew).toHaveBeenCalledWith('r1', 86400))
})

it('shows no expired badge and no chip for a never-expiring rule', () => {
  render(<RuleRow rule={{ id: 'r2', upstream_id: 'u1', outcome: 'allow', browse_path: '/**', expires_at: '' } as any} onChanged={() => {}} />)
  expect(screen.queryByText('истекло')).not.toBeInTheDocument()
})
```
Wire the `renew` spy the way the file already mocks `deleteRule` (extend the `vi.mock('../../lib/api', ...)` factory to export `renewRule`).

- [ ] **Step 2: Run — expect FAIL**

Run: `pnpm -C web exec vitest run src/pages/access/RuleRow.test.tsx`
Expected: FAIL (no expired badge / no Продлить button).

- [ ] **Step 3: Add expiry UI to `RuleRow.tsx`.** Imports: add `RelTime` (`import { RelTime } from '../../components/RelTime'`), `DurationSelect, DEFAULT_TTL_SECONDS` from `./DurationSelect`, and `renewRule` from `../../lib/api`. Add state:
```tsx
  const [renewing, setRenewing] = useState(false)
  const [ttl, setTtl] = useState(DEFAULT_TTL_SECONDS)
  const expired = !!rule.expires_at && new Date(rule.expires_at).getTime() < Date.now()

  async function renew() {
    try {
      await renewRule(rule.id, ttl)
      push('success', 'Grant renewed')
      setRenewing(false)
      onChanged()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to renew')
    }
  }
```
In the row's right-side control cluster (the `<div className="ml-auto flex items-center gap-1">`), before the outcome span, add an expiry chip / badge:
```tsx
          {rule.expires_at &&
            (expired ? (
              <span className="rounded bg-destructive/15 px-1.5 text-[11px] text-destructive">истекло</span>
            ) : (
              <span className="text-[11px] text-muted-foreground" title="expires">
                истекает <RelTime iso={rule.expires_at} />
              </span>
            ))}
          {(expired || rule.expires_at) && (
            <button
              onClick={() => setRenewing((v) => !v)}
              aria-label="Продлить"
              className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground text-[11px]"
            >
              Продлить
            </button>
          )}
```
And a small inline renew popover after the top row (sibling of the `{open && ...}` block):
```tsx
      {renewing && (
        <div className="flex items-center gap-2 border-t border-border px-2.5 py-2">
          <DurationSelect value={ttl} onChange={setTtl} />
          <button onClick={renew} aria-label="ОК" className="rounded bg-primary px-2 py-1 text-[12px] text-primary-foreground">
            ОК
          </button>
        </div>
      )}
```
(`RelTime` renders a relative label with the exact time on hover — it exists from the prior relative-timestamps work.)

- [ ] **Step 4: Add the grant aggregate helper** in `web/src/lib/grants.ts`:
```ts
/** grantExpiry summarises a grant's rules: 'expired' if any rule is past its expiry, else
 *  'expiring' if the soonest future expiry is under an hour away, else null (all permanent / far). */
export function grantExpiry(rules: Rule[]): 'expired' | 'expiring' | null {
  const now = Date.now()
  let soonest = Infinity
  for (const r of rules) {
    if (!r.expires_at) continue
    const t = new Date(r.expires_at).getTime()
    if (t < now) return 'expired'
    soonest = Math.min(soonest, t)
  }
  if (soonest !== Infinity && soonest - now < 3600_000) return 'expiring'
  return null
}
```
(Ensure `Rule` is imported in `grants.ts`; it already imports from `./types`.)

- [ ] **Step 5: Render the aggregate badge** in `UpstreamGrantCard.tsx` and `UpstreamGroupCard.tsx`. Import `grantExpiry` from `../../lib/grants`; where each card has its list of rules for the grant, compute `const exp = grantExpiry(rules)` and render next to the header:
```tsx
        {exp === 'expired' && <span className="rounded bg-destructive/15 px-1.5 text-[11px] text-destructive">истекло</span>}
        {exp === 'expiring' && <span className="rounded bg-warning/15 px-1.5 text-[11px] text-warning">истекает</span>}
```
If a `warning` colour token is absent, use `text-muted-foreground` for `expiring` and add the token in Step 6.

- [ ] **Step 6: Add an `expired` colour** if the design system lacks one. In `web/src/components/StatusBadge.tsx` map `expired`/`revoked` to `C_STOPPED` (or a new `--color-status-expired` token in `web/src/index.css`). Keep it minimal — reuse an existing token unless the palette clearly needs a new one. (This closes the backlog note that `revoked`/`expired` had no colour.)

- [ ] **Step 7: Run web tests + typecheck**

Run: `pnpm -C web exec vitest run src/pages/access/RuleRow.test.tsx && make lint-web`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
make test-web && make lint-web
git checkout -- internal/daemon/webdist/index.html 2>/dev/null || true
git add web/src/pages/access/RuleRow.tsx web/src/lib/grants.ts web/src/pages/access/UpstreamGrantCard.tsx web/src/pages/access/UpstreamGroupCard.tsx web/src/components/StatusBadge.tsx web/src/index.css web/src/pages/access/RuleRow.test.tsx
git commit -m "feat(web): rule expiry chip + expired badge + Продлить renew + grant aggregate"
```

---

## Task 7: Wire `DurationSelect` into the approval card and manual-grant modal

**Files:**
- Modify: `web/src/pages/access/ApprovalCards.tsx` (add TTL state; include `ttl_seconds` in the approve `ResolveOptions`)
- Modify: `web/src/pages/access/ManualRuleModal.tsx` (add TTL state; include `ttl_seconds` in the `createRule` payload)
- Test: `web/src/pages/access/ApprovalCards.test.tsx`, `web/src/pages/access/ManualRuleModal.test.tsx`

**Interfaces:**
- Consumes: `DurationSelect`, `DEFAULT_TTL_SECONDS`; `ResolveOptions.ttl_seconds`; `createRule` payload.

- [ ] **Step 1: Write the failing approval-card test.** In `ApprovalCards.test.tsx`, for a card that creates rules (preset or operation), assert the resolve callback receives `ttl_seconds`:
```tsx
it('passes the chosen ttl_seconds when approving a preset', async () => {
  const onResolve = vi.fn()
  // render the preset card with a stub approval (match the file's existing card-render helper)
  renderPresetCard({ onResolve })
  fireEvent.change(screen.getByRole('combobox', { name: 'grant duration' }), { target: { value: '28800' } })
  fireEvent.click(screen.getByRole('button', { name: /approve|Одобрить/i }))
  await waitFor(() => expect(onResolve).toHaveBeenCalledWith(expect.any(String), true, expect.objectContaining({ ttl_seconds: 28800 })))
})
```
Adapt to the file's actual render helper and approve-button label.

- [ ] **Step 2: Run — expect FAIL**

Run: `pnpm -C web exec vitest run src/pages/access/ApprovalCards.test.tsx`
Expected: FAIL (`ttl_seconds` not passed).

- [ ] **Step 3: Add the dropdown to the approval cards.** In `ApprovalCards.tsx`, in each card that CREATES rules (the preset card and the operation/new-value card — NOT the host-credential or plain deny path), add near the approve button:
```tsx
  const [ttl, setTtl] = useState(DEFAULT_TTL_SECONDS)
  // ...
  <DurationSelect value={ttl} onChange={setTtl} />
```
and merge `ttl_seconds: ttl` into the options passed to `onResolve`. For the preset card change `onResolve(approval.id, true, { bindings })` → `onResolve(approval.id, true, { bindings, ttl_seconds: ttl })`. For the operation card change `{ trust_any }`/`{ trust_any: [] }` → include `ttl_seconds: ttl`. (Import `DurationSelect, DEFAULT_TTL_SECONDS` from `./DurationSelect`.)

- [ ] **Step 4: Write the failing manual-modal test.** In `ManualRuleModal.test.tsx`, assert `createRule` is called with `ttl_seconds`:
```tsx
it('includes ttl_seconds in the created rule', async () => {
  const create = vi.fn().mockResolvedValue({ id: 'r1' })
  // extend the api mock so createRule is the spy; render the modal, fill the minimum required fields
  renderModal({ createRule: create })
  fireEvent.change(screen.getByRole('combobox', { name: 'grant duration' }), { target: { value: '604800' } })
  // ... trigger submit the way the existing tests do
  await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ ttl_seconds: 604800 })))
})
```

- [ ] **Step 5: Add the dropdown to `ManualRuleModal.tsx`.** Add `const [ttl, setTtl] = useState(DEFAULT_TTL_SECONDS)`, render `<DurationSelect value={ttl} onChange={setTtl} />` in the form, and add `ttl_seconds: ttl` to each `payload` object passed to `createRule(...)` (the http / k8s / profile branches).

- [ ] **Step 6: Run web tests + typecheck**

Run: `pnpm -C web exec vitest run src/pages/access/ApprovalCards.test.tsx src/pages/access/ManualRuleModal.test.tsx && make lint-web`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
make test-web && make lint-web
git checkout -- internal/daemon/webdist/index.html 2>/dev/null || true
git add web/src/pages/access/ApprovalCards.tsx web/src/pages/access/ManualRuleModal.tsx web/src/pages/access/ApprovalCards.test.tsx web/src/pages/access/ManualRuleModal.test.tsx
git commit -m "feat(web): grant-duration dropdown on approval card + manual grant"
```

---

## Task 8: ADR-0045 + docs

**Files:**
- Create: `docs/architecture/decisions/0045-grant-ttl-and-renewal.md`
- Modify: `docs/INDEX.md` (ADR line)
- Modify: `docs/architecture/modules/policy.md` (Rule.ExpiresAt; Decide expiry filter; Renew), `docs/architecture/modules/store.md` (rule_expiry migration + expires_at column), and `docs/architecture/modules/webui.md` if it enumerates access components (add DurationSelect / expiry chips).

- [ ] **Step 1: Write ADR-0045** following `docs/workflow/adr-template.md`. Context: grants were permanent; operator needs time-limited access; complex rules must not be auto-deleted. Decision: per-rule `expires_at` (`''`=never); enforced only in `Decide` (both paths) by filtering expired rules after load; `ForUpstream`/`List` unfiltered; `ttl_seconds` transport with server-side `expires_at = now + ttl`; TTL stamped by approve (preset/operation/k8s) + manual create; `POST /rules/{id}/renew`; expired rules kept + marked + renewable in the UI; durations fixed (1m=30d, 1y=365d); default 1h. Alternatives considered: janitor auto-delete (rejected — loses valuable rules), filtering in `ForUpstream` (rejected — UI must see expired), client-supplied `expires_at` (rejected — server-authoritative time), agent-facing expiry (deferred). Consequences: additive column + `rule_expiry` migration; signature changes to `applyApprovalSideEffects`/`approvePreset`/`approveOperation`/`approveK8sAccess`; operation extend-path refreshes expiry on re-approval.

- [ ] **Step 2: Update module docs + INDEX** as listed (present-tense "what is"; one INDEX ADR line mirroring the 0043/0044 style).

- [ ] **Step 3: Full gate**

Run: `make check`
Expected: PASS (gofmt, vet, golangci-lint, eslint, tsc, go test, vitest, build).

- [ ] **Step 4: Reset webdist churn + commit**

```bash
git checkout -- internal/daemon/webdist/index.html
git add docs/
git commit -m "docs(adr): ADR-0045 grant TTL and renewal; module docs"
```

---

## Self-review notes (author)

- **Spec coverage:** data model+migration → T1; Decide enforcement → T2; renew backend → T3/T4; ttl transport (approve+manual) + renew endpoint + list exposure → T4; DurationSelect/api/types → T5; RuleRow chip/badge/renew + grant aggregate + expired colour → T6; approval-card + manual-modal wiring → T7; ADR + docs → T8. All spec sections mapped.
- **Type consistency:** `expiryFromTTL(int) time.Time`, `Renew(id string, expiresAt time.Time) error`, `renewRule(id, ttl_seconds)`, `DurationSelect{value:number,onChange:(number)=>void}`, `Rule.expires_at?:string`, `ResolveOptions.ttl_seconds?:number`, `grantExpiry(Rule[]) → 'expired'|'expiring'|null` — used consistently across tasks.
- **Harness caveat:** Go test-helper names (`newRegistry`, `newTestDaemon`, `mustUpstream`, `adminPOST`) and web mock helpers are placeholders for the ACTUAL helpers in each test file — the implementer reuses the existing harness rather than inventing one. This is called out per task.
</content>
