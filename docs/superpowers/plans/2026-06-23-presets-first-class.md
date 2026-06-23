# Presets as a first-class concept — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a **preset** a first-class, plugin-defined bundle of related rights with typed variable slots that an **agent requests** (per upstream) and the **operator approves** (optionally narrowing slot values), fanning out into ordinary `policy.Rule`s **bound to the requesting agent**.

**Architecture:** A preset is declared in code by a server-profile plugin (plus a small core catalog for generic http). It expands, via a `Build(Bindings)` func, into profile-neutral `RuleTemplate`s (the daemon maps these to `policy.Rule`s — `serverprofile` must NOT import `policy`, since `policy` imports `serverprofile`). Discovery enriches the MCP `list_upstreams`; a new `request_preset` tool enqueues a `KindPreset` approval; on approve the daemon re-validates the (possibly operator-edited) bindings and creates the rules. Reuses the existing `access_requests` log + `approval.Queue` — no new tables.

**Tech Stack:** Go (CGO-free server), `modernc.org/sqlite`, `stretchr/testify`; React + Vite + Vitest + Testing Library. Spec: `docs/superpowers/specs/2026-06-23-presets-first-class-design.md`.

## Global Constraints

- Module path exactly `github.com/Sipaha/outwall` in every import.
- **No `citeck`** in the core — only inside `internal/serverprofile/citeck` + the `"citeck"` profile-name value (ADR-0034). `serverprofile`, `mcpsvc`, `mcp`, `approval`, `daemon`, `policy`, and UI chrome stay citeck-free (the web `=== 'citeck'`/`profile:'citeck'` value is the allowed profile-name value).
- **`serverprofile` must not import `policy`** (would be an import cycle). Presets expand to `serverprofile.RuleTemplate` (neutral); the daemon maps to `policy.Rule`.
- No CGO in the server binary (`CGO_ENABLED=0`). No panics in library code (wrapped errors, `%w`). `log/slog`.
- Commit author MUST be `Sipaha <sipahabk@gmail.com>`: `git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit`. No `Co-Authored-By`. No amend. Branch `main`.
- Gate before each commit: `gofmt -w .`, `go vet ./...`, `go test ./... -race`, `CGO_ENABLED=0 go build ./...`. Web tasks also `cd web && pnpm test && pnpm lint && pnpm build`, then `git checkout -- internal/daemon/webdist/index.html` (pnpm build rewrites that tracked file — it must NOT be staged).
- Builds on the shipped browse-rule primitive (`policy.Rule.BrowseMethods`/`BrowsePath`, ADR-0036) and the server-profile registry (`RuleSchema`, ADR-0034); the fan-out mirrors the k8s multi-grant flow (`KindK8sAccess`, ADR-0029).

## File structure (created / modified)

- **Modify** `internal/serverprofile/serverprofile.go` — `Preset`/`PresetSlot`/`Bindings`/`RuleTemplate` types; `Profile.Presets()` interface method; `ValidateBindings`; `CoreHTTPPresets`; `AvailablePresets`; `FindPreset`.
- **Modify** `internal/serverprofile/serverprofile_test.go` + `internal/policy/decide_profile_test.go` — add `Presets()` to their test fakes (interface grew).
- **Modify** `internal/serverprofile/citeck/citeck.go` (+ test) — `Presets()` returning `ReadOnly` + `ReadWrite`.
- **Modify** `internal/approval/queue.go` — `KindPreset` + `Pending.PresetID` + `Pending.Bindings`.
- **Modify** `internal/mcpsvc/service.go` (+ test) — `UpstreamInfo` gains `Profile`/`Presets`; `ListUpstreams` fills them; `RequestPreset` + `RequestPresetInput`.
- **Modify** `internal/mcp/server.go` — `request_preset` tool (struct + handler + registration).
- **Modify** `internal/daemon/admin.go` (+ test) — `approvePreset` fan-out; resolve accepts `bindings`; `hApprovalList` emits preset fields; `POST /presets/preview`.
- **Modify** `web/src/lib/types.ts`, `web/src/lib/api.ts`, `web/src/pages/Approvals.tsx` (+ `Approvals.test.tsx`) — preset types, `previewPreset`, `PresetCard`.
- **Create** `docs/architecture/decisions/0037-presets-first-class.md`; **modify** `docs/INDEX.md`.

---

### Task 1: serverprofile — preset types, validation, core catalog, interface method

**Files:**
- Modify: `internal/serverprofile/serverprofile.go`
- Modify (test fakes): `internal/serverprofile/serverprofile_test.go`, `internal/policy/decide_profile_test.go`
- Test: `internal/serverprofile/preset_test.go` (new, `package serverprofile`)

**Interfaces:**
- Produces: `Preset{ID,Label,Slots,Build}`, `PresetSlot{Key,Label,Type,Options,AllowAny,Required}`, `Bindings map[string]string`, `RuleTemplate{Outcome,BrowseMethods,BrowsePath,Profile,ProfileParams}`; `Profile.Presets() []Preset`; `ValidateBindings(slots,b) error`; `CoreHTTPPresets() []Preset` (one preset `browse-get`); `AvailablePresets(includeCoreHTTP bool, profile string) []Preset`; `FindPreset(includeCoreHTTP bool, profile, id string) (Preset, bool)`.

- [ ] **Step 1: Write the failing test** — create `internal/serverprofile/preset_test.go`:

```go
package serverprofile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateBindings(t *testing.T) {
	slots := []PresetSlot{
		{Key: "sourceId", Type: "text", AllowAny: true, Required: true},
		{Key: "workspace", Type: "text", AllowAny: false, Required: true},
		{Key: "mode", Type: "enum", Options: []string{"a", "b"}, Required: false},
	}
	require.NoError(t, ValidateBindings(slots, Bindings{"sourceId": "*", "workspace": "proj-x"}))
	require.Error(t, ValidateBindings(slots, Bindings{"workspace": "proj-x"}))            // sourceId required
	require.Error(t, ValidateBindings(slots, Bindings{"sourceId": "*", "workspace": "*"})) // "*" not allowed
	require.Error(t, ValidateBindings(slots, Bindings{"sourceId": "x", "workspace": "p", "mode": "z"})) // enum
	require.Error(t, ValidateBindings(slots, Bindings{"sourceId": "*", "workspace": "p", "bogus": "1"})) // unknown slot
}

func TestCoreHTTPBrowsePreset(t *testing.T) {
	ps := CoreHTTPPresets()
	require.Len(t, ps, 1)
	require.Equal(t, "browse-get", ps[0].ID)
	tmpls, err := ps[0].Build(Bindings{})
	require.NoError(t, err)
	require.Len(t, tmpls, 1)
	require.Equal(t, "GET,HEAD", tmpls[0].BrowseMethods)
	require.Equal(t, "/**", tmpls[0].BrowsePath)
	require.Equal(t, Allow, tmpls[0].Outcome)
}

func TestAvailablePresetsComposition(t *testing.T) {
	require.Len(t, AvailablePresets(true, "raw-http"), 1)  // core only
	require.Empty(t, AvailablePresets(false, "raw-http"))  // k8s-like: no core, no profile
	_, ok := FindPreset(true, "raw-http", "browse-get")
	require.True(t, ok)
	_, ok = FindPreset(true, "raw-http", "nope")
	require.False(t, ok)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serverprofile/ -run 'TestValidateBindings|TestCoreHTTP|TestAvailable' -v`
Expected: FAIL — undefined `PresetSlot`/`ValidateBindings`/`CoreHTTPPresets`/`AvailablePresets`/`FindPreset`.

- [ ] **Step 3: Write minimal implementation**

In `serverprofile.go`, add `"fmt"` to the import block. Add the types + funcs (place after the `RuleSchema` block, before `Profile`):

```go
// Bindings maps a preset slot key to its chosen value ("*" or a concrete value).
type Bindings map[string]string

// PresetSlot is one typed variable a preset exposes for the agent/operator to fill.
type PresetSlot struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // "text" | "enum"
	Options  []string `json:"options,omitempty"`
	AllowAny bool     `json:"allow_any"` // is "*" a permitted value for this slot?
	Required bool     `json:"required"`
}

// RuleTemplate is a profile-neutral rule a preset expands to. The daemon maps it to a policy.Rule
// (serverprofile must not import policy — policy imports serverprofile). Subject/upstream are set by
// the daemon at fan-out; this carries only the rule's shape.
type RuleTemplate struct {
	Outcome       string          // Allow | Deny | RequireApproval
	BrowseMethods string          // browse rule (coarse method-set), e.g. "GET,HEAD"
	BrowsePath    string          // browse rule path glob, e.g. "/**"
	Profile       string          // profile rule: the profile name (e.g. "citeck")
	ProfileParams json.RawMessage // profile rule params blob
}

// Preset is a named bundle of related rights with typed slots. Build expands it (with bound slot
// values) into rule templates; Build is not serialized (json:"-") — only ID/Label/Slots reach the UI.
type Preset struct {
	ID    string                                 `json:"id"`
	Label string                                 `json:"label"`
	Slots []PresetSlot                           `json:"slots"`
	Build func(Bindings) ([]RuleTemplate, error) `json:"-"`
}

// ValidateBindings checks b against slots: every Required slot present and non-empty; "*" allowed
// only when the slot's AllowAny is set; an enum value must be one of Options; unknown keys are an
// error. An empty value for a non-required slot is allowed (skipped).
func ValidateBindings(slots []PresetSlot, b Bindings) error {
	allowed := make(map[string]PresetSlot, len(slots))
	for _, s := range slots {
		allowed[s.Key] = s
	}
	for k := range b {
		if _, ok := allowed[k]; !ok {
			return fmt.Errorf("unknown slot %q", k)
		}
	}
	for _, s := range slots {
		v := b[s.Key]
		if v == "" {
			if s.Required {
				return fmt.Errorf("slot %q is required", s.Key)
			}
			continue
		}
		if v == "*" {
			if !s.AllowAny {
				return fmt.Errorf("slot %q does not allow %q", s.Key, "*")
			}
			continue
		}
		if s.Type == "enum" {
			ok := false
			for _, o := range s.Options {
				if o == v {
					ok = true
					break
				}
			}
			if !ok {
				return fmt.Errorf("slot %q value %q is not an allowed option", s.Key, v)
			}
		}
	}
	return nil
}

// CoreHTTPPresets are the generic presets available on any http upstream regardless of profile.
func CoreHTTPPresets() []Preset {
	return []Preset{{
		ID:    "browse-get",
		Label: "Browse (GET)",
		Build: func(Bindings) ([]RuleTemplate, error) {
			return []RuleTemplate{{Outcome: Allow, BrowseMethods: "GET,HEAD", BrowsePath: "/**"}}, nil
		},
	}}
}

// AvailablePresets is the preset catalog for an upstream: the core http presets (when includeCoreHTTP)
// plus the named profile's presets (if that profile is registered).
func AvailablePresets(includeCoreHTTP bool, profile string) []Preset {
	var out []Preset
	if includeCoreHTTP {
		out = append(out, CoreHTTPPresets()...)
	}
	if p, ok := Get(profile); ok {
		out = append(out, p.Presets()...)
	}
	return out
}

// FindPreset returns the preset with id from the upstream's available catalog.
func FindPreset(includeCoreHTTP bool, profile, id string) (Preset, bool) {
	for _, p := range AvailablePresets(includeCoreHTTP, profile) {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
```

Add `Presets() []Preset` to the `Profile` interface (after `RuleSchema() RuleSchema`):

```go
	RuleSchema() RuleSchema
	// Presets returns this profile's named rule bundles (may be empty).
	Presets() []Preset
```

Now the interface grew — add a `Presets()` to every test fake that implements `Profile`:
- In `internal/serverprofile/serverprofile_test.go`: add `func (<receiver>) Presets() []Preset { return nil }` to the fake profile type (match its existing receiver name).
- In `internal/policy/decide_profile_test.go`: add `func (<receiver>) Presets() []serverprofile.Preset { return nil }` to its fake profile type.

> Read each fake's existing method set (e.g. its `RuleSchema()`) to copy the exact receiver name.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/serverprofile/... ./internal/policy/... -race`
Expected: PASS (the two test fakes compile again; new preset tests green).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/serverprofile/ internal/policy/
git add internal/serverprofile/ internal/policy/decide_profile_test.go
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(serverprofile): preset types, validation, core catalog, Profile.Presets()"
```

---

### Task 2: citeck — ReadOnly + ReadWrite presets

**Files:**
- Modify: `internal/serverprofile/citeck/citeck.go`
- Test: `internal/serverprofile/citeck/preset_test.go` (new, `package citeck`)

**Interfaces:**
- Consumes: `serverprofile.Preset`/`PresetSlot`/`RuleTemplate`/`Bindings` (Task 1); the existing `ruleParams` struct.
- Produces: `profile.Presets()` returns `citeck-readonly` (browse + `op:read`) and `citeck-readwrite` (browse + `op:read` + `op:write`), both with slots `{sourceId (allow_any), workspace (concrete-only)}`.

- [ ] **Step 1: Write the failing test** — create `internal/serverprofile/citeck/preset_test.go`:

```go
package citeck

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/Sipaha/outwall/internal/serverprofile"
)

func TestCiteckPresetsBuild(t *testing.T) {
	ps := New().Presets()
	byID := map[string]serverprofile.Preset{}
	for _, p := range ps {
		byID[p.ID] = p
	}
	ro, ok := byID["citeck-readonly"]
	require.True(t, ok)
	rw, ok := byID["citeck-readwrite"]
	require.True(t, ok)

	b := serverprofile.Bindings{"sourceId": "*", "workspace": "proj-x"}

	roTmpls, err := ro.Build(b)
	require.NoError(t, err)
	require.Len(t, roTmpls, 2) // browse + read
	require.Equal(t, "GET,HEAD", roTmpls[0].BrowseMethods)
	require.Equal(t, "citeck", roTmpls[1].Profile)
	var rp ruleParams
	require.NoError(t, json.Unmarshal(roTmpls[1].ProfileParams, &rp))
	require.Equal(t, "read", rp.Op)
	require.Equal(t, "*", rp.SourceID)
	require.Equal(t, "proj-x", rp.Workspace)

	rwTmpls, err := rw.Build(b)
	require.NoError(t, err)
	require.Len(t, rwTmpls, 3) // browse + read + write
	var wp ruleParams
	require.NoError(t, json.Unmarshal(rwTmpls[2].ProfileParams, &wp))
	require.Equal(t, "write", wp.Op)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serverprofile/citeck/ -run TestCiteckPresetsBuild -v`
Expected: FAIL — `New().Presets()` has no presets (returns nil from the Task 1 stub).

- [ ] **Step 3: Write minimal implementation**

In `citeck.go`, replace the Task-1 stub `Presets()` with:

```go
func (profile) Presets() []serverprofile.Preset {
	slots := []serverprofile.PresetSlot{
		{Key: "sourceId", Label: "Source ID (glob)", Type: "text", AllowAny: true, Required: true},
		{Key: "workspace", Label: "Workspace", Type: "text", AllowAny: false, Required: true},
	}
	browse := serverprofile.RuleTemplate{Outcome: serverprofile.Allow, BrowseMethods: "GET,HEAD", BrowsePath: "/**"}
	opTmpl := func(op string, b serverprofile.Bindings) (serverprofile.RuleTemplate, error) {
		params, err := json.Marshal(ruleParams{Op: op, SourceID: b["sourceId"], Workspace: b["workspace"]})
		if err != nil {
			return serverprofile.RuleTemplate{}, err
		}
		return serverprofile.RuleTemplate{Outcome: serverprofile.Allow, Profile: "citeck", ProfileParams: params}, nil
	}
	return []serverprofile.Preset{
		{
			ID: "citeck-readonly", Label: "ReadOnly", Slots: slots,
			Build: func(b serverprofile.Bindings) ([]serverprofile.RuleTemplate, error) {
				read, err := opTmpl("read", b)
				if err != nil {
					return nil, err
				}
				return []serverprofile.RuleTemplate{browse, read}, nil
			},
		},
		{
			ID: "citeck-readwrite", Label: "ReadWrite", Slots: slots,
			Build: func(b serverprofile.Bindings) ([]serverprofile.RuleTemplate, error) {
				read, err := opTmpl("read", b)
				if err != nil {
					return nil, err
				}
				write, err := opTmpl("write", b)
				if err != nil {
					return nil, err
				}
				return []serverprofile.RuleTemplate{browse, read, write}, nil
			},
		},
	}
}
```

(`encoding/json` is already imported in `citeck.go`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/serverprofile/citeck/ -race`
Expected: PASS (existing citeck tests unaffected).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/serverprofile/citeck/
git add internal/serverprofile/citeck/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(citeck): ReadOnly + ReadWrite presets"
```

---

### Task 3: mcpsvc + approval — discovery enrichment + RequestPreset

**Files:**
- Modify: `internal/approval/queue.go` (`KindPreset` const ~47-58; `Pending` fields ~61-113)
- Modify: `internal/mcpsvc/service.go` (`UpstreamInfo` ~85-90; `ListUpstreams` ~215-233; add `RequestPreset`)
- Test: `internal/mcpsvc/service_test.go` (or the existing mcpsvc test file)

**Interfaces:**
- Consumes: `serverprofile.AvailablePresets`/`FindPreset`/`ValidateBindings` (Task 1); `upstream.Upstream{Kind,Profile,Name,ID}`; `upstream.KindK8s`.
- Produces: `approval.KindPreset = "preset"`; `approval.Pending.PresetID string`, `approval.Pending.Bindings map[string]string`; `mcpsvc.UpstreamInfo.Profile string` + `.Presets []serverprofile.Preset`; `Service.RequestPreset(agentID string, in RequestPresetInput) (AccessResult, error)` with `RequestPresetInput{Host, PresetID string; Bindings map[string]string; Purpose string}`.

- [ ] **Step 1: Write the failing test** — add to the mcpsvc test file (`package mcpsvc`; mirror the existing test setup helpers for building a Service with registries — read the file first to reuse them):

```go
func TestRequestPresetEnqueuesAndValidates(t *testing.T) {
	svc, deps := newTestService(t) // existing helper: returns Service + its registries/queue
	up := deps.mustUpstream(t, "cite.test", "https://cite.test", "http", "citeck")
	agentID := deps.mustAgent(t, "a1")

	// Unknown preset → error, no card.
	_, err := svc.RequestPreset(agentID, RequestPresetInput{Host: "cite.test", PresetID: "nope", Purpose: "x"})
	require.Error(t, err)

	// "*" workspace is not allowed for the citeck presets → error.
	_, err = svc.RequestPreset(agentID, RequestPresetInput{
		Host: "cite.test", PresetID: "citeck-readonly",
		Bindings: map[string]string{"sourceId": "*", "workspace": "*"}, Purpose: "x"})
	require.Error(t, err)

	// Valid → pending + one KindPreset card carrying the preset id + bindings.
	res, err := svc.RequestPreset(agentID, RequestPresetInput{
		Host: "cite.test", PresetID: "citeck-readonly",
		Bindings: map[string]string{"sourceId": "*", "workspace": "proj-x"}, Purpose: "read prod"})
	require.NoError(t, err)
	require.Equal(t, "pending", res.Status)
	var card approval.Pending
	require.Eventually(t, func() bool {
		for _, p := range deps.queue.List() {
			if p.Kind == approval.KindPreset {
				card = p
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, "citeck-readonly", card.PresetID)
	require.Equal(t, "proj-x", card.Bindings["workspace"])
	require.Equal(t, agentID, card.AgentID)
	require.Equal(t, up.ID, card.UpstreamID)
}

func TestListUpstreamsCarriesPresets(t *testing.T) {
	svc, deps := newTestService(t)
	deps.mustUpstream(t, "cite.test", "https://cite.test", "http", "citeck")
	agentID := deps.mustAgent(t, "a1")
	infos, err := svc.ListUpstreams(agentID)
	require.NoError(t, err)
	require.Len(t, infos, 1)
	require.Equal(t, "citeck", infos[0].Profile)
	ids := map[string]bool{}
	for _, p := range infos[0].Presets {
		ids[p.ID] = true
	}
	require.True(t, ids["browse-get"])
	require.True(t, ids["citeck-readonly"])
	require.True(t, ids["citeck-readwrite"])
}
```

> If the mcpsvc test file lacks `newTestService`/`mustUpstream`/`mustAgent`/`queue` helpers, write the minimal equivalents inline using the same registry constructors the other tests in the file use (read it first). The queue must be wired via `svc.SetApprovals(q)` so `enqueue` parks a real card; `require.Eventually` covers the background goroutine in `enqueue`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcpsvc/ -run 'TestRequestPreset|TestListUpstreamsCarriesPresets' -v`
Expected: FAIL — `RequestPreset`/`RequestPresetInput` undefined; `UpstreamInfo.Profile`/`.Presets` undefined; `approval.KindPreset` undefined.

- [ ] **Step 3: Write minimal implementation**

In `internal/approval/queue.go`, add the kind constant (in the `const (...)` block after `KindK8sAccess`):

```go
	// KindPreset is an MCP preset request carrying a preset id + slot bindings: on approve the
	// resolve path expands the preset into agent-scoped rules (see ADR-0037).
	KindPreset = "preset"
```

Add to `Pending` (after the `K8sGrants` field):

```go
	// Preset fields (set for KindPreset): the requested preset and its slot bindings. On approve the
	// resolve path re-validates the (possibly operator-edited) bindings and fans them out into rules.
	PresetID string
	Bindings map[string]string
```

In `internal/mcpsvc/service.go`, add the import `"github.com/Sipaha/outwall/internal/serverprofile"`. Extend `UpstreamInfo`:

```go
type UpstreamInfo struct {
	Name    string                `json:"name"`
	BaseURL string                `json:"base_url"`
	Kind    string                `json:"kind"`
	Profile string                `json:"profile"`
	Status  string                `json:"status"`
	Presets []serverprofile.Preset `json:"presets"`
}
```

In `ListUpstreams`, fill the new fields (inside the loop, replacing the `out = append(...)` line):

```go
		profileName := u.Profile
		presets := serverprofile.AvailablePresets(kind != upstream.KindK8s, profileName)
		out = append(out, UpstreamInfo{
			Name: u.Name, BaseURL: u.BaseURL, Kind: kind, Profile: profileName,
			Status: st, Presets: presets,
		})
```

Add the input type + method (place near `RequestK8sAccess`):

```go
// RequestPresetInput is the request_preset payload: the upstream, the preset id, the slot bindings
// ("*" or concrete per slot), and the purpose.
type RequestPresetInput struct {
	Host     string
	PresetID string
	Bindings map[string]string
	Purpose  string
}

// RequestPreset validates a preset request against the upstream's available catalog and the preset's
// slot schema (a bad preset id or invalid bindings is a tool error, NOT a pending), then enqueues a
// KindPreset approval carrying the preset id + bindings. The MCP call does not block — the agent
// polls get_access. On approve the daemon expands the preset into agent-scoped rules (ADR-0037).
func (s *Service) RequestPreset(agentID string, in RequestPresetInput) (AccessResult, error) {
	up, err := s.resolveUpstream(in.Host)
	if err == upstream.ErrNotFound {
		return AccessResult{Status: "denied", Memo: "no such host — request_host_access first"}, nil
	}
	if err != nil {
		return AccessResult{}, err
	}
	includeCore := up.Kind != upstream.KindK8s
	preset, ok := serverprofile.FindPreset(includeCore, up.Profile, in.PresetID)
	if !ok {
		return AccessResult{}, fmt.Errorf("unknown preset %q for upstream %q", in.PresetID, up.Name)
	}
	if err := serverprofile.ValidateBindings(preset.Slots, serverprofile.Bindings(in.Bindings)); err != nil {
		return AccessResult{}, fmt.Errorf("invalid preset bindings: %w", err)
	}

	// Dedupe: the same preset already awaiting a decision for this agent+upstream → don't raise a second.
	if s.pendingExists(func(p approval.Pending) bool {
		return p.Kind == approval.KindPreset && p.AgentID == agentID && p.UpstreamID == up.ID &&
			p.PresetID == in.PresetID
	}) {
		res := AccessResult{Status: "pending", BasePath: "/" + up.Name,
			Memo: "preset already submitted — call get_access (it waits for the decision)"}
		res.BrowseURL = s.browseURL(up)
		return res, nil
	}

	if _, err := s.access.Create(agentID, up.ID, in.Purpose); err != nil {
		return AccessResult{}, fmt.Errorf("log access request: %w", err)
	}
	if err := s.enqueue(agentID, approval.Pending{
		Kind: approval.KindPreset, AgentID: agentID, UpstreamID: up.ID, Host: up.Name,
		Purpose: in.Purpose, PresetID: in.PresetID, Bindings: in.Bindings,
	}); err != nil {
		return AccessResult{}, err
	}
	res := AccessResult{Status: "pending", BasePath: "/" + up.Name,
		Memo: "preset submitted for approval — call get_access (it waits for the decision)"}
	res.BrowseURL = s.browseURL(up)
	return res, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcpsvc/ ./internal/approval/ -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/mcpsvc/ internal/approval/
git add internal/mcpsvc/ internal/approval/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(mcpsvc): request_preset domain service + list_upstreams presets; approval KindPreset"
```

---

### Task 4: mcp adapter — `request_preset` tool

**Files:**
- Modify: `internal/mcp/server.go` (tool I/O structs ~50-105; registration ~122-142; handlers ~210-340)
- Test: `internal/mcp/server_test.go` (only if the package has tests that assert the tool set — otherwise the daemon/integration covers it; read the file first and mirror its style)

**Interfaces:**
- Consumes: `mcpsvc.RequestPreset`/`RequestPresetInput` (Task 3); `mcpsvc.UpstreamInfo` (already carries `Presets` — `list_upstreams` output is automatically enriched, no adapter change for it).
- Produces: a `request_preset` MCP tool.

- [ ] **Step 1: Write the failing test**

If `internal/mcp/server_test.go` exists and asserts the registered tool names, add `"request_preset"` to that expectation (read the file; match its assertion shape). If there is no such test, SKIP the test step for this task and note in the report that the tool is covered by the manual e2e (Task 8 step) and the daemon tests — do not invent a brittle SDK-level test.

- [ ] **Step 2: Run test to verify it fails** (only if a test was added)

Run: `go test ./internal/mcp/ -v`
Expected: FAIL — `request_preset` not registered.

- [ ] **Step 3: Write minimal implementation**

Add the input struct (near `requestK8sAccessIn`):

```go
type requestPresetIn struct {
	Upstream string            `json:"upstream" jsonschema:"the upstream name the preset applies to"`
	Preset   string            `json:"preset" jsonschema:"the preset id from list_upstreams (e.g. citeck-readonly)"`
	Vars     map[string]string `json:"vars" jsonschema:"slot values for the preset; each is a concrete value or \"*\" where allowed (e.g. {sourceId: \"*\", workspace: \"proj-x\"})"`
	Purpose  string            `json:"purpose" jsonschema:"why the agent needs this preset (required)"`
}
```

Register the tool (after the `request_k8s_access` registration):

```go
	sdkmcp.AddTool(sdkServer,
		&sdkmcp.Tool{Name: "request_preset", Description: "Request a named PRESET (a bundle of related rights) on an upstream. Call list_upstreams first to see each upstream's available presets and their slots; pass `vars` with a value per slot (\"*\" only where allowed). The operator approves (and may narrow the values). Returns granted|pending|denied; on pending call get_access (it waits)."},
		srv.handleRequestPreset)
```

Add the handler (near `handleRequestK8sAccess`):

```go
func (s *server) handleRequestPreset(ctx context.Context, req *sdkmcp.CallToolRequest, in requestPresetIn) (*sdkmcp.CallToolResult, mcpsvc.AccessResult, error) {
	id, err := s.agentFor(req)
	if err != nil {
		return nil, mcpsvc.AccessResult{}, err
	}
	if strings.TrimSpace(in.Purpose) == "" {
		return toolError("purpose is required"), mcpsvc.AccessResult{}, nil
	}
	if strings.TrimSpace(in.Preset) == "" {
		return toolError("preset is required"), mcpsvc.AccessResult{}, nil
	}
	if s.locked() {
		return toolError(lockedMsg), mcpsvc.AccessResult{}, nil
	}
	res, err := s.deps.Svc.RequestPreset(id.agentID, mcpsvc.RequestPresetInput{
		Host: in.Upstream, PresetID: in.Preset, Bindings: in.Vars, Purpose: in.Purpose,
	})
	if err != nil {
		return toolError(err.Error()), mcpsvc.AccessResult{}, nil
	}
	return nil, res, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/ -race` and `CGO_ENABLED=0 go build ./...`
Expected: PASS / clean build.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/mcp/
git add internal/mcp/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(mcp): request_preset control-plane tool"
```

---

### Task 5: daemon — approvePreset fan-out + resolve bindings + approval-list preset fields

**Files:**
- Modify: `internal/daemon/admin.go` (`hApprovalList` ~513-553; `hApprovalResolve` ~556-609; `applyApprovalSideEffects` ~615-643; add `approvePreset` + `presetForUpstream` helper)
- Test: `internal/daemon/admin_test.go`

**Interfaces:**
- Consumes: `approval.KindPreset`/`Pending.PresetID`/`Pending.Bindings` (Task 3); `serverprofile.FindPreset`/`ValidateBindings`/`RuleTemplate` (Task 1); `policy.Rule` browse/profile fields.
- Produces: on approving a `KindPreset` card the daemon creates one agent-scoped `policy.Rule` per `RuleTemplate`; `POST /approvals/{id}/resolve` accepts `bindings`; `GET /approvals` emits `kind:"preset"`, `preset_id`, `bindings`, and a `preset` object `{id,label,slots}`.

- [ ] **Step 1: Write the failing test** — add to `internal/daemon/admin_test.go`:

```go
func TestApprovePresetFansOutAgentScopedRules(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, 200, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	// A citeck upstream + an agent.
	up := d.mustUpstreamProfiled(t, "cite.test", "https://cite.test", "http", "citeck") // helper; see note
	agentID := d.mustAgent(t, "a1")

	// Park a KindPreset pending directly on the queue (the daemon owns the resolve side effects).
	go func() {
		_, _ = d.approvals.Submit(context.Background(), approval.Pending{
			Kind: approval.KindPreset, AgentID: agentID, UpstreamID: up.ID, Host: up.Name,
			PresetID: "citeck-readonly", Bindings: map[string]string{"sourceId": "*", "workspace": "proj-x"},
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

	// Operator narrows nothing; approve with the requested bindings echoed back.
	body := `{"approve":true,"bindings":{"sourceId":"*","workspace":"proj-x"}}`
	require.Equal(t, 200, req(t, h, "POST", "/approvals/"+id+"/resolve", body).Code)

	rules, err := d.policy.ForUpstream(up.ID)
	require.NoError(t, err)
	var browse, citeckRead int
	for _, r := range rules {
		require.Equal(t, agentID, r.SubjectAgentID) // every fanned-out rule is agent-scoped
		if r.BrowsePath == "/**" {
			browse++
		}
		if r.Profile == "citeck" {
			citeckRead++
		}
	}
	require.Equal(t, 1, browse)
	require.Equal(t, 1, citeckRead)
}
```

> Reuse the daemon test's existing upstream/agent helpers if present; otherwise add `mustUpstreamProfiled` (calls `d.upstreams.CreateProfiled(name, baseURL, "http", "citeck", upstream.AuthConfig{Type:"none"})`) and `mustAgent` (calls `d.agents.Register`). Import `context`, `time`, and `internal/approval` in the test.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run TestApprovePresetFansOut -v`
Expected: FAIL — `KindPreset` not handled in `applyApprovalSideEffects` (no rules created).

- [ ] **Step 3: Write minimal implementation**

In `hApprovalResolve`, add a `Bindings` field to the body struct (after `TrustAny`):

```go
		// Bindings are the operator's final preset slot values for a KindPreset approval (may narrow
		// the agent's requested values). Ignored for other kinds.
		Bindings map[string]string `json:"bindings"`
```

Change the `applyApprovalSideEffects` call to pass them:

```go
			if err := d.applyApprovalSideEffects(p, body.Auth, body.TrustAny, body.Bindings); err != nil {
```

Update `applyApprovalSideEffects` signature + add the case:

```go
func (d *Daemon) applyApprovalSideEffects(p approval.Pending, auth *upstream.AuthConfig, trustAny []string, bindings map[string]string) error {
	switch p.Kind {
	// … existing KindHostAccess / KindOperation / KindK8sAccess cases unchanged …
	case approval.KindPreset:
		return d.approvePreset(p, bindings)
	default:
		return nil
	}
}
```

Add `approvePreset` and the helper (near `approveK8sAccess`):

```go
// approvePreset expands a KindPreset approval into agent-scoped allow rules. The operator may have
// narrowed the slot values (bindings); when nil the agent's requested bindings are used. The final
// bindings are re-validated against the preset's slot schema before any rule is created (ADR-0037).
func (d *Daemon) approvePreset(p approval.Pending, bindings map[string]string) error {
	final := bindings
	if final == nil {
		final = p.Bindings
	}
	preset, ok, err := d.presetForUpstream(p.UpstreamID, p.PresetID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("unknown preset %q for upstream", p.PresetID)
	}
	if err := serverprofile.ValidateBindings(preset.Slots, serverprofile.Bindings(final)); err != nil {
		return fmt.Errorf("invalid preset bindings: %w", err)
	}
	tmpls, err := preset.Build(serverprofile.Bindings(final))
	if err != nil {
		return fmt.Errorf("expand preset: %w", err)
	}
	for _, t := range tmpls {
		if _, err := d.policy.Create(policy.Rule{
			SubjectAgentID: p.AgentID, UpstreamID: p.UpstreamID, Outcome: t.Outcome,
			BrowseMethods: t.BrowseMethods, BrowsePath: t.BrowsePath,
			Profile: t.Profile, ProfileParams: t.ProfileParams,
		}); err != nil {
			return fmt.Errorf("create preset rule: %w", err)
		}
	}
	return nil
}

// presetForUpstream resolves a preset id against an upstream's available catalog (core http presets
// for non-k8s + the upstream's profile presets).
func (d *Daemon) presetForUpstream(upstreamID, presetID string) (serverprofile.Preset, bool, error) {
	up, err := d.upstreams.GetByID(upstreamID)
	if err != nil {
		return serverprofile.Preset{}, false, fmt.Errorf("load upstream: %w", err)
	}
	preset, ok := serverprofile.FindPreset(up.Kind != upstream.KindK8s, up.Profile, presetID)
	return preset, ok, nil
}
```

In `hApprovalList`, add the preset context to the per-pending map (after the `KindOperation` block):

```go
		if p.Kind == approval.KindPreset {
			m["preset_id"] = p.PresetID
			m["bindings"] = p.Bindings
			if preset, ok, perr := d.presetForUpstream(p.UpstreamID, p.PresetID); perr == nil && ok {
				m["preset"] = preset // serializes {id,label,slots} (Build is json:"-")
			}
		}
```

(`serverprofile` is already imported in `admin.go` — it is used by `hProfileList`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/daemon/ -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/daemon/
git add internal/daemon/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(daemon): approvePreset fan-out + operator binding edits + approval-list preset fields"
```

---

### Task 6: daemon — `POST /presets/preview` (dry-run)

**Files:**
- Modify: `internal/daemon/admin.go` (route registration ~52; add `hPresetPreview` + `summarizeTemplate`)
- Test: `internal/daemon/admin_test.go`

**Interfaces:**
- Consumes: `presetForUpstream` (Task 5); `serverprofile.ValidateBindings`/`Build`/`RuleTemplate`.
- Produces: `POST /presets/preview {upstream_id, preset_id, bindings}` → `{"rules": ["…","…"]}` — a human-readable summary per rule the preset would create with the given bindings (reflects operator edits).

- [ ] **Step 1: Write the failing test** — add to `internal/daemon/admin_test.go`:

```go
func TestPresetPreview(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, 200, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	up := d.mustUpstreamProfiled(t, "cite.test", "https://cite.test", "http", "citeck")

	body := `{"upstream_id":"` + up.ID + `","preset_id":"citeck-readonly","bindings":{"sourceId":"*","workspace":"proj-x"}}`
	rec := req(t, h, "POST", "/presets/preview", body)
	require.Equal(t, 200, rec.Code)
	out := rec.Body.String()
	require.Contains(t, out, "GET,HEAD") // browse rule summarized
	require.Contains(t, out, "proj-x")   // citeck read rule with the bound workspace

	// Invalid bindings → 400.
	bad := `{"upstream_id":"` + up.ID + `","preset_id":"citeck-readonly","bindings":{"sourceId":"*","workspace":"*"}}`
	require.Equal(t, 400, req(t, h, "POST", "/presets/preview", bad).Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run TestPresetPreview -v`
Expected: FAIL — 404 (route not registered).

- [ ] **Step 3: Write minimal implementation**

Register the route (in the mux setup, near `GET /profiles`):

```go
	mux.HandleFunc("POST /presets/preview", d.hPresetPreview)
```

Add the handler + summarizer:

```go
// hPresetPreview dry-runs a preset's Build with the given bindings and returns a human-readable
// summary per rule it would create — so the approval card shows the concrete grant (reflecting any
// operator edits) before approving. It creates nothing.
func (d *Daemon) hPresetPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UpstreamID string            `json:"upstream_id"`
		PresetID   string            `json:"preset_id"`
		Bindings   map[string]string `json:"bindings"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	preset, ok, err := d.presetForUpstream(body.UpstreamID, body.PresetID)
	if err != nil {
		adminErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ok {
		adminErr(w, http.StatusBadRequest, "unknown preset")
		return
	}
	if err := serverprofile.ValidateBindings(preset.Slots, serverprofile.Bindings(body.Bindings)); err != nil {
		adminErr(w, http.StatusBadRequest, err.Error())
		return
	}
	tmpls, err := preset.Build(serverprofile.Bindings(body.Bindings))
	if err != nil {
		adminErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rules := make([]string, 0, len(tmpls))
	for _, t := range tmpls {
		rules = append(rules, summarizeTemplate(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

// summarizeTemplate renders a one-line human summary of a preset rule template for the preview.
func summarizeTemplate(t serverprofile.RuleTemplate) string {
	switch {
	case t.BrowsePath != "":
		methods := t.BrowseMethods
		if methods == "" {
			methods = "*"
		}
		return fmt.Sprintf("%s browse %s %s", t.Outcome, methods, t.BrowsePath)
	case t.Profile != "":
		return fmt.Sprintf("%s %s %s", t.Outcome, t.Profile, string(t.ProfileParams))
	default:
		return t.Outcome
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/daemon/ -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/daemon/
git add internal/daemon/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(daemon): POST /presets/preview dry-run for the approval card"
```

---

### Task 7: web — preset types, preview API, and the preset approval card

**Files:**
- Modify: `web/src/lib/types.ts` (`Upstream`, `Approval`, `ResolveOptions`; new `Preset`/`PresetSlot`)
- Modify: `web/src/lib/api.ts` (add `previewPreset`)
- Modify: `web/src/pages/Approvals.tsx` (new `PresetCard` + `ApprovalCard` branch)
- Test: `web/src/pages/Approvals.test.tsx`

**Interfaces:**
- Consumes: `GET /approvals` `kind:"preset"`, `preset`, `bindings` (Task 5); `POST /presets/preview` (Task 6).
- Produces: a `PresetCard` rendering editable slots seeded from `bindings`, a live preview, and an Approve that posts `{bindings}`.

- [ ] **Step 1: Write the failing test** — add to `web/src/pages/Approvals.test.tsx` (mirror the existing card tests' imports/mocks; read the file first):

```ts
it('preset card edits a slot and approves with the final bindings', async () => {
  vi.spyOn(api, 'listAccessRequests').mockResolvedValue([])
  vi.spyOn(api, 'previewPreset').mockResolvedValue({ rules: ['allow browse GET,HEAD /**'] })
  vi.spyOn(api, 'listApprovals').mockResolvedValue([
    {
      id: 'p1', agent_id: 'a1', upstream_id: 'u1', kind: 'preset',
      host: 'cite.test', purpose: 'read prod', preset_id: 'citeck-readonly',
      bindings: { sourceId: '*', workspace: 'proj-x' },
      preset: {
        id: 'citeck-readonly', label: 'ReadOnly',
        slots: [
          { key: 'sourceId', label: 'Source ID', type: 'text', allow_any: true, required: true },
          { key: 'workspace', label: 'Workspace', type: 'text', allow_any: false, required: true },
        ],
      },
    },
  ] as unknown as Approval[])
  const resolveSpy = vi.spyOn(api, 'resolveApproval').mockResolvedValue({ ok: true })
  render(<Approvals />)
  const ws = await screen.findByLabelText('workspace')
  fireEvent.change(ws, { target: { value: 'proj-y' } })
  fireEvent.click(screen.getByRole('button', { name: /approve/i }))
  await waitFor(() =>
    expect(resolveSpy).toHaveBeenCalledWith('p1', true, {
      bindings: { sourceId: '*', workspace: 'proj-y' },
    }),
  )
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && pnpm test -- Approvals`
Expected: FAIL — no preset card / `previewPreset` undefined.

- [ ] **Step 3: Write minimal implementation**

In `types.ts`, add:

```ts
export interface PresetSlot {
  key: string
  label: string
  type: string // "text" | "enum"
  options?: string[]
  allow_any: boolean
  required: boolean
}
export interface Preset {
  id: string
  label: string
  slots: PresetSlot[]
}
```

Add `presets?: Preset[]` to `Upstream`. Add to `Approval`: `preset_id?: string`, `bindings?: Record<string, string>`, `preset?: Preset`. Add to `ResolveOptions`: `bindings?: Record<string, string>`.

In `api.ts`, add:

```ts
export function previewPreset(
  upstream_id: string,
  preset_id: string,
  bindings: Record<string, string>,
): Promise<{ rules: string[] }> {
  return request('POST', '/presets/preview', { upstream_id, preset_id, bindings })
}
```

In `Approvals.tsx`, import `previewPreset` and `type Preset` (extend the existing imports). Add the card component (near `K8sAccessCard`):

```tsx
/** MCP preset card: editable slots (seeded from the requested bindings) + a live rule preview. */
function PresetCard({ approval, onResolve }: CardProps) {
  const preset = approval.preset
  const [bindings, setBindings] = useState<Record<string, string>>(approval.bindings ?? {})
  const [preview, setPreview] = useState<string[]>([])

  useEffect(() => {
    let live = true
    previewPreset(approval.upstream_id, approval.preset_id ?? '', bindings)
      .then((r) => {
        if (live) setPreview(r.rules)
      })
      .catch(() => {
        if (live) setPreview([])
      })
    return () => {
      live = false
    }
  }, [approval.upstream_id, approval.preset_id, bindings])

  function setSlot(key: string, value: string) {
    setBindings((b) => ({ ...b, [key]: value }))
  }

  return (
    <div className={cardClass}>
      <div className="flex items-start justify-between gap-2">
        <div className="space-y-1">
          <div className="text-[11px] text-muted-foreground">
            Preset · agent <span className="font-mono">{shortId(approval.agent_id)}</span> ·{' '}
            <span className="font-mono">{approval.host}</span>
          </div>
          <div className="text-sm font-medium">{preset?.label ?? approval.preset_id}</div>
          {approval.purpose && <div className="text-xs text-muted-foreground">{approval.purpose}</div>}
        </div>
        <StatusBadge status="preset" />
      </div>

      {(preset?.slots ?? []).length > 0 && (
        <div className="space-y-2">
          {preset!.slots.map((s) => (
            <FormField key={s.key} label={s.label || s.key}>
              {s.type === 'enum' ? (
                <Select
                  value={bindings[s.key] ?? ''}
                  onChange={(v) => setSlot(s.key, v)}
                  options={[
                    ...(s.allow_any ? [{ value: '*', label: '* (all)' }] : []),
                    ...(s.options ?? []).map((o) => ({ value: o, label: o })),
                  ]}
                />
              ) : (
                <input
                  className={fieldControlClass}
                  value={bindings[s.key] ?? ''}
                  onChange={(e) => setSlot(s.key, e.target.value)}
                  aria-label={s.key}
                  placeholder={s.allow_any ? '* or a concrete value' : 'a concrete value'}
                />
              )}
            </FormField>
          ))}
        </div>
      )}

      <div className="rounded border border-border/60 bg-muted/30 p-2">
        <div className="mb-1 text-[11px] text-muted-foreground">Will create</div>
        {preview.length === 0 ? (
          <div className="text-[11px] text-muted-foreground italic">…</div>
        ) : (
          preview.map((r, i) => (
            <code key={i} className="block break-all text-xs">
              {r}
            </code>
          ))
        )}
      </div>

      <div className="flex justify-end gap-1.5">
        <button onClick={() => onResolve(approval.id, true, { bindings })} className={approveBtn}>
          Approve
        </button>
        <button onClick={() => onResolve(approval.id, false)} className={denyBtn}>
          Deny
        </button>
      </div>
    </div>
  )
}
```

Add `useEffect` to the React import at the top if not already imported (the file imports `useCallback, useEffect, useState` already — confirm). Add the branch in `ApprovalCard`:

```tsx
  if (approval.kind === 'preset') return <PresetCard {...props} />
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && pnpm test && pnpm lint && pnpm build && cd ..` then `git checkout -- internal/daemon/webdist/index.html`
Expected: PASS (existing approval-card tests unaffected).

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/types.ts web/src/lib/api.ts web/src/pages/Approvals.tsx web/src/pages/Approvals.test.tsx
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(web): preset approval card (editable slots + live preview)"
```

---

### Task 8: full gate + ADR-0037 + live verification

**Files:**
- Create: `docs/architecture/decisions/0037-presets-first-class.md`
- Modify: `docs/INDEX.md`

- [ ] **Step 1: Run the full gate**

```bash
gofmt -w . && go vet ./... && go test ./... -race && CGO_ENABLED=0 go build ./...
go build -tags desktop -o /tmp/owd ./cmd/outwall-desktop
cd web && pnpm test && pnpm lint && pnpm build && cd ..
git checkout -- internal/daemon/webdist/index.html
```
Expected: all green.

- [ ] **Step 2: Write ADR-0037**

Create `docs/architecture/decisions/0037-presets-first-class.md` (status accepted, date 2026-06-23). Cover: presets are plugin-defined (`Profile.Presets()` + a core http catalog), expand via `Build` to profile-neutral `RuleTemplate`s (why neutral — `serverprofile` must not import `policy`); agent discovery via enriched `list_upstreams`; the `request_preset` tool; `KindPreset` approval re-using `access_requests` + `approval.Queue`; operator can narrow slot bindings before approve (re-validated server-side); fan-out creates agent-scoped rules (subject = requesting agent — fixes the ADR-0036 host-wide preset issue); the `/presets/preview` dry-run; v1 catalog (`Browse (GET)`, citeck `ReadOnly`/`ReadWrite`); slot `allow_any` (citeck `workspace` forces a concrete value because empty/`*` workspace = ALL, per ADR-0034). Out of scope (v1): operator-initiated grants, operator-defined/custom presets, k8s presets, rule-origin tagging. Link ADR-0034 / ADR-0036 / ADR-0029.

- [ ] **Step 3: Link ADR in INDEX**

Add the ADR-0037 line to the `decisions/` list in `docs/INDEX.md` (match the existing entry format).

- [ ] **Step 4: Commit + push**

```bash
git add docs/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "docs(adr-0037): presets as a first-class, agent-requested concept"
git push origin main
```

- [ ] **Step 5: Live verification (operator-assisted; record the real result)**

With a rebuilt app (`make run`): from a fresh MCP session call `list_upstreams` and confirm `enterprise.ecos24.ru` reports `profile: "citeck"` and presets `browse-get` / `citeck-readonly` / `citeck-readwrite` with their slots. Call `request_preset(upstream:"enterprise.ecos24.ru", preset:"citeck-readonly", vars:{sourceId:"*", workspace:<a concrete workspace>}, purpose:"…")`. In the operator UI approve the preset card (optionally narrow a slot; confirm the preview updates). Then drive Playwright (a context with `ignoreHTTPSErrors: true` + the `outwall_token` cookie on `https://enterprise.ecos24.ru.outwall.localhost:<port>`) and confirm the app renders and a Records read-query returns 200 through outwall, while a mutate/delete stays denied. Record the outcome (or the exact remaining blocker). This step is operator-driven — the daemon cannot approve cards or drive the browser; surface the result to the user.

---

## Self-review notes

- **Spec coverage:** concept/data-model → Task 1 (+2 for citeck); control-plane discovery → Task 3 (`ListUpstreams`); `request_preset` → Tasks 3-4; `KindPreset` + fan-out + operator narrowing → Tasks 3, 5; preview → Task 6; operator UI card → Task 7; v1 catalog → Tasks 1-2; ADR/docs/gate/e2e → Task 8.
- **Import-cycle guard:** `serverprofile` stays policy-free — presets expand to `serverprofile.RuleTemplate`; only the daemon (Task 5) maps to `policy.Rule`. This is the one deliberate deviation from the spec's `Build → []policy.Rule` sketch (the spec is updated to `RuleTemplate`).
- **Interface growth:** adding `Profile.Presets()` breaks the two test fakes (`serverprofile_test.go`, `decide_profile_test.go`) — Task 1 updates both in the same commit so the tree never fails to build.
- **Naming consistency:** `Preset{ID,Label,Slots,Build}`, `PresetSlot{Key,Label,Type,Options,AllowAny,Required}`, `RuleTemplate`, `Bindings`, `KindPreset="preset"`, `RequestPresetInput{Host,PresetID,Bindings,Purpose}`, `AvailablePresets(includeCoreHTTP,profile)`, `FindPreset`, `previewPreset` — identical across Go, control-API, and web. Preset ids `browse-get` / `citeck-readonly` / `citeck-readwrite` and slot keys `sourceId` / `workspace` are used verbatim everywhere.
- **Non-breaking:** all changes are additive; existing operation/k8s/host approvals and rules are untouched (the new `applyApprovalSideEffects` param is threaded through the single call site; other kinds ignore it).
- **Placeholder scan:** none — every step carries concrete code/commands.
