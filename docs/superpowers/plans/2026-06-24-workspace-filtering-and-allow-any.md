# Workspace `*` grants + mode-aware read-query workspace filtering — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let agents/operators grant `workspace: "*"` (all workspaces), and make Citeck read-query
authorization mode-aware — for browser (cookie) requests, narrow a multi-workspace query to the
allowed subset (or return an empty result) instead of a blanket `403`; API (bearer) requests stay
strict.

**Architecture:** All new logic lives in the citeck server-profile plugin. The `Profile` interface's
per-rule `Match` is replaced by a single `Authorize(AuthInput) (AuthResult, error)` that owns the full
decision and can return a rewritten request body or a synthetic response. The policy engine adapts
(`decideProfile`), and the proxy applies the result (swap body / return synthetic 200). The core never
learns about workspaces — only the opaque signals `Browser bool` (in) and `RewriteBody []byte` /
`Response []byte` (out).

**Tech Stack:** Go, `encoding/json`, `stretchr/testify`, `modernc.org/sqlite` (unaffected here).

## Global Constraints

- Module path is exactly `github.com/Sipaha/outwall` in every import.
- No `citeck` strings/imports/branding outside `internal/serverprofile/citeck` and the persisted
  profile name `"citeck"`. The core (serverprofile, policy, proxy) stays citeck-free — new fields are
  profile-neutral (`Browser`, `RewriteBody`, `Response`), never named after workspaces.
- No CGO (`CGO_ENABLED=0`). No panics / `log.Fatal` in library code — return wrapped errors.
- No `Co-Authored-By` in commits. No `git commit --amend` / no rewriting commits — always new commits.
- `gofmt` (tabs), `go vet` clean. `log/slog` for logging, `stretchr/testify` for tests.
- Before each commit run: `make fmt`, `make vet`, `make test`. Fix failures, don't ignore them.
- Alpha: no storage-format compat to preserve; breaking the `Profile` interface is fine (update all
  implementers in the same commit so the tree always builds).

**Design source of truth:** `docs/superpowers/specs/2026-06-24-workspace-filtering-and-allow-any-design.md`
and `docs/architecture/decisions/0039-workspace-filtering-and-allow-any.md`.

---

### Task 1: Allow `workspace: "*"` in citeck presets (R1)

The `workspace` preset slot is `AllowAny: false`, so `ValidateBindings` rejects `"*"` for both the
agent (`request_preset`) and the operator (approval). The rule layer already treats `""`/`"*"` as
"any workspace", so this is a one-field flip plus a guard test.

**Files:**
- Modify: `internal/serverprofile/citeck/citeck.go:28`
- Test: `internal/serverprofile/citeck/preset_test.go`

**Interfaces:**
- Consumes: `serverprofile.ValidateBindings(slots, Bindings)` (existing).
- Produces: citeck presets whose `workspace` slot has `AllowAny: true`.

- [ ] **Step 1: Write the failing test**

Add to `internal/serverprofile/citeck/preset_test.go`:

```go
func TestWorkspaceSlotAllowsStar(t *testing.T) {
	for _, id := range []string{"citeck-readonly", "citeck-readwrite"} {
		p, ok := findPreset(t, id)
		require.True(t, ok, "preset %s present", id)
		// "*" must now be a valid binding for both sourceId and workspace.
		err := serverprofile.ValidateBindings(p.Slots, serverprofile.Bindings{"sourceId": "*", "workspace": "*"})
		require.NoError(t, err, "preset %s should accept workspace=*", id)
	}
}

// findPreset returns the citeck preset with the given id.
func findPreset(t *testing.T, id string) (serverprofile.Preset, bool) {
	t.Helper()
	for _, p := range New().Presets() {
		if p.ID == id {
			return p, true
		}
	}
	return serverprofile.Preset{}, false
}
```

If `preset_test.go` already defines a `findPreset`/equivalent helper, reuse it and drop the duplicate.
Confirm the existing imports include `require` and `serverprofile`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serverprofile/citeck/ -run TestWorkspaceSlotAllowsStar -v`
Expected: FAIL — `slot "workspace" does not allow "*"`.

- [ ] **Step 3: Flip the slot**

In `internal/serverprofile/citeck/citeck.go`, change the `workspace` slot:

```go
		{Key: "workspace", Label: "Workspace", Type: "text", AllowAny: true, Required: true},
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/serverprofile/citeck/ -run TestWorkspaceSlotAllowsStar -v`
Expected: PASS.
Also run the package to catch any preset test that asserted the old rejection:
Run: `go test ./internal/serverprofile/... -v`
Expected: PASS (if an existing test asserted `workspace: "*"` is rejected, update it to expect
acceptance — that assertion encoded the old ADR-0037 rule now superseded by ADR-0039).

- [ ] **Step 5: Commit**

```bash
make fmt && make vet
git add internal/serverprofile/citeck/citeck.go internal/serverprofile/citeck/preset_test.go
git commit -m "feat(citeck): allow workspace=* in presets (R1, ADR-0039)"
```

---

### Task 2: Replace `Profile.Match` with `Profile.Authorize` (behavior-preserving migration)

Change the interface and plumb the new in/out signals, with **zero behavior change**: citeck's
`Authorize` reproduces the current all-or-nothing semantics; `RewriteBody`/`Response`/`Browser` are
carried but unused. Filtering arrives in Task 3; proxy application in Task 4.

**Files:**
- Modify: `internal/serverprofile/serverprofile.go` (types + interface)
- Modify: `internal/serverprofile/citeck/citeck.go` (`Match` → `Authorize`, legacy resolver)
- Modify: `internal/policy/decide.go` (`Input.Browser`, `Decision.RewriteBody`/`Response`,
  `decideProfile` adapter)
- Modify: `internal/proxy/proxy.go:232-233` (thread `viaCookie` → `Input.Browser`)
- Modify (test fakes): `internal/serverprofile/serverprofile_test.go:13`,
  `internal/policy/decide_profile_test.go:27`, `internal/policy/decide_browse_test.go:48`,
  `internal/mcpsvc/service_test.go:32`, `internal/daemon/admin_test.go:843`
- Test: `internal/policy/decide_profile_test.go` (existing cases must stay green via the adapter)

**Interfaces:**
- Produces (consumed by Tasks 3–4):
  ```go
  // internal/serverprofile
  type AuthInput struct {
      Op      Operation // classified operation (from Classify)
      Body    []byte    // raw request body (for rewrite)
      Browser bool      // request authenticated via browser cookie
      Agent   []Rule    // this profile's rules for the requesting agent (agent tier)
      Any     []Rule    // this profile's any-agent rules (subject "")
  }
  type AuthResult struct {
      Outcome     string // Allow | Deny | RequireApproval
      RuleID      string // deciding rule id, for audit ("" for synthesized)
      RewriteBody []byte // non-nil → proxy forwards this body instead of the original
      Response    []byte // non-nil → proxy returns 200 application/json with this body, no upstream call
  }
  type Profile interface {
      Name() string
      Classify(req Request) (op Operation, handled bool, err error)
      Authorize(in AuthInput) (AuthResult, error) // replaces Match
      RuleSchema() RuleSchema
      Presets() []Preset
  }
  ```
  ```go
  // internal/policy
  // Input gains:
  Browser bool
  // Decision gains:
  RewriteBody []byte
  Response    []byte
  ```

- [ ] **Step 1: Add the new types and change the interface in `serverprofile.go`**

In `internal/serverprofile/serverprofile.go`, replace the `Match` line in the `Profile` interface and
add the two new types just above the interface:

```go
// AuthInput carries everything a profile needs to authorize a classified operation: the operation,
// the raw body (for rewrite), the request mode, and the agent's candidate rules split by tier.
type AuthInput struct {
	Op      Operation
	Body    []byte
	Browser bool
	Agent   []Rule
	Any     []Rule
}

// AuthResult is a profile's full authorization decision for one operation. At most one of
// RewriteBody / Response is non-nil; both nil means "forward the original request".
type AuthResult struct {
	Outcome     string // Allow | Deny | RequireApproval
	RuleID      string // deciding rule id (for audit); "" when synthesized
	RewriteBody []byte // forward this body instead of the original
	Response    []byte // return 200 application/json with this body; do not contact the upstream
}
```

Change the interface method:

```go
	// Authorize evaluates a handled operation against the agent's candidate rules for this profile
	// (split into Agent / Any tiers) and returns the outcome. A profile may additionally return a
	// rewritten request body or a synthetic response (e.g. to narrow a multi-valued request).
	Authorize(in AuthInput) (AuthResult, error)
```

Remove the old `Match(rule Rule, op Operation) (outcome string, matched bool, err error)` line.

- [ ] **Step 2: Update the trivial serverprofile test fake**

In `internal/serverprofile/serverprofile_test.go:13`, replace the `Match` method:

```go
func (f fakeProfile) Authorize(AuthInput) (AuthResult, error) { return AuthResult{Outcome: Allow}, nil }
```

- [ ] **Step 3: Rewrite citeck `Match` → `Authorize` (legacy semantics only)**

In `internal/serverprofile/citeck/citeck.go`, delete the `Match` method (lines 84-101) and replace it
with the legacy resolver below. This reproduces the current "every touched resource must pass" +
tier-precedence behavior exactly. `matchSource`, `matchWorkspace`, `matchGlob`, `nonNil`, `ruleParams`
stay as they are.

```go
// Authorize resolves a classified operation against the agent's citeck rules. In this task it only
// reproduces the legacy all-or-nothing decision; read-query filtering is added in a later task.
func (profile) Authorize(in serverprofile.AuthInput) (serverprofile.AuthResult, error) {
	outcome, ruleID := resolveLegacy(in.Op, in.Agent, in.Any)
	return serverprofile.AuthResult{Outcome: outcome, RuleID: ruleID}, nil
}

// ruleMatches reports whether a citeck rule's params structurally match the operation: op-kind gate
// plus every touched (source, scope) passing the rule's source/workspace globs. (Mirrors the former
// Match's structural test, independent of the rule's outcome.)
func ruleMatches(r serverprofile.Rule, op serverprofile.Operation) bool {
	var p ruleParams
	if err := json.Unmarshal(nonNil(r.Params), &p); err != nil {
		return false // a malformed rule never grants
	}
	if p.Op != "" && p.Op != op.Kind {
		return false
	}
	if len(op.Resources) == 0 {
		return false
	}
	for _, res := range op.Resources {
		if !matchSource(p.SourceID, res.Resource) || !matchWorkspace(p.Workspace, res.Scope) {
			return false
		}
	}
	return true
}

// resolveLegacy applies tier precedence (agent tier outranks any tier; within a tier deny >
// require-approval > allow) over the rules that structurally match op. Returns the winning outcome
// and its rule id, or (Deny, "") on default-deny.
func resolveLegacy(op serverprofile.Operation, agent, any []serverprofile.Rule) (string, string) {
	pick := func(rules []serverprofile.Rule) (string, string, bool) {
		var allow, approval, deny *serverprofile.Rule
		for i := range rules {
			if !ruleMatches(rules[i], op) {
				continue
			}
			switch rules[i].Outcome {
			case serverprofile.Deny:
				if deny == nil {
					deny = &rules[i]
				}
			case serverprofile.RequireApproval:
				if approval == nil {
					approval = &rules[i]
				}
			case serverprofile.Allow:
				if allow == nil {
					allow = &rules[i]
				}
			}
		}
		switch {
		case deny != nil:
			return serverprofile.Deny, deny.ID, true
		case approval != nil:
			return serverprofile.RequireApproval, approval.ID, true
		case allow != nil:
			return serverprofile.Allow, allow.ID, true
		default:
			return "", "", false
		}
	}
	if o, id, ok := pick(agent); ok {
		return o, id
	}
	if o, id, ok := pick(any); ok {
		return o, id
	}
	return serverprofile.Deny, ""
}
```

- [ ] **Step 4: Update `policy.Input`, `policy.Decision`, and `decideProfile`**

In `internal/policy/decide.go`:

Add to `Input` (after the `Body []byte` field, ~line 28):

```go
	// Browser is true when the request authenticated via the browser cookie (vs the bearer header).
	// Profiles may use it to choose a lenient (narrow/empty) vs strict (deny) outcome.
	Browser bool
```

Add to `Decision` (after `NewValues`, ~line 50):

```go
	// Profile-driven request/response rewrites (citeck workspace filtering). At most one is set.
	RewriteBody []byte // forward this body instead of the original
	Response    []byte // return 200 application/json with this body; skip the upstream
```

Replace `decideProfile` (lines 180-211) with the adapter:

```go
// decideProfile delegates the whole decision for a handled operation to the profile's Authorize,
// passing the profile's rules split by subject tier, then maps the AuthResult into a Decision.
func decideProfile(prof serverprofile.Profile, in Input, op serverprofile.Operation, rules []*Rule) (Decision, error) {
	var agent, any []serverprofile.Rule
	byID := map[string]*Rule{}
	for _, rule := range rules {
		if rule.Profile != in.Profile {
			continue
		}
		sr := serverprofile.Rule{ID: rule.ID, Outcome: rule.Outcome, Params: rule.ProfileParams}
		switch rule.SubjectAgentID {
		case in.AgentID:
			agent = append(agent, sr)
			byID[rule.ID] = rule
		case "":
			any = append(any, sr)
			byID[rule.ID] = rule
		}
	}
	ar, err := prof.Authorize(serverprofile.AuthInput{
		Op: op, Body: in.Body, Browser: in.Browser, Agent: agent, Any: any,
	})
	if err != nil {
		return Decision{}, err
	}
	return Decision{
		Outcome:     ar.Outcome,
		Rule:        byID[ar.RuleID], // nil for synthesized/default-deny outcomes
		Vars:        opVars(op),
		RewriteBody: ar.RewriteBody,
		Response:    ar.Response,
	}, nil
}
```

- [ ] **Step 5: Thread `viaCookie` into the policy input (proxy)**

In `internal/proxy/proxy.go`, update the `Decide` call (lines 232-233) to set `Browser`:

```go
		dec, err = h.Policy.Decide(policy.Input{AgentID: ag.ID, UpstreamID: up.ID, Profile: up.Profile,
			Method: r.Method, Path: escRelPath, Query: r.URL.Query(), Body: bodyBytes, Browser: viaCookie})
```

(`viaCookie` is already in scope from line 117.)

- [ ] **Step 6: Update the remaining test fakes (`Match` → `Authorize`)**

`internal/policy/decide_profile_test.go:27` — this fake has real test logic. Replace its `Match` with
an `Authorize` that uses the same tier/precedence shape the adapter now expects. Read the current
`fakeProf.Match` body; reimplement equivalently:

```go
func (f fakeProf) Authorize(in serverprofile.AuthInput) (serverprofile.AuthResult, error) {
	// Mirror the old per-rule Match over both tiers with deny > allow precedence.
	pick := func(rules []serverprofile.Rule) (string, string, bool) {
		var allow, deny *serverprofile.Rule
		for i := range rules {
			out, matched := f.matchOne(rules[i], in.Op) // port the old Match body into matchOne
			if !matched {
				continue
			}
			switch out {
			case serverprofile.Deny:
				if deny == nil {
					deny = &rules[i]
				}
			case serverprofile.Allow:
				if allow == nil {
					allow = &rules[i]
				}
			}
		}
		switch {
		case deny != nil:
			return serverprofile.Deny, deny.ID, true
		case allow != nil:
			return serverprofile.Allow, allow.ID, true
		default:
			return "", "", false
		}
	}
	if o, id, ok := pick(in.Agent); ok {
		return serverprofile.AuthResult{Outcome: o, RuleID: id}, nil
	}
	if o, id, ok := pick(in.Any); ok {
		return serverprofile.AuthResult{Outcome: o, RuleID: id}, nil
	}
	return serverprofile.AuthResult{Outcome: serverprofile.Deny}, nil
}
```

Move the body of the old `fakeProf.Match` into a helper `matchOne(serverprofile.Rule, serverprofile.Operation) (string, bool)`. If the existing test asserted a specific `dec.Rule`, the adapter sets `Decision.Rule` from `RuleID`, so keep `RuleID` accurate.

`internal/policy/decide_browse_test.go:48` (`browseOnlyProf`):

```go
func (browseOnlyProf) Authorize(_ serverprofile.AuthInput) (serverprofile.AuthResult, error) {
	return serverprofile.AuthResult{Outcome: serverprofile.Deny}, nil
}
```

`internal/mcpsvc/service_test.go:32` (`fakeProfile`):

```go
func (fakeProfile) Authorize(_ serverprofile.AuthInput) (serverprofile.AuthResult, error) {
	return serverprofile.AuthResult{Outcome: serverprofile.Allow}, nil
}
```

`internal/daemon/admin_test.go:843` (`atomicFakeProfile`):

```go
func (atomicFakeProfile) Authorize(serverprofile.AuthInput) (serverprofile.AuthResult, error) {
	return serverprofile.AuthResult{Outcome: serverprofile.Allow}, nil
}
```

Delete each fake's old `Match` method.

- [ ] **Step 7: Build and run the full suite — verify no behavior change**

Run: `make vet`
Expected: clean (no "missing method Authorize", no "unknown field").
Run: `make test`
Expected: PASS — all existing citeck/policy/proxy/mcpsvc/daemon tests green. (Any test that called
`prof.Match(...)` directly must be switched to `prof.Authorize(...)`; search with
`grep -rn "\.Match(" internal/*/*_test.go` and update.)

- [ ] **Step 8: Commit**

```bash
make fmt && make vet && make test
git add internal/serverprofile/serverprofile.go internal/serverprofile/serverprofile_test.go \
        internal/serverprofile/citeck/citeck.go internal/policy/decide.go internal/proxy/proxy.go \
        internal/policy/decide_profile_test.go internal/policy/decide_browse_test.go \
        internal/mcpsvc/service_test.go internal/daemon/admin_test.go
git commit -m "refactor(profile): replace Match with Authorize; plumb Browser/RewriteBody/Response (no behavior change)"
```

---

### Task 3: Citeck read-query workspace filtering (R2–R4)

Add the mode-aware filtering inside `citeck.Authorize`: when the legacy decision for a filterable read
query is `Deny`, narrow (browser) / deny (API) / inject (absent workspaces) / synth-empty (browser,
nothing allowed) per the spec matrix. Legacy `Allow`/`RequireApproval` and all non-read ops are
untouched.

**Files:**
- Modify: `internal/serverprofile/citeck/citeck.go` (`Authorize` branch)
- Create: `internal/serverprofile/citeck/filter.go` (filtering helpers + body rewrite + synth response)
- Modify: `internal/serverprofile/citeck/records.go:1-4` (extend package doc to mention filtering)
- Test: `internal/serverprofile/citeck/filter_test.go`

**Interfaces:**
- Consumes: `serverprofile.AuthInput` (Task 2), `ruleParams`, `matchSource`, `matchWorkspace`,
  `nonNil`, `scopeAll`, `scopeUnknown` (existing in the package).
- Produces: `serverprofile.AuthResult` with `RewriteBody` or `Response` set.

- [ ] **Step 1: Write failing tests for the full matrix**

Create `internal/serverprofile/citeck/filter_test.go`. Use `New().Authorize(...)` with hand-built
`AuthInput`s. Helper to build a read-allow rule:

```go
package citeck

import (
	"encoding/json"
	"testing"

	"github.com/Sipaha/outwall/internal/serverprofile"
	"github.com/stretchr/testify/require"
)

func readRule(id, source, ws string) serverprofile.Rule {
	p, _ := json.Marshal(ruleParams{Op: "read", SourceID: source, Workspace: ws})
	return serverprofile.Rule{ID: id, Outcome: serverprofile.Allow, Params: p}
}

// queryBody builds a /records/query body with the given sourceId and workspaces (nil = absent).
func queryBody(sourceID string, workspaces []string) []byte {
	q := map[string]any{"sourceId": sourceID}
	if workspaces != nil {
		q["workspaces"] = workspaces
	}
	b, _ := json.Marshal(map[string]any{"query": q})
	return b
}

func authorizeQuery(t *testing.T, sourceID string, workspaces []string, browser bool, agent ...serverprofile.Rule) serverprofile.AuthResult {
	t.Helper()
	body := queryBody(sourceID, workspaces)
	op, handled, err := classify(serverprofile.Request{Method: "POST", Path: "/api/records/query", Body: body})
	require.NoError(t, err)
	require.True(t, handled)
	ar, err := New().Authorize(serverprofile.AuthInput{Op: op, Body: body, Browser: browser, Agent: agent})
	require.NoError(t, err)
	return ar
}

func TestFilter_AllAllowed_ForwardUnchanged(t *testing.T) {
	ar := authorizeQuery(t, "emodel/person", []string{"a", "b"}, true,
		readRule("r1", "*", "a"), readRule("r2", "*", "b"))
	require.Equal(t, serverprofile.Allow, ar.Outcome)
	require.Nil(t, ar.RewriteBody)
	require.Nil(t, ar.Response)
}

func TestFilter_PartialAllowed_Browser_Rewrites(t *testing.T) {
	ar := authorizeQuery(t, "emodel/person", []string{"a", "b", "c"}, true, readRule("r1", "*", "b"))
	require.Equal(t, serverprofile.Allow, ar.Outcome)
	require.NotNil(t, ar.RewriteBody)
	require.Equal(t, []string{"b"}, workspacesOf(t, ar.RewriteBody))
}

func TestFilter_PartialAllowed_API_Denies(t *testing.T) {
	ar := authorizeQuery(t, "emodel/person", []string{"a", "b"}, false, readRule("r1", "*", "b"))
	require.Equal(t, serverprofile.Deny, ar.Outcome)
	require.Nil(t, ar.RewriteBody)
}

func TestFilter_NoneAllowed_Browser_EmptyResult(t *testing.T) {
	ar := authorizeQuery(t, "emodel/person", []string{"a", "b"}, true, readRule("r1", "*", "z"))
	require.Equal(t, serverprofile.Allow, ar.Outcome)
	require.NotNil(t, ar.Response)
	require.JSONEq(t, `{"records":[],"hasMore":false,"totalCount":0,"messages":[],"version":1}`, string(ar.Response))
}

func TestFilter_NoneAllowed_API_Denies(t *testing.T) {
	ar := authorizeQuery(t, "emodel/person", []string{"a", "b"}, false, readRule("r1", "*", "z"))
	require.Equal(t, serverprofile.Deny, ar.Outcome)
}

func TestFilter_AbsentWorkspaces_ConcreteGrants_Injects(t *testing.T) {
	ar := authorizeQuery(t, "emodel/person", nil, false,
		readRule("r1", "*", "a"), readRule("r2", "*", "b"))
	require.Equal(t, serverprofile.Allow, ar.Outcome)
	require.NotNil(t, ar.RewriteBody)
	require.Equal(t, []string{"a", "b"}, workspacesOf(t, ar.RewriteBody)) // sorted
}

func TestFilter_AbsentWorkspaces_StarGrant_ForwardUnchanged(t *testing.T) {
	ar := authorizeQuery(t, "emodel/person", nil, false, readRule("r1", "*", "*"))
	require.Equal(t, serverprofile.Allow, ar.Outcome)
	require.Nil(t, ar.RewriteBody)
	require.Nil(t, ar.Response)
}

func TestFilter_AbsentWorkspaces_NoGrants_API_Denies(t *testing.T) {
	ar := authorizeQuery(t, "emodel/person", nil, false) // no rules
	require.Equal(t, serverprofile.Deny, ar.Outcome)
}

func TestFilter_AbsentWorkspaces_NoGrants_Browser_EmptyResult(t *testing.T) {
	ar := authorizeQuery(t, "emodel/person", nil, true)
	require.Equal(t, serverprofile.Allow, ar.Outcome)
	require.NotNil(t, ar.Response)
}

func TestFilter_GlobGrant_MatchesExplicit_NotInjected(t *testing.T) {
	// "user$*" authorizes an explicit "user$pavel" but is not enumerable for injection.
	ar := authorizeQuery(t, "emodel/person", []string{"user$pavel"}, true, readRule("r1", "*", "user$*"))
	require.Equal(t, serverprofile.Allow, ar.Outcome)
	require.Nil(t, ar.RewriteBody) // all requested allowed → forward unchanged

	inj := authorizeQuery(t, "emodel/person", nil, true, readRule("r1", "*", "user$*"))
	require.Equal(t, serverprofile.Allow, inj.Outcome)
	require.NotNil(t, inj.Response) // nothing concrete to inject, browser → empty
}

func TestFilter_RewritePreservesSiblingFields(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"query":      map[string]any{"sourceId": "emodel/person", "language": "predicate", "workspaces": []string{"a", "b"}},
		"attributes": map[string]any{"disp": "?disp"},
	})
	op, _, _ := classify(serverprofile.Request{Method: "POST", Path: "/api/records/query", Body: body})
	ar, err := New().Authorize(serverprofile.AuthInput{Op: op, Body: body, Browser: true, Agent: []serverprofile.Rule{readRule("r1", "*", "a")}})
	require.NoError(t, err)
	require.NotNil(t, ar.RewriteBody)
	var got map[string]any
	require.NoError(t, json.Unmarshal(ar.RewriteBody, &got))
	require.Equal(t, map[string]any{"disp": "?disp"}, got["attributes"])
	q := got["query"].(map[string]any)
	require.Equal(t, "predicate", q["language"])
	require.Equal(t, []any{"a"}, q["workspaces"])
}

func TestFilter_WriteUnchanged(t *testing.T) {
	// A mutate op is never filtered: legacy deny stays deny even in browser mode.
	body, _ := json.Marshal(map[string]any{"records": []map[string]any{{"id": "emodel/person@new", "attributes": map[string]any{"_workspace": "a"}}}})
	op, handled, _ := classify(serverprofile.Request{Method: "POST", Path: "/api/records/mutate", Body: body})
	require.True(t, handled)
	ar, err := New().Authorize(serverprofile.AuthInput{Op: op, Body: body, Browser: true}) // no rules
	require.NoError(t, err)
	require.Equal(t, serverprofile.Deny, ar.Outcome)
	require.Nil(t, ar.RewriteBody)
	require.Nil(t, ar.Response)
}

func TestFilter_AgentDenyOverridesAnyAllow(t *testing.T) {
	denyRule := func(id, source, ws string) serverprofile.Rule {
		p, _ := json.Marshal(ruleParams{Op: "read", SourceID: source, Workspace: ws})
		return serverprofile.Rule{ID: id, Outcome: serverprofile.Deny, Params: p}
	}
	body := queryBody("emodel/person", []string{"a"})
	op, _, _ := classify(serverprofile.Request{Method: "POST", Path: "/api/records/query", Body: body})
	ar, err := New().Authorize(serverprofile.AuthInput{
		Op: op, Body: body, Browser: false,
		Agent: []serverprofile.Rule{denyRule("d1", "*", "a")},
		Any:   []serverprofile.Rule{readRule("a1", "*", "a")},
	})
	require.NoError(t, err)
	require.Equal(t, serverprofile.Deny, ar.Outcome) // agent-tier deny wins → API deny
}

// workspacesOf extracts query.workspaces from a rewritten body for assertions.
func workspacesOf(t *testing.T, body []byte) []string {
	t.Helper()
	var m struct {
		Query struct {
			Workspaces []string `json:"workspaces"`
		} `json:"query"`
	}
	require.NoError(t, json.Unmarshal(body, &m))
	return m.Query.Workspaces
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/serverprofile/citeck/ -run TestFilter -v`
Expected: FAIL — `Authorize` currently returns only the legacy outcome (no `RewriteBody`/`Response`),
so the partial/absent/none cases fail.

- [ ] **Step 3: Implement the filtering in `filter.go`**

Create `internal/serverprofile/citeck/filter.go`:

```go
package citeck

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/Sipaha/outwall/internal/serverprofile"
)

// emptyRecordsResponse is the synthetic body returned to a browser when a read query is fully
// filtered out — a valid empty Records response so the SPA renders instead of erroring.
var emptyRecordsResponse = []byte(`{"records":[],"hasMore":false,"totalCount":0,"messages":[],"version":1}`)

// filterableRead reports whether op is a workspace-filterable read query: kind "read" with at least
// one resource and no scopeUnknown scope (get-atts reads carry scopeUnknown and are never filtered).
func filterableRead(op serverprofile.Operation) bool {
	if op.Kind != "read" || len(op.Resources) == 0 {
		return false
	}
	for _, res := range op.Resources {
		if res.Scope == scopeUnknown {
			return false
		}
	}
	return true
}

// uniqueSources returns the distinct source strings the operation touches.
func uniqueSources(op serverprofile.Operation) []string {
	seen := map[string]bool{}
	var out []string
	for _, res := range op.Resources {
		if !seen[res.Resource] {
			seen[res.Resource] = true
			out = append(out, res.Resource)
		}
	}
	return out
}

// requestedScopes returns the distinct workspace scopes the operation touches, preserving order.
// For an absent workspaces filter this is exactly [scopeAll].
func requestedScopes(op serverprofile.Operation) []string {
	seen := map[string]bool{}
	var out []string
	for _, res := range op.Resources {
		if !seen[res.Scope] {
			seen[res.Scope] = true
			out = append(out, res.Scope)
		}
	}
	return out
}

// wsAllowedForRead reports whether read access to (source, ws) is granted, applying tier precedence
// (agent tier outranks any tier; within a tier deny outranks allow). require-approval rules do not
// grant here — the filtering path is only reached when the legacy decision was Deny.
func wsAllowedForRead(source, ws string, agent, any []serverprofile.Rule) bool {
	eval := func(rules []serverprofile.Rule) (allowed, matched bool) {
		var hasAllow, hasDeny bool
		for _, r := range rules {
			var p ruleParams
			if err := json.Unmarshal(nonNil(r.Params), &p); err != nil {
				continue
			}
			if p.Op != "" && p.Op != "read" {
				continue
			}
			if !matchSource(p.SourceID, source) || !matchWorkspace(p.Workspace, ws) {
				continue
			}
			switch r.Outcome {
			case serverprofile.Deny:
				hasDeny = true
			case serverprofile.Allow:
				hasAllow = true
			}
		}
		if hasDeny {
			return false, true
		}
		if hasAllow {
			return true, true
		}
		return false, false
	}
	if ok, matched := eval(agent); matched {
		return ok
	}
	if ok, matched := eval(any); matched {
		return ok
	}
	return false
}

// concreteWorkspaces collects the enumerable (non-empty, non-glob) workspace values from the agent's
// read-allow rules across both tiers, for injecting into an absent-workspaces query. Glob/`*` grants
// are not enumerable and are skipped.
func concreteWorkspaces(agent, any []serverprofile.Rule) []string {
	set := map[string]bool{}
	for _, tier := range [][]serverprofile.Rule{agent, any} {
		for _, r := range tier {
			if r.Outcome != serverprofile.Allow {
				continue
			}
			var p ruleParams
			if err := json.Unmarshal(nonNil(r.Params), &p); err != nil {
				continue
			}
			if p.Op != "" && p.Op != "read" {
				continue
			}
			if p.Workspace == "" || strings.Contains(p.Workspace, "*") {
				continue
			}
			set[p.Workspace] = true
		}
	}
	out := make([]string, 0, len(set))
	for w := range set {
		out = append(out, w)
	}
	sort.Strings(out)
	return out
}

// filterReadQuery is invoked when the legacy decision for a filterable read query was Deny. It applies
// the spec matrix and returns an AuthResult that may rewrite the body or synthesize an empty response.
func filterReadQuery(in serverprofile.AuthInput) (serverprofile.AuthResult, error) {
	sources := uniqueSources(in.Op)
	allowAll := func(ws string) bool {
		for _, s := range sources {
			if !wsAllowedForRead(s, ws, in.Agent, in.Any) {
				return false
			}
		}
		return true
	}

	scopes := requestedScopes(in.Op)
	absent := len(scopes) == 1 && scopes[0] == scopeAll

	var keep []string
	if absent {
		// Legacy was Deny, so there is no wildcard grant covering scopeAll; inject the concrete set.
		for _, w := range concreteWorkspaces(in.Agent, in.Any) {
			if allowAll(w) {
				keep = append(keep, w)
			}
		}
	} else {
		for _, w := range scopes {
			if allowAll(w) {
				keep = append(keep, w)
			}
		}
	}

	// An explicit list whose every workspace is allowed (possibly via separate rules — the legacy
	// resolver only matches when ONE rule covers all touched resources, so this case reaches here)
	// is forwarded unchanged. The absent case never takes this branch: keep is the injected concrete
	// set, which must always be written into the body.
	if !absent && len(keep) == len(scopes) {
		return serverprofile.AuthResult{Outcome: serverprofile.Allow}, nil
	}
	if len(keep) == 0 {
		if in.Browser {
			return serverprofile.AuthResult{Outcome: serverprofile.Allow, Response: emptyRecordsResponse}, nil
		}
		return serverprofile.AuthResult{Outcome: serverprofile.Deny}, nil
	}
	// A partial explicit list is narrowed only for a browser (an API caller that named workspaces
	// must not be silently narrowed). Absent-case injection always rewrites, for both modes.
	if !absent && !in.Browser {
		return serverprofile.AuthResult{Outcome: serverprofile.Deny}, nil
	}
	body, err := rewriteWorkspaces(in.Body, keep)
	if err != nil {
		return serverprofile.AuthResult{Outcome: serverprofile.Deny}, nil
	}
	return serverprofile.AuthResult{Outcome: serverprofile.Allow, RewriteBody: body}, nil
}

// rewriteWorkspaces returns body with query.workspaces replaced by ws, preserving every other field.
func rewriteWorkspaces(body []byte, ws []string) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(nonNil(body), &top); err != nil {
		return nil, err
	}
	var q map[string]json.RawMessage
	if err := json.Unmarshal(nonNil(top["query"]), &q); err != nil {
		return nil, err
	}
	wsJSON, err := json.Marshal(ws)
	if err != nil {
		return nil, err
	}
	q["workspaces"] = wsJSON
	qJSON, err := json.Marshal(q)
	if err != nil {
		return nil, err
	}
	top["query"] = qJSON
	return json.Marshal(top)
}
```

- [ ] **Step 4: Wire the filtering branch into `Authorize`**

In `internal/serverprofile/citeck/citeck.go`, update `Authorize` to branch on the legacy outcome:

```go
func (profile) Authorize(in serverprofile.AuthInput) (serverprofile.AuthResult, error) {
	outcome, ruleID := resolveLegacy(in.Op, in.Agent, in.Any)
	// Only previously-denied, filterable read queries get the new mode-aware narrowing; everything
	// else (allow, require-approval, writes, get-atts) keeps the legacy decision.
	if outcome == serverprofile.Deny && filterableRead(in.Op) {
		return filterReadQuery(in)
	}
	return serverprofile.AuthResult{Outcome: outcome, RuleID: ruleID}, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/serverprofile/citeck/ -run TestFilter -v`
Expected: PASS (all matrix cases).
Run: `go test ./internal/serverprofile/... -v`
Expected: PASS (legacy citeck tests unaffected).

- [ ] **Step 6: Update the package doc**

In `internal/serverprofile/citeck/records.go`, extend the package comment (lines 1-3) to note the new
behavior, e.g. append: `It also narrows a browser-originated read query to the agent's allowed
workspaces (or returns an empty result) instead of denying the whole request — see ADR-0039.`

- [ ] **Step 7: Commit**

```bash
make fmt && make vet && make test
git add internal/serverprofile/citeck/filter.go internal/serverprofile/citeck/filter_test.go \
        internal/serverprofile/citeck/citeck.go internal/serverprofile/citeck/records.go
git commit -m "feat(citeck): mode-aware read-query workspace filtering (R2-R4, ADR-0039)"
```

---

### Task 4: Proxy applies `Response` and `RewriteBody`

The decision can now carry a synthetic response or a rewritten body. Apply both in the proxy: return
the synthetic `200` without contacting the upstream, or swap the forwarded body (and Content-Length)
before the existing forward + audit-tee path.

**Files:**
- Modify: `internal/proxy/proxy.go` (after the `switch dec.Outcome` block, ~line 299)
- Test: `internal/proxy/proxy_test.go`

**Interfaces:**
- Consumes: `policy.Decision.Response []byte`, `policy.Decision.RewriteBody []byte` (Task 2).
- Produces: HTTP behavior — synthetic `200 application/json`; or upstream request with the narrowed
  body.

- [ ] **Step 1: Write failing proxy tests**

Add to `internal/proxy/proxy_test.go`. Follow the file's existing harness for building a handler with
a stub `Policy` and a fake upstream; if the existing tests use a real `policy.Registry`, prefer a
small stub that returns a canned `policy.Decision` so these tests isolate the proxy's apply logic.
Sketch (adapt to the file's existing helpers/fields):

```go
func TestProxy_SyntheticResponse_NoUpstreamCall(t *testing.T) {
	upstreamHit := false
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { upstreamHit = true }))
	defer up.Close()

	h := newTestHandler(t, up.URL, policy.Decision{
		Outcome:  policy.Allow,
		Response: []byte(`{"records":[],"hasMore":false,"totalCount":0,"messages":[],"version":1}`),
	})

	rec := httptest.NewRecorder()
	req := newAgentRequest(t, "POST", "/u/api/records/query", `{"query":{"sourceId":"emodel/person"}}`)
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"records":[],"hasMore":false,"totalCount":0,"messages":[],"version":1}`, rec.Body.String())
	require.False(t, upstreamHit, "upstream must not be contacted for a synthetic response")
}

func TestProxy_RewriteBody_ForwardsNarrowedBody(t *testing.T) {
	var gotBody string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	narrowed := []byte(`{"query":{"sourceId":"emodel/person","workspaces":["b"]}}`)
	h := newTestHandler(t, up.URL, policy.Decision{Outcome: policy.Allow, RewriteBody: narrowed})

	rec := httptest.NewRecorder()
	req := newAgentRequest(t, "POST", "/u/api/records/query", `{"query":{"sourceId":"emodel/person","workspaces":["a","b","c"]}}`)
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, string(narrowed), gotBody, "upstream must receive the narrowed body")
}
```

`newTestHandler`/`newAgentRequest` are illustrative — bind to whatever the existing proxy tests use
(look at the top of `proxy_test.go`). The key assertions are: synthetic ⇒ 200 + body + no upstream
hit; rewrite ⇒ upstream sees the narrowed body. If a stub Policy type doesn't exist yet, add a tiny
one implementing the `Decider`/policy interface the handler holds.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/proxy/ -run 'TestProxy_(SyntheticResponse|RewriteBody)' -v`
Expected: FAIL — synthetic returns whatever the upstream/forward path does (not the empty body); the
narrowed body is not forwarded (upstream sees the original `["a","b","c"]`).

- [ ] **Step 3: Apply the decision in the proxy**

In `internal/proxy/proxy.go`, immediately after the `switch dec.Outcome { ... }` block (after line
299, before the rate-limit check at line 300), insert:

```go
	// A profile may synthesize a response (e.g. citeck returns an empty Records result for a browser
	// query whose workspaces are all filtered out) — return it without contacting the upstream.
	if dec.Response != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, werr := w.Write(dec.Response); werr != nil {
			h.Logger.Error("write synthetic response", "err", werr)
		}
		h.recordOutcome(r, ag, up, relPath, http.StatusOK, string(dec.Outcome), dec.Rule, "")
		return
	}
	// A profile may rewrite the request body (e.g. narrow query.workspaces to the allowed subset).
	// Swap it in before the forward + audit tee so the upstream and the audit log see the sent body.
	if dec.RewriteBody != nil {
		r.Body = io.NopCloser(bytes.NewReader(dec.RewriteBody))
		r.ContentLength = int64(len(dec.RewriteBody))
		r.Header.Set("Content-Length", strconv.Itoa(len(dec.RewriteBody)))
	}
```

Confirm `strconv` is imported in `proxy.go` (it is — used elsewhere; if not, add it). `bytes` and `io`
are already imported.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/proxy/ -run 'TestProxy_(SyntheticResponse|RewriteBody)' -v`
Expected: PASS.
Run: `go test ./internal/proxy/... -v`
Expected: PASS (no regression in existing proxy/audit tests).

- [ ] **Step 5: Commit**

```bash
make fmt && make vet && make test
git add internal/proxy/proxy.go internal/proxy/proxy_test.go
git commit -m "feat(proxy): apply profile Response (synthetic 200) and RewriteBody (narrowed forward)"
```

---

### Task 5: End-to-end verification, docs, and ADR status

Confirm the whole feature works through the daemon, finalize docs, and run the full gate.

**Files:**
- Modify: `docs/architecture/decisions/0039-workspace-filtering-and-allow-any.md` (status → accepted)
- Modify: `docs/INDEX.md` if it lists ADRs/specs (add 0039 + the spec) — check first.
- Test: `internal/daemon/*_test.go` — add one end-to-end case if the daemon test harness supports it
  (optional; the citeck blank-import test exception per ADR-0034 already exists in the daemon test
  package).

**Interfaces:** none new.

- [ ] **Step 1: Optional end-to-end daemon test**

If `internal/daemon` has a test that drives a citeck upstream end-to-end (search:
`grep -rn "citeck" internal/daemon/*_test.go`), add a case: agent with a concrete `workspace`
read grant issues a browser (cookie) `/records/query` spanning that workspace plus another → assert
`200` and that the forwarded body's `query.workspaces` was narrowed to the granted one. If no such
harness exists, skip — Tasks 3 and 4 already cover the logic at unit level; note the skip here.

- [ ] **Step 2: Grep for leftover `Match` references**

Run: `grep -rn "\.Match(" internal/ --include=*.go | grep -i profile`
Expected: no profile-`Match` calls remain (optemplate's `Template.Match` is unrelated and stays).
Run: `grep -rn "citeck" internal/policy internal/proxy internal/serverprofile/serverprofile.go`
Expected: no matches — the core stayed citeck-free.

- [ ] **Step 3: Full local gate**

Run: `make fmt && make vet && make test`
Expected: all PASS.

- [ ] **Step 4: Manual smoke (optional, if the app is running)**

Rebuild and re-run (`make run-server`), then through outwall as a browser (cookie) request, issue a
`/records/query` with no `workspaces` under a concrete grant and confirm it no longer 403s. (This is
the original `enterprise.ecos24.ru` SPA scenario.) Record the result; do not block the task on a
running environment.

- [ ] **Step 5: Flip ADR status and finalize docs**

In `docs/architecture/decisions/0039-workspace-filtering-and-allow-any.md`, change
`Status: proposed` → `Status: accepted`. Update `docs/INDEX.md` if it enumerates ADRs/specs.

- [ ] **Step 6: Commit**

```bash
make fmt && make vet && make test
git add -A docs/ internal/daemon/
git commit -m "docs(policy): ADR-0039 accepted; workspace filtering verified end-to-end"
```

---

## Self-Review

**Spec coverage:**
- R1 (`workspace: *`) → Task 1. ✓
- Mode-aware matrix (R2), read-only filtering (R3), absent→inject (R4) → Task 3 (citeck) + Task 4
  (proxy apply). ✓
- Mechanism `Profile.Authorize` + `RewriteBody`/`Response`/`Browser` → Task 2. ✓
- Proxy synthetic response + body rewrite + `viaCookie→Browser` → Task 2 (thread) + Task 4 (apply). ✓
- Writes/delete/get-atts unchanged → Task 2 (legacy resolver) + Task 3 (`filterableRead` excludes
  them; `TestFilter_WriteUnchanged`). ✓
- Core stays citeck-free → Task 5 Step 2 grep gate. ✓
- ADR/spec written → already committed; status flipped in Task 5. ✓

**Placeholder scan:** No TBD/TODO. Test bodies and implementation code are provided in full. The only
"adapt to existing harness" notes are in Task 4 Step 1 (proxy test helpers) and Task 2 Step 6
(`fakeProf.matchOne`), where the exact helper names must match the current test files — flagged
explicitly with the grep/read to perform.

**Type consistency:** `AuthInput{Op,Body,Browser,Agent,Any}`, `AuthResult{Outcome,RuleID,RewriteBody,
Response}`, `Decision{...,RewriteBody,Response}`, `Input{...,Browser}` are used identically across
Tasks 2–4. `resolveLegacy`/`filterReadQuery`/`wsAllowedForRead`/`concreteWorkspaces`/`rewriteWorkspaces`/
`filterableRead`/`uniqueSources`/`requestedScopes` are defined once (Tasks 2–3) and referenced
consistently. `emptyRecordsResponse` bytes match between the citeck test (Task 3) and the proxy test
(Task 4).
