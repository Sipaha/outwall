# Atomic rule fan-out (transactional CreateMany) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make multi-rule fan-out atomic: add `policy.Registry.CreateMany` that writes all rules in ONE SQL transaction (all-or-nothing), and route both fan-out sites (`approvePreset`, `approveK8sAccess`) through it so a mid-batch failure can never leave a partial grant.

**Architecture:** Extract the single-row prepare+INSERT in `policy.Registry.Create` into a private `insertRule(exec rowExecutor, in Rule)` helper, where `rowExecutor` is a minimal `Exec(...)` interface satisfied by both `*sql.DB` and `*sql.Tx`. `Create` calls it with the DB; `CreateMany` opens a transaction and calls it per rule, rolling back on any error. This also centralizes the 18-column INSERT in one place. The daemon's two fan-out handlers build a `[]policy.Rule` and make a single `CreateMany` call. ADR-0038 records the change and supersedes the "known limitation" note in ADR-0037.

**Tech Stack:** Go (CGO-free server), `database/sql` + `modernc.org/sqlite`, `stretchr/testify`.

## Global Constraints

- Module path exactly `github.com/Sipaha/outwall` in every import.
- No CGO in the server binary (`CGO_ENABLED=0`). No panics in library code (wrapped errors, `%w`). `log/slog`.
- No `citeck` in core (ADR-0034 scope) — this plan touches `policy` + `daemon` (core); keep citeck-free (daemon tests may use the pre-existing sanctioned citeck blank-import + `"citeck"` profile-name value).
- Commit author MUST be `Sipaha <sipahabk@gmail.com>`: `git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit`. No `Co-Authored-By`. No amend. Branch `main`.
- Gate before each commit: `gofmt -w .`, `go vet ./...`, `go test ./... -race`, `CGO_ENABLED=0 go build ./...`.
- Builds on ADR-0037 (preset fan-out) / ADR-0029 (k8s multi-grant fan-out) — the two call sites this makes atomic.

## File structure (modified)

- **Modify** `internal/policy/registry.go` — extract `insertRule(rowExecutor, Rule)`; `Create` delegates to it; add `CreateMany([]Rule) ([]Rule, error)`; add `rowExecutor` interface + `database/sql` import.
- **Modify** `internal/policy/registry_test.go` — `CreateMany` happy-path + atomic-rollback tests.
- **Modify** `internal/daemon/admin.go` — `approvePreset` + `approveK8sAccess` use `CreateMany`.
- **Create** `docs/architecture/decisions/0038-atomic-rule-fanout.md`; **modify** `docs/INDEX.md`; **modify** `docs/architecture/decisions/0037-presets-first-class.md` (point its atomicity note at ADR-0038).

---

### Task 1: policy — `insertRule` helper + transactional `CreateMany`

**Files:**
- Modify: `internal/policy/registry.go` (`Create` ~27-66)
- Test: `internal/policy/registry_test.go`

**Interfaces:**
- Produces: `Registry.CreateMany(ins []Rule) ([]Rule, error)` — inserts all rules in one transaction; returns the created rules (with IDs/CreatedAt) or, on any per-rule error, rolls back and returns the error with NO rows written. `Create` behavior is unchanged (now delegates to the shared `insertRule`).

- [ ] **Step 1: Write the failing test** — add to `internal/policy/registry_test.go`:

```go
func TestCreateManyAtomic(t *testing.T) {
	reg := newReg(t)

	// Happy path: two valid rules → both persisted, IDs assigned.
	out, err := reg.CreateMany([]Rule{
		{UpstreamID: "u1", Outcome: Allow, BrowseMethods: "GET,HEAD", BrowsePath: "/**"},
		{UpstreamID: "u1", Outcome: Allow, Profile: "p", ProfileParams: []byte(`{"x":1}`)},
	})
	require.NoError(t, err)
	require.Len(t, out, 2)
	require.NotEmpty(t, out[0].ID)
	require.NotEqual(t, out[0].ID, out[1].ID)
	got, err := reg.ForUpstream("u1")
	require.NoError(t, err)
	require.Len(t, got, 2)

	// Atomic rollback: a batch whose 2nd rule is invalid (bad outcome) writes NOTHING.
	_, err = reg.CreateMany([]Rule{
		{UpstreamID: "u2", Outcome: Allow, BrowseMethods: "GET", BrowsePath: "/**"},
		{UpstreamID: "u2", Outcome: "bogus"},
	})
	require.Error(t, err)
	got2, err := reg.ForUpstream("u2")
	require.NoError(t, err)
	require.Empty(t, got2) // the first rule must NOT have been committed
}
```

> `newReg(t)` is the existing policy-test helper.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/policy/ -run TestCreateManyAtomic -v`
Expected: FAIL — `CreateMany` undefined.

- [ ] **Step 3: Write minimal implementation**

In `registry.go`, add `"database/sql"` to the import block. Add the executor interface (near the top, after the `Registry` type):

```go
// rowExecutor is the subset of *sql.DB / *sql.Tx that insertRule needs, so a single insert helper
// serves both the autocommit Create path and the transactional CreateMany path.
type rowExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
}
```

Refactor `Create` to delegate, and extract `insertRule` (move the existing validation + marshal + INSERT body verbatim into it, swapping `r.store.DB().Exec` for `exec.Exec`):

```go
// Create validates and persists a new rule, assigning an ID and CreatedAt.
func (r *Registry) Create(in Rule) (*Rule, error) {
	out, err := insertRule(r.store.DB(), in)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateMany persists all rules in a single transaction: either every rule is committed or none is
// (a per-rule validation/insert error rolls the whole batch back). Used by the approval fan-out paths
// so a mid-batch failure can never leave a partial grant.
func (r *Registry) CreateMany(ins []Rule) ([]Rule, error) {
	tx, err := r.store.DB().Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	out := make([]Rule, 0, len(ins))
	for _, in := range ins {
		ruleOut, ierr := insertRule(tx, in)
		if ierr != nil {
			_ = tx.Rollback()
			return nil, ierr
		}
		out = append(out, ruleOut)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return out, nil
}

// insertRule validates `in`, assigns its ID + CreatedAt, and inserts it via exec (a *sql.DB for the
// autocommit path or a *sql.Tx for a batch). The 18-column INSERT lives here only.
func insertRule(exec rowExecutor, in Rule) (Rule, error) {
	if !ValidOutcome(in.Outcome) {
		return Rule{}, fmt.Errorf("invalid outcome %q", in.Outcome)
	}
	if in.RateLimitPerMin < 0 {
		return Rule{}, fmt.Errorf("rate limit must be >= 0")
	}
	queryJSON, err := marshalJSONMap(in.OpQueryTemplate)
	if err != nil {
		return Rule{}, fmt.Errorf("marshal op_query_template: %w", err)
	}
	bodyJSON, err := marshalJSONMap(in.OpBodyTemplate)
	if err != nil {
		return Rule{}, fmt.Errorf("marshal op_body_template: %w", err)
	}
	policiesJSON, err := marshalValuePolicies(in.OpValuePolicies)
	if err != nil {
		return Rule{}, fmt.Errorf("marshal op_value_policies: %w", err)
	}
	params := in.ProfileParams
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	in.ID = newID()
	in.CreatedAt = time.Now().UTC()
	_, err = exec.Exec(
		`INSERT INTO rules (id, subject_agent_id, upstream_id, op_method, op_path_template, op_query_template, op_body_template, op_value_policies, outcome, rate_limit_per_min, k8s_namespace, k8s_resource, k8s_verb, profile, profile_params, browse_methods, browse_path, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.SubjectAgentID, in.UpstreamID, in.OpMethod, in.OpPathTemplate, queryJSON, bodyJSON, policiesJSON,
		in.Outcome, in.RateLimitPerMin,
		in.Namespace, in.Resource, in.Verb,
		in.Profile, string(params),
		in.BrowseMethods, in.BrowsePath,
		in.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return Rule{}, fmt.Errorf("insert rule: %w", err)
	}
	return in, nil
}
```

> Delete the old INSERT body from `Create` — it now lives in `insertRule` only. Confirm `database/sql` is the only new import.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/policy/ -race`
Expected: PASS (all existing policy tests + the new one).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/policy/
git add internal/policy/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(policy): transactional CreateMany (atomic multi-rule insert)"
```

---

### Task 2: daemon — route both fan-out sites through `CreateMany`

**Files:**
- Modify: `internal/daemon/admin.go` (`approvePreset` ~700-735; `approveK8sAccess` ~648-678)
- Test: `internal/daemon/admin_test.go` (existing `TestApprovePresetFansOutAgentScopedRules` + the k8s approve test must still pass)

**Interfaces:**
- Consumes: `policy.Registry.CreateMany` (Task 1).
- Produces: `approvePreset` and `approveK8sAccess` create their rules in one atomic batch each.

- [ ] **Step 1: Write the failing test** — add to `internal/daemon/admin_test.go` an atomicity assertion for the preset path (a Build that yields a valid + an invalid rule writes nothing). Since the real presets only ever Build valid rules, drive it through a fake profile whose preset Build returns one good rule + one with a bad outcome, OR assert the simpler invariant the wiring guarantees. Use this test (register a fake profile in the daemon test that returns a bad-outcome template):

```go
func TestApprovePresetFanoutIsAtomic(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, 200, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	// A fake profile whose preset Build yields a valid rule followed by an invalid-outcome rule.
	serverprofile.Register("atomicfake", atomicFakeProfile{})
	up := d.mustUpstreamProfiled(t, "af.test", "https://af.test", "http", "atomicfake")
	agentID := d.mustAgent(t, "a1")

	go func() {
		_, _ = d.approvals.Submit(context.Background(), approval.Pending{
			Kind: approval.KindPreset, AgentID: agentID, UpstreamID: up.ID, Host: up.Name,
			PresetID: "bad", Bindings: map[string]string{},
		})
	}()
	var id string
	require.Eventually(t, func() bool {
		for _, p := range d.approvals.List() {
			if p.Kind == approval.KindPreset {
				id = p.ID
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond)

	// Approve → fan-out fails on the invalid rule → 400 → NO rules created (atomic).
	require.Equal(t, 400, req(t, h, "POST", "/approvals/"+id+"/resolve", `{"approve":true}`).Code)
	rules, err := d.policy.ForUpstream(up.ID)
	require.NoError(t, err)
	require.Empty(t, rules)
}

// atomicFakeProfile's preset "bad" Builds one valid rule then one with an invalid outcome.
type atomicFakeProfile struct{}

func (atomicFakeProfile) Name() string { return "atomicfake" }
func (atomicFakeProfile) Classify(serverprofile.Request) (serverprofile.Operation, bool, error) {
	return serverprofile.Operation{}, false, nil
}
func (atomicFakeProfile) Match(serverprofile.Rule, serverprofile.Operation) (string, bool, error) {
	return "", false, nil
}
func (atomicFakeProfile) RuleSchema() serverprofile.RuleSchema {
	return serverprofile.RuleSchema{Profile: "atomicfake"}
}
func (atomicFakeProfile) Presets() []serverprofile.Preset {
	return []serverprofile.Preset{{
		ID: "bad", Label: "Bad",
		Build: func(serverprofile.Bindings) ([]serverprofile.RuleTemplate, error) {
			return []serverprofile.RuleTemplate{
				{Outcome: serverprofile.Allow, BrowseMethods: "GET", BrowsePath: "/**"},
				{Outcome: "bogus"}, // invalid → CreateMany rolls the whole batch back
			}, nil
		},
	}}
}
```

> Import `context`, `time`, `internal/approval`, `internal/serverprofile` in the test as needed (most are already imported). `mustUpstreamProfiled`/`mustAgent` exist from the presets plan.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run TestApprovePresetFanoutIsAtomic -v`
Expected: FAIL — with the current per-rule `Create` loop the first (valid) rule is committed before the second fails, so `ForUpstream` returns 1 rule, not 0.

- [ ] **Step 3: Write minimal implementation**

In `approvePreset`, replace the per-template `Create` loop with a single `CreateMany`:

```go
	rules := make([]policy.Rule, 0, len(tmpls))
	for _, t := range tmpls {
		rules = append(rules, policy.Rule{
			SubjectAgentID: p.AgentID, UpstreamID: p.UpstreamID, Outcome: t.Outcome,
			BrowseMethods: t.BrowseMethods, BrowsePath: t.BrowsePath,
			Profile: t.Profile, ProfileParams: t.ProfileParams,
		})
	}
	if _, err := d.policy.CreateMany(rules); err != nil {
		return fmt.Errorf("create preset rules: %w", err)
	}
	return nil
```

In `approveK8sAccess`, collect the missing tuples into a slice and write them in one batch (keep the existing `exists` idempotency filter):

```go
	missing := make([]policy.Rule, 0, len(grants))
	for _, g := range grants {
		if exists(g) {
			continue
		}
		missing = append(missing, policy.Rule{
			SubjectAgentID: p.AgentID, UpstreamID: p.UpstreamID, Outcome: policy.Allow,
			Namespace: g.Namespace, Resource: g.Resource, Verb: g.Verb,
		})
	}
	if len(missing) == 0 {
		return nil
	}
	if _, err := d.policy.CreateMany(missing); err != nil {
		return fmt.Errorf("create k8s rules: %w", err)
	}
	return nil
```

> Replace ONLY the create-loop bodies; keep each function's preceding logic (preset resolve/validate/build; k8s rules-load/`exists`/`grants` fallback) unchanged.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/daemon/ -race`
Expected: PASS (the new atomicity test + the existing preset/k8s approve tests).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/daemon/
git add internal/daemon/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(daemon): atomic fan-out — approvePreset/approveK8sAccess use CreateMany"
```

---

### Task 3: ADR-0038 + full gate + push

**Files:**
- Create: `docs/architecture/decisions/0038-atomic-rule-fanout.md`
- Modify: `docs/INDEX.md`, `docs/architecture/decisions/0037-presets-first-class.md`

- [ ] **Step 1: Run the full gate**

```bash
gofmt -w . && go vet ./... && go test ./... -race && CGO_ENABLED=0 go build ./...
go build -tags desktop -o /tmp/owd ./cmd/outwall-desktop
```
Expected: all green. (No web changes — skip the web build.)

- [ ] **Step 2: Write ADR-0038**

Create `docs/architecture/decisions/0038-atomic-rule-fanout.md` (status accepted, date 2026-06-23). Cover: the per-rule `Create`-loop fan-out (ADR-0037 preset, ADR-0029 k8s) could leave a partial grant on a mid-loop DB error; the fix is `policy.Registry.CreateMany`, a single transaction (all-or-nothing), with `Create`/`CreateMany` sharing one `insertRule(rowExecutor, Rule)` helper (also centralizing the INSERT, removing the column-order-drift hazard); both fan-out sites now call it. Note this supersedes the "known limitation" in ADR-0037. Link ADR-0037, ADR-0029.

- [ ] **Step 3: Link + reconcile docs**

Add the ADR-0038 line to `docs/INDEX.md`. In `docs/architecture/decisions/0037-presets-first-class.md`, update the "Fan-out atomicity (known limitation)" note to say it is now resolved by ADR-0038 (transactional `CreateMany`).

- [ ] **Step 4: Commit + push**

```bash
git add docs/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "docs(adr-0038): atomic rule fan-out via transactional CreateMany"
git push origin main
```

---

## Self-review notes

- **Spec coverage:** transactional CreateMany → Task 1; both fan-out sites → Task 2; ADR/docs/gate/push → Task 3.
- **DRY/risk:** the 18-column INSERT now lives ONLY in `insertRule` — removes the multi-site column-order hazard noted earlier; `Create` and `CreateMany` share it.
- **Atomicity proof:** Task 1 asserts a bad rule in a batch rolls back the good one (DB-level); Task 2 asserts the same through the real `approvePreset` path via a fake profile that Builds a valid+invalid pair.
- **Non-breaking:** `Create` keeps its signature and behavior; `CreateMany` is additive; the k8s `exists` idempotency filter is preserved; an empty missing-set is a no-op (no empty tx churn).
- **Placeholder scan:** none — every step has concrete code/commands.
