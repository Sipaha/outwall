# Server profiles + Citeck plugin — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a per-upstream **server profile** plugin mechanism (core stays platform-agnostic) whose first plugin, **citeck**, gates Citeck Records API requests (read/write × sourceId × workspace), while non-Records paths on the same upstream still use the existing raw-http rule engine.

**Architecture:** A new core package `internal/serverprofile` defines a `Profile` interface + a global registry that plugins self-register into via `init()`. The policy engine, for an upstream whose `profile` is non-`raw-http`, asks the profile to `Classify` the request; if the profile claims it, the profile's own rules decide; otherwise the request falls through to the existing raw-http/k8s engine. The `citeck` plugin lives entirely in `internal/serverprofile/citeck` (the only place `citeck` strings appear) and is bundled by a blank import in the `cmd/` entrypoints.

**Tech Stack:** Go (CGO-free server), `modernc.org/sqlite`, `stretchr/testify`; React + Vite + Vitest + Testing Library for the UI.

**Companion ADRs to write during this plan:** ADR-0034 (server profiles + the citeck plugin + the contained relaxation of the "no citeck" rule). Spec: `docs/superpowers/specs/2026-06-22-server-profiles-and-per-upstream-origin-design.md`.

## Global Constraints

- Module path is exactly `github.com/Sipaha/outwall` in every import.
- **No `citeck` strings/imports anywhere EXCEPT inside `internal/serverprofile/citeck`** and the persisted profile-name value `"citeck"`. The core (`serverprofile`, `policy`, `proxy`, `daemon`, `store`, `upstream`) must not name citeck. (This plan deliberately relaxes the previous blanket "no citeck" rule — see Task 16, which records it in ADR-0034 and edits `AGENTS.md` + memory.)
- No CGO in the server binary (`CGO_ENABLED=0`). SQLite is pure-Go.
- No panics / `log.Fatal` in library code — return wrapped `error` (`%w`). Panics only in `main`/tests.
- Add new deps at `@latest`; do not bump existing deps.
- `log/slog` for logging. Small focused files.
- Alpha: no storage-format back-compat to preserve, but **migrations are forward-only** — edit `schema` AND append a migration step (never edit/reorder a released step). See `internal/store/migrate.go` doc comment.
- Commit author MUST be `Sipaha <sipahabk@gmail.com>`: every commit uses `git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit`. **No `Co-Authored-By`. No `git commit --amend`.** New commits only.
- Gate before each commit: `gofmt -w .`, `go vet ./...`, `go test ./... -race`, `CGO_ENABLED=0 go build ./...`. For web tasks also `cd web && pnpm test && pnpm lint && pnpm build`, then `git checkout -- internal/daemon/webdist/index.html` (the build rewrites it).

## File structure (created / modified)

- **Create** `internal/serverprofile/serverprofile.go` — `Profile` interface, `Operation`/`ResourceScope`/`Request`/`Rule`/`RuleSchema` types, outcome consts, global `Register`/`Get`.
- **Create** `internal/serverprofile/serverprofile_test.go`.
- **Create** `internal/serverprofile/citeck/citeck.go` — the plugin: `Profile` impl + `init()` registration.
- **Create** `internal/serverprofile/citeck/records.go` — Records request parsing (ref/sourceId, classify).
- **Create** `internal/serverprofile/citeck/*_test.go`.
- **Modify** `internal/store/migrate.go` — `schema` + one migration step (upstreams.profile; rules.profile, rules.profile_params).
- **Modify** `internal/upstream/registry.go` — `Upstream.Profile` field threaded through insert/scan/list/CreateKind.
- **Modify** `internal/policy/rule.go` — `Rule.Profile` + `Rule.ProfileParams`.
- **Modify** `internal/policy/registry.go` — persist/scan the two new columns.
- **Modify** `internal/policy/decide.go` — `Input.Profile`; profile classification + routing; raw-http path skips profile rules.
- **Modify** `internal/proxy/proxy.go` — set `Input.Profile` from the upstream.
- **Modify** `internal/daemon/admin.go` — accept/return `profile` on upstreams; accept `profile`/`profile_params` on rules; `GET /profiles`.
- **Modify** `cmd/outwall/main.go`, `cmd/outwall-desktop/main.go` — blank-import the citeck plugin.
- **Modify** `web/src/lib/types.ts`, `web/src/lib/api.ts` — `profile` fields + `getProfiles()`.
- **Modify** `web/src/pages/Upstreams.tsx` (+ test) — Server-type selector.
- **Modify** `web/src/pages/Rules.tsx` (+ test) — Citeck Records-rule editor.

---

### Task 1: `serverprofile` core package — interface + registry

**Files:**
- Create: `internal/serverprofile/serverprofile.go`
- Test: `internal/serverprofile/serverprofile_test.go`

**Interfaces:**
- Produces: `Profile` interface; `Register(name string, p Profile)`; `Get(name string) (Profile, bool)`; types `Request`, `Operation`, `ResourceScope`, `Rule`, `RuleSchema`, `RuleField`; outcome consts `Allow`/`Deny`/`RequireApproval`.

- [ ] **Step 1: Write the failing test**

```go
package serverprofile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeProfile struct{ name string }

func (f fakeProfile) Name() string                                      { return f.name }
func (f fakeProfile) Classify(Request) (Operation, bool, error)         { return Operation{}, false, nil }
func (f fakeProfile) Match(Rule, Operation) (string, bool, error)       { return Allow, true, nil }
func (f fakeProfile) RuleSchema() RuleSchema                            { return RuleSchema{Profile: f.name} }

func TestRegisterAndGet(t *testing.T) {
	Register("fake-x", fakeProfile{name: "fake-x"})
	p, ok := Get("fake-x")
	require.True(t, ok)
	require.Equal(t, "fake-x", p.Name())

	_, ok = Get("nope")
	require.False(t, ok)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serverprofile/ -run TestRegisterAndGet -v`
Expected: FAIL — package/types do not exist (compile error).

- [ ] **Step 3: Write minimal implementation**

```go
// Package serverprofile is the platform-agnostic plugin point for classifying and authorizing
// requests to a specific kind of upstream server. A plugin (e.g. internal/serverprofile/citeck)
// registers itself via Register in its init(); the core never imports a plugin.
package serverprofile

import (
	"encoding/json"
	"net/url"
	"sync"
)

// Outcomes mirror the policy engine's outcome strings (kept here to avoid importing policy).
const (
	Allow           = "allow"
	Deny            = "deny"
	RequireApproval = "require-approval"
)

// Request is the part of an HTTP request a profile inspects to classify an operation.
type Request struct {
	Method string
	Path   string
	Query  url.Values
	Body   []byte // raw request body (may be nil)
}

// ResourceScope is one (resource, scope) pair a request touches. The meaning of the strings is
// profile-defined and opaque to the core (e.g. citeck encodes sourceId + workspace, using its own
// sentinels for "all" / "unknown" scopes).
type ResourceScope struct {
	Resource string
	Scope    string
}

// Operation is a profile's normalized view of a request, used for policy matching and audit.
type Operation struct {
	Kind      string // "read" | "write" | "" (unknown)
	Resources []ResourceScope
	Method    string
	Path      string
}

// Rule is a stored policy rule for a profile: an opaque, profile-defined params blob plus its ID.
type Rule struct {
	ID     string
	Params json.RawMessage
}

// RuleField describes one editable field of a profile rule, so a UI can render an editor.
type RuleField struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Type    string   `json:"type"` // "text" | "enum"
	Options []string `json:"options,omitempty"`
}

// RuleSchema describes a profile's rule shape for the UI.
type RuleSchema struct {
	Profile string      `json:"profile"`
	Fields  []RuleField `json:"fields"`
}

// Profile classifies and authorizes requests for one kind of upstream server.
type Profile interface {
	Name() string
	// Classify reports whether THIS profile handles the request (handled=true). When handled is
	// false the caller uses the generic raw-http engine. A parse error returns handled=false.
	Classify(req Request) (op Operation, handled bool, err error)
	// Match evaluates one of this profile's rules against a handled operation, returning an outcome
	// (Allow/Deny/RequireApproval) and whether the rule matched at all.
	Match(rule Rule, op Operation) (outcome string, matched bool, err error)
	RuleSchema() RuleSchema
}

var (
	mu       sync.RWMutex
	registry = map[string]Profile{}
)

// Register adds a profile under name. Intended to be called from a plugin's init(). A duplicate
// name overwrites (last registration wins) — plugins use unique names.
func Register(name string, p Profile) {
	mu.Lock()
	defer mu.Unlock()
	registry[name] = p
}

// Get returns the registered profile for name.
func Get(name string) (Profile, bool) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := registry[name]
	return p, ok
}

// Names returns the registered profile names (unordered).
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/serverprofile/ -run TestRegisterAndGet -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/serverprofile/
git add internal/serverprofile/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(serverprofile): core Profile interface + plugin registry"
```

---

### Task 2: storage — `profile` column on upstreams, `profile`/`profile_params` on rules

**Files:**
- Modify: `internal/store/migrate.go` (`schema` blocks at lines 14-22 and 30-45; `migrations` at line 115-122)
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces: columns `upstreams.profile TEXT NOT NULL DEFAULT 'raw-http'`, `rules.profile TEXT NOT NULL DEFAULT ''`, `rules.profile_params TEXT NOT NULL DEFAULT '{}'`; schema version becomes 2.

- [ ] **Step 1: Write the failing test**

```go
// TestServerProfileColumns: an existing v1 database is upgraded to carry the server-profile columns,
// and a fresh DB has them from the current schema.
func TestServerProfileColumns(t *testing.T) {
	// Fresh DB from current schema.
	s, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	require.NoError(t, err)
	defer s.Close()
	_, err = s.DB().Exec(`SELECT profile FROM upstreams LIMIT 0`)
	require.NoError(t, err)
	_, err = s.DB().Exec(`SELECT profile, profile_params FROM rules LIMIT 0`)
	require.NoError(t, err)

	// Simulate an OLD (v1) database: baseline schema without the new columns, stamped at version 1.
	p := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", p)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE upstreams (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, base_url TEXT NOT NULL, kind TEXT NOT NULL DEFAULT 'http', auth_type TEXT NOT NULL, auth_config BLOB, created_at TEXT NOT NULL);
		CREATE TABLE rules (id TEXT PRIMARY KEY, subject_agent_id TEXT NOT NULL DEFAULT '', upstream_id TEXT NOT NULL, op_method TEXT NOT NULL DEFAULT '', op_path_template TEXT NOT NULL DEFAULT '', op_query_template TEXT NOT NULL DEFAULT '{}', op_body_template TEXT NOT NULL DEFAULT '{}', op_value_policies TEXT NOT NULL DEFAULT '{}', outcome TEXT NOT NULL, rate_limit_per_min INTEGER NOT NULL DEFAULT 0, k8s_namespace TEXT NOT NULL DEFAULT '', k8s_resource TEXT NOT NULL DEFAULT '', k8s_verb TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);
		PRAGMA user_version = 1;`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// Re-open through the runner: the new migration adds the columns.
	s2, err := Open(p)
	require.NoError(t, err)
	defer s2.Close()
	_, err = s2.DB().Exec(`SELECT profile FROM upstreams LIMIT 0`)
	require.NoError(t, err)
	_, err = s2.DB().Exec(`SELECT profile, profile_params FROM rules LIMIT 0`)
	require.NoError(t, err)
	require.Equal(t, len(migrations), userVersion(t, s2))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestServerProfileColumns -v`
Expected: FAIL — `no such column: profile`.

- [ ] **Step 3: Write minimal implementation**

In `internal/store/migrate.go`, edit the `upstreams` CREATE (after the `kind` line) to add:

```sql
	profile     TEXT NOT NULL DEFAULT 'raw-http',
```

so the block reads:

```sql
CREATE TABLE IF NOT EXISTS upstreams (
	id          TEXT PRIMARY KEY,
	name        TEXT NOT NULL UNIQUE,
	base_url    TEXT NOT NULL,
	kind        TEXT NOT NULL DEFAULT 'http',
	profile     TEXT NOT NULL DEFAULT 'raw-http',
	auth_type   TEXT NOT NULL,
	auth_config BLOB,
	created_at  TEXT NOT NULL
);
```

Edit the `rules` CREATE to add two columns after `k8s_verb`:

```sql
	k8s_verb           TEXT NOT NULL DEFAULT '',
	profile            TEXT NOT NULL DEFAULT '',
	profile_params     TEXT NOT NULL DEFAULT '{}',
	created_at         TEXT NOT NULL
);
```

Append a migration step to `migrations` (after the `baseline` entry):

```go
	{"server_profiles", func(tx *sql.Tx) error {
		for _, stmt := range []string{
			`ALTER TABLE upstreams ADD COLUMN profile TEXT NOT NULL DEFAULT 'raw-http'`,
			`ALTER TABLE rules ADD COLUMN profile TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE rules ADD COLUMN profile_params TEXT NOT NULL DEFAULT '{}'`,
		} {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("server_profiles: %w", err)
			}
		}
		return nil
	}},
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS (all store tests, including the existing migration tests).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/store/
git add internal/store/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(store): add server-profile columns (upstreams.profile, rules.profile/profile_params)"
```

---

### Task 3: `upstream.Upstream.Profile` field

**Files:**
- Modify: `internal/upstream/registry.go` (struct lines 95-104; `CreateKind` 130-155; `scan` 231-249; `GetByID`/`GetByName`/`List` SELECTs at 266-309)
- Test: `internal/upstream/registry_test.go` (add a test)

**Interfaces:**
- Consumes: storage columns from Task 2.
- Produces: `Upstream.Profile string`; `CreateKind`/`Create` default it to `"raw-http"`; a new `CreateProfiled(name, baseURL, kind, profile string, auth AuthConfig)` OR an added param — see Step 3 (we add a `SetProfile`-free path by threading `profile` through `CreateKind`).

- [ ] **Step 1: Write the failing test**

```go
func TestUpstreamProfileRoundTrip(t *testing.T) {
	reg := newTestRegistry(t) // existing test helper that opens a store + unlocked vault
	up, err := reg.CreateProfiled("api.example.test", "https://api.example.test", upstream.KindHTTP, "citeck", upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)
	require.Equal(t, "citeck", up.Profile)

	got, err := reg.GetByName("api.example.test")
	require.NoError(t, err)
	require.Equal(t, "citeck", got.Profile)

	// A plain Create defaults to raw-http.
	def, err := reg.Create("plain.test", "https://plain.test", upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)
	require.Equal(t, "raw-http", def.Profile)
}
```

> If `newTestRegistry` does not exist, mirror the setup used by the other tests in this package (open `store.Open(tmp)`, create + unlock a `secret.Vault`, `upstream.NewRegistry(s, v)`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/upstream/ -run TestUpstreamProfileRoundTrip -v`
Expected: FAIL — `Profile` field / `CreateProfiled` undefined.

- [ ] **Step 3: Write minimal implementation**

Add the field to the struct (after `Kind`):

```go
type Upstream struct {
	ID        string
	Name      string
	BaseURL   string
	Kind      string // "http" (default) | "k8s"
	Profile   string // server profile: "raw-http" (default) | a registered plugin name (e.g. "citeck")
	AuthType  string
	Auth      AuthConfig
	CreatedAt time.Time
}
```

Replace `CreateKind` with a profile-aware core and keep the old signatures as thin wrappers:

```go
// Create stores a new raw-http upstream.
func (r *Registry) Create(name, baseURL string, auth AuthConfig) (*Upstream, error) {
	return r.CreateProfiled(name, baseURL, KindHTTP, "raw-http", auth)
}

// CreateKind stores a new upstream of the given kind with the default raw-http profile.
func (r *Registry) CreateKind(name, baseURL, kind string, auth AuthConfig) (*Upstream, error) {
	return r.CreateProfiled(name, baseURL, kind, "raw-http", auth)
}

// CreateProfiled stores a new upstream with an explicit server profile. An empty kind defaults to
// "http"; an empty profile defaults to "raw-http".
func (r *Registry) CreateProfiled(name, baseURL, kind, profile string, auth AuthConfig) (*Upstream, error) {
	if kind == "" {
		kind = KindHTTP
	}
	if profile == "" {
		profile = "raw-http"
	}
	raw, err := json.Marshal(auth)
	if err != nil {
		return nil, fmt.Errorf("marshal auth: %w", err)
	}
	enc, err := r.vault.Encrypt(raw)
	if err != nil {
		return nil, fmt.Errorf("encrypt auth: %w", err)
	}
	up := &Upstream{
		ID: newID(), Name: name, BaseURL: baseURL, Kind: kind, Profile: profile, AuthType: auth.Type,
		Auth: auth, CreatedAt: time.Now().UTC(),
	}
	_, err = r.store.DB().Exec(
		`INSERT INTO upstreams (id, name, base_url, kind, profile, auth_type, auth_config, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		up.ID, up.Name, up.BaseURL, up.Kind, up.Profile, up.AuthType, enc, up.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("insert upstream: %w", err)
	}
	return up, nil
}
```

Update `scan` to read the column and the three SELECT lists. Change the column list everywhere from
`id, name, base_url, kind, auth_type, auth_config, created_at` to
`id, name, base_url, kind, profile, auth_type, auth_config, created_at`, and in `scan`:

```go
func (r *Registry) scan(row interface{ Scan(...any) error }) (*Upstream, error) {
	var (
		up      Upstream
		enc     []byte
		created string
	)
	if err := row.Scan(&up.ID, &up.Name, &up.BaseURL, &up.Kind, &up.Profile, &up.AuthType, &enc, &created); err != nil {
		return nil, err
	}
	// ... unchanged (parse created, decrypt+unmarshal auth) ...
}
```

Apply the new column list to the SELECTs in `GetByID` (line ~266), `GetByName` (line ~280), and `List` (line ~294).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/upstream/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/upstream/
git add internal/upstream/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(upstream): thread server profile through the registry"
```

---

### Task 4: `policy.Rule.Profile` + `ProfileParams`

**Files:**
- Modify: `internal/policy/rule.go` (struct lines 24-43)
- Modify: `internal/policy/registry.go` (`Create` 27-60; `ruleCols` 221; `scanRows` 185-219)
- Test: `internal/policy/registry_test.go` (add a test)

**Interfaces:**
- Produces: `Rule.Profile string`, `Rule.ProfileParams json.RawMessage`; persisted to/from the `profile`/`profile_params` columns.

- [ ] **Step 1: Write the failing test**

```go
func TestProfileRuleRoundTrip(t *testing.T) {
	reg := newTestPolicyRegistry(t) // mirror existing policy tests' store setup
	created, err := reg.Create(policy.Rule{
		UpstreamID:    "up1",
		Outcome:       policy.Allow,
		Profile:       "citeck",
		ProfileParams: json.RawMessage(`{"op":"read","source_id":"emodel/type","workspace":"*"}`),
	})
	require.NoError(t, err)

	got, err := reg.ForUpstream("up1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "citeck", got[0].Profile)
	require.JSONEq(t, `{"op":"read","source_id":"emodel/type","workspace":"*"}`, string(got[0].ProfileParams))
	require.Equal(t, created.ID, got[0].ID)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/policy/ -run TestProfileRuleRoundTrip -v`
Expected: FAIL — `Profile`/`ProfileParams` undefined.

- [ ] **Step 3: Write minimal implementation**

Add to the `Rule` struct (after the k8s dimensions), and import `encoding/json` in `rule.go`:

```go
	// Server-profile rule (empty on raw-http/k8s rules): the profile that owns this rule and its
	// opaque, profile-defined params (see internal/serverprofile).
	Profile       string
	ProfileParams json.RawMessage
```

In `registry.go` `Create`: marshal a default for empty params and extend the INSERT:

```go
	params := in.ProfileParams
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	in.ID = newID()
	in.CreatedAt = time.Now().UTC()
	_, err = r.store.DB().Exec(
		`INSERT INTO rules (id, subject_agent_id, upstream_id, op_method, op_path_template, op_query_template, op_body_template, op_value_policies, outcome, rate_limit_per_min, k8s_namespace, k8s_resource, k8s_verb, profile, profile_params, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.SubjectAgentID, in.UpstreamID, in.OpMethod, in.OpPathTemplate, queryJSON, bodyJSON, policiesJSON,
		in.Outcome, in.RateLimitPerMin,
		in.Namespace, in.Resource, in.Verb,
		in.Profile, string(params),
		in.CreatedAt.Format(time.RFC3339Nano),
	)
```

Extend `ruleCols`:

```go
const ruleCols = `id, subject_agent_id, upstream_id, op_method, op_path_template, op_query_template, op_body_template, op_value_policies, outcome, rate_limit_per_min, k8s_namespace, k8s_resource, k8s_verb, profile, profile_params, created_at`
```

In `scanRows`, add locals and scan the two columns (params as a string then store as RawMessage):

```go
		var (
			rule         Rule
			queryJSON    string
			bodyJSON     string
			policiesJSON string
			profileParam string
			created      string
		)
		if err := rows.Scan(&rule.ID, &rule.SubjectAgentID, &rule.UpstreamID,
			&rule.OpMethod, &rule.OpPathTemplate, &queryJSON, &bodyJSON, &policiesJSON,
			&rule.Outcome, &rule.RateLimitPerMin,
			&rule.Namespace, &rule.Resource, &rule.Verb,
			&rule.Profile, &profileParam, &created); err != nil {
			return nil, err
		}
		rule.ProfileParams = json.RawMessage(profileParam)
```

(`json` is already imported in `registry.go`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/policy/ -v`
Expected: PASS (all policy tests still green).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/policy/
git add internal/policy/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(policy): carry profile + profile_params on rules"
```

---

### Task 5: citeck — EntityRef / sourceId parsing

**Files:**
- Create: `internal/serverprofile/citeck/records.go`
- Test: `internal/serverprofile/citeck/records_test.go`

**Interfaces:**
- Produces (package-internal): `func refSource(ref string) (source string, localID string)` — splits `appName/sourceId@localId`; `source` keeps the optional `appName/` prefix (matched by operator globs); `localID` is the part after the first `@` ("" when no `@`, meaning a create or a bare id).

- [ ] **Step 1: Write the failing test**

```go
package citeck

import "testing"

func TestRefSource(t *testing.T) {
	cases := []struct {
		ref, source, local string
	}{
		{"emodel/type@contract", "emodel/type", "contract"},
		{"type@contract", "type", "contract"},
		{"emodel/type@", "emodel/type", ""}, // create
		{"contract", "", "contract"},        // no '@' → whole thing is localId, empty source
		{"", "", ""},
	}
	for _, c := range cases {
		src, loc := refSource(c.ref)
		if src != c.source || loc != c.local {
			t.Fatalf("refSource(%q) = (%q,%q), want (%q,%q)", c.ref, src, loc, c.source, c.local)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serverprofile/citeck/ -run TestRefSource -v`
Expected: FAIL — `refSource` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// Package citeck is the server-profile plugin for Citeck ECOS upstreams. It classifies and gates
// Records API requests (POST /api/records/{query,mutate,delete}). It is the ONLY package in outwall
// permitted to name "citeck" (see ADR-0034); the core stays platform-agnostic.
package citeck

import "strings"

// refSource splits an EntityRef "appName/sourceId@localId" into its source part (keeping the
// optional "appName/" prefix, which operator globs match against) and its localId. With no '@' the
// whole string is the localId and the source is empty (mirrors EntityRef.valueOf).
func refSource(ref string) (source, localID string) {
	at := strings.IndexByte(ref, '@')
	if at < 0 {
		return "", ref
	}
	return ref[:at], ref[at+1:]
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/serverprofile/citeck/ -run TestRefSource -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/serverprofile/citeck/
git add internal/serverprofile/citeck/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(citeck): EntityRef sourceId parsing"
```

---

### Task 6: citeck — classify Records requests into an Operation

**Files:**
- Modify: `internal/serverprofile/citeck/records.go`
- Test: `internal/serverprofile/citeck/records_test.go`

**Interfaces:**
- Consumes: `serverprofile.Request`, `serverprofile.Operation`, `serverprofile.ResourceScope`; `refSource` (Task 5).
- Produces: `func classify(req serverprofile.Request) (serverprofile.Operation, bool, error)`; scope sentinels `scopeAll`, `scopeUnknown` (package consts) and a `recordsOp(path string) (string, bool)` helper.

- [ ] **Step 1: Write the failing test**

```go
import (
	"net/url"
	"testing"

	"github.com/Sipaha/outwall/internal/serverprofile"
	"github.com/stretchr/testify/require"
)

func req(path, body string) serverprofile.Request {
	return serverprofile.Request{Method: "POST", Path: path, Query: url.Values{}, Body: []byte(body)}
}

func TestClassifyQueryWithWorkspace(t *testing.T) {
	op, ok, err := classify(req("/api/records/query",
		`{"query":{"sourceId":"emodel/type","workspaces":["w1"],"query":{}}}`))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "read", op.Kind)
	require.Equal(t, []serverprofile.ResourceScope{{Resource: "emodel/type", Scope: "w1"}}, op.Resources)
}

func TestClassifyQueryEmptyWorkspacesIsAll(t *testing.T) {
	op, ok, _ := classify(req("/api/records/query", `{"query":{"sourceId":"emodel/type","query":{}}}`))
	require.True(t, ok)
	require.Equal(t, []serverprofile.ResourceScope{{Resource: "emodel/type", Scope: scopeAll}}, op.Resources)
}

func TestClassifyQueryEcosTypeAlsoExtracted(t *testing.T) {
	op, ok, _ := classify(req("/api/records/query", `{"query":{"ecosType":"emodel/type@contract","query":{}}}`))
	require.True(t, ok)
	require.Equal(t, "read", op.Kind)
	require.Contains(t, op.Resources, serverprofile.ResourceScope{Resource: "emodel/type@contract", Scope: scopeAll})
}

func TestClassifyMutateCreateUsesWorkspaceAttr(t *testing.T) {
	op, ok, _ := classify(req("/api/records/mutate",
		`{"records":[{"id":"emodel/type@","attributes":{"_workspace":"w2","name":"x"}}]}`))
	require.True(t, ok)
	require.Equal(t, "write", op.Kind)
	require.Equal(t, []serverprofile.ResourceScope{{Resource: "emodel/type", Scope: "w2"}}, op.Resources)
}

func TestClassifyMutateUpdateWorkspaceUnknown(t *testing.T) {
	op, ok, _ := classify(req("/api/records/mutate",
		`{"records":[{"id":"emodel/type@abc","attributes":{"name":"x"}}]}`))
	require.True(t, ok)
	require.Equal(t, []serverprofile.ResourceScope{{Resource: "emodel/type", Scope: scopeUnknown}}, op.Resources)
}

func TestClassifyDelete(t *testing.T) {
	op, ok, _ := classify(req("/api/records/delete", `{"records":["emodel/type@abc","emodel/other@def"]}`))
	require.True(t, ok)
	require.Equal(t, "write", op.Kind)
	require.Equal(t, []serverprofile.ResourceScope{
		{Resource: "emodel/type", Scope: scopeUnknown},
		{Resource: "emodel/other", Scope: scopeUnknown},
	}, op.Resources)
}

func TestClassifyNonRecordsNotHandled(t *testing.T) {
	_, ok, err := classify(req("/gateway/observer/events", ""))
	require.NoError(t, err)
	require.False(t, ok)
}

func TestClassifyGatewayPrefixHandled(t *testing.T) {
	_, ok, _ := classify(req("/gateway/emodel/api/records/query", `{"query":{"sourceId":"emodel/type","query":{}}}`))
	require.True(t, ok)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serverprofile/citeck/ -run TestClassify -v`
Expected: FAIL — `classify`/`scopeAll`/`scopeUnknown` undefined.

- [ ] **Step 3: Write minimal implementation**

Add to `records.go`:

```go
import (
	"encoding/json"
	"strings"

	"github.com/Sipaha/outwall/internal/serverprofile"
)

// Scope sentinels (profile-internal; opaque to the core). A concrete workspace id is any other
// string.
const (
	scopeAll     = "\x00all"     // request spans ALL workspaces (read with no workspaces filter)
	scopeUnknown = "\x00unknown" // workspace not derivable from the body (update / delete / get-atts)
)

// recordsOp returns the Records operation ("query"/"mutate"/"delete") for a path, matching both the
// direct (/api/records/...) and gateway-prefixed (/gateway/.../api/records/...) forms.
func recordsOp(path string) (string, bool) {
	for _, op := range []string{"query", "mutate", "delete"} {
		if strings.HasSuffix(path, "/api/records/"+op) {
			return op, true
		}
	}
	return "", false
}

func classify(r serverprofile.Request) (serverprofile.Operation, bool, error) {
	if r.Method != "POST" {
		return serverprofile.Operation{}, false, nil
	}
	op, ok := recordsOp(r.Path)
	if !ok {
		return serverprofile.Operation{}, false, nil
	}
	out := serverprofile.Operation{Method: r.Method, Path: r.Path}
	switch op {
	case "query":
		out.Kind = "read"
		var body struct {
			Query struct {
				SourceID   string   `json:"sourceId"`
				EcosType   string   `json:"ecosType"`
				Workspaces []string `json:"workspaces"`
			} `json:"query"`
			Records []string `json:"records"` // get-atts mode
		}
		if err := json.Unmarshal(nonNil(r.Body), &body); err != nil {
			return serverprofile.Operation{}, false, nil // malformed → not handled (never grants)
		}
		if body.Query.SourceID != "" || body.Query.EcosType != "" {
			scopes := workspaceScopes(body.Query.Workspaces)
			for _, src := range nonEmpty(body.Query.SourceID, body.Query.EcosType) {
				for _, ws := range scopes {
					out.Resources = append(out.Resources, serverprofile.ResourceScope{Resource: src, Scope: ws})
				}
			}
		} else {
			// get-atts: read specific records by ref; workspace not derivable.
			for _, ref := range body.Records {
				src, _ := refSource(ref)
				out.Resources = append(out.Resources, serverprofile.ResourceScope{Resource: src, Scope: scopeUnknown})
			}
		}
	case "mutate":
		out.Kind = "write"
		var body struct {
			Records []struct {
				ID         string                 `json:"id"`
				Attributes map[string]any `json:"attributes"`
			} `json:"records"`
		}
		if err := json.Unmarshal(nonNil(r.Body), &body); err != nil {
			return serverprofile.Operation{}, false, nil
		}
		for _, rec := range body.Records {
			src, localID := refSource(rec.ID)
			scope := scopeUnknown
			if localID == "" { // create: workspace may be set explicitly
				if ws, _ := rec.Attributes["_workspace"].(string); ws != "" {
					_, wsLocal := refSource(ws) // _workspace may be a ref; take its localId
					if wsLocal != "" {
						scope = wsLocal
					} else {
						scope = ws
					}
				}
			}
			out.Resources = append(out.Resources, serverprofile.ResourceScope{Resource: src, Scope: scope})
		}
	case "delete":
		out.Kind = "write"
		var body struct {
			Records []string `json:"records"`
		}
		if err := json.Unmarshal(nonNil(r.Body), &body); err != nil {
			return serverprofile.Operation{}, false, nil
		}
		for _, ref := range body.Records {
			src, _ := refSource(ref)
			out.Resources = append(out.Resources, serverprofile.ResourceScope{Resource: src, Scope: scopeUnknown})
		}
	}
	return out, true, nil
}

// workspaceScopes maps a query's workspaces filter to scope tokens: empty → [scopeAll], else the
// concrete ids.
func workspaceScopes(ws []string) []string {
	if len(ws) == 0 {
		return []string{scopeAll}
	}
	return ws
}

func nonEmpty(xs ...string) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

func nonNil(b []byte) []byte {
	if b == nil {
		return []byte("{}")
	}
	return b
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/serverprofile/citeck/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/serverprofile/citeck/
git add internal/serverprofile/citeck/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(citeck): classify Records requests into an operation"
```

---

### Task 7: citeck — Profile impl (`Match`, `RuleSchema`, registration)

**Files:**
- Create: `internal/serverprofile/citeck/citeck.go`
- Test: `internal/serverprofile/citeck/citeck_test.go`

**Interfaces:**
- Consumes: `classify`, scope sentinels (Tasks 5-6); `serverprofile` types/consts; `policy.MatchGlob` is NOT imported (avoid policy dep) — citeck uses its own `matchGlob` re-using the same `*`/`**` semantics (copy the small helper).
- Produces: exported `New() serverprofile.Profile`; `func init() { serverprofile.Register("citeck", New()) }`; `ruleParams` struct.

- [ ] **Step 1: Write the failing test**

```go
package citeck

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/Sipaha/outwall/internal/serverprofile"
	"github.com/stretchr/testify/require"
)

func rule(params string) serverprofile.Rule { return serverprofile.Rule{ID: "r", Params: json.RawMessage(params)} }

func mustClassify(t *testing.T, path, body string) serverprofile.Operation {
	op, ok, err := classify(serverprofile.Request{Method: "POST", Path: path, Query: url.Values{}, Body: []byte(body)})
	require.NoError(t, err)
	require.True(t, ok)
	return op
}

func TestMatchReadAllowedByWorkspaceRule(t *testing.T) {
	p := New()
	op := mustClassify(t, "/api/records/query", `{"query":{"sourceId":"emodel/type","workspaces":["w1"],"query":{}}}`)
	out, matched, err := p.Match(rule(`{"op":"read","source_id":"emodel/*","workspace":"w1"}`), op)
	require.NoError(t, err)
	require.True(t, matched)
	require.Equal(t, serverprofile.Allow, out)
}

func TestMatchReadAllWorkspacesRejectedByConcreteRule(t *testing.T) {
	p := New()
	op := mustClassify(t, "/api/records/query", `{"query":{"sourceId":"emodel/type","query":{}}}`) // scopeAll
	_, matched, _ := p.Match(rule(`{"op":"read","source_id":"emodel/type","workspace":"w1"}`), op)
	require.False(t, matched, "a concrete-workspace rule must not match an all-workspaces read")
}

func TestMatchReadAllWorkspacesAllowedByWildcard(t *testing.T) {
	p := New()
	op := mustClassify(t, "/api/records/query", `{"query":{"sourceId":"emodel/type","query":{}}}`)
	_, matched, _ := p.Match(rule(`{"op":"read","source_id":"emodel/type","workspace":"*"}`), op)
	require.True(t, matched)
}

func TestMatchWriteOpMismatch(t *testing.T) {
	p := New()
	op := mustClassify(t, "/api/records/query", `{"query":{"sourceId":"emodel/type","workspaces":["w1"],"query":{}}}`)
	_, matched, _ := p.Match(rule(`{"op":"write","source_id":"*","workspace":"*"}`), op)
	require.False(t, matched, "a write rule must not match a read")
}

func TestMatchDeleteSourceIdOnly(t *testing.T) {
	p := New()
	op := mustClassify(t, "/api/records/delete", `{"records":["emodel/type@abc"]}`) // scopeUnknown
	// A wildcard-workspace write rule matches (workspace not enforceable for delete).
	_, matched, _ := p.Match(rule(`{"op":"write","source_id":"emodel/type","workspace":"*"}`), op)
	require.True(t, matched)
	// A concrete-workspace rule cannot be proven → does not match.
	_, matched2, _ := p.Match(rule(`{"op":"write","source_id":"emodel/type","workspace":"w1"}`), op)
	require.False(t, matched2)
}

func TestMatchBatchEveryResourceMustPass(t *testing.T) {
	p := New()
	op := mustClassify(t, "/api/records/delete", `{"records":["emodel/type@a","emodel/secret@b"]}`)
	_, matched, _ := p.Match(rule(`{"op":"write","source_id":"emodel/type","workspace":"*"}`), op)
	require.False(t, matched, "a rule covering only one sourceId must not allow a cross-source batch")
}

func TestRegistered(t *testing.T) {
	_, ok := serverprofile.Get("citeck")
	require.True(t, ok)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serverprofile/citeck/ -run 'TestMatch|TestRegistered' -v`
Expected: FAIL — `New`/`ruleParams` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
package citeck

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/Sipaha/outwall/internal/serverprofile"
)

func init() { serverprofile.Register("citeck", New()) }

// New returns the citeck server profile.
func New() serverprofile.Profile { return profile{} }

type profile struct{}

func (profile) Name() string { return "citeck" }

func (profile) Classify(r serverprofile.Request) (serverprofile.Operation, bool, error) {
	return classify(r)
}

func (profile) RuleSchema() serverprofile.RuleSchema {
	return serverprofile.RuleSchema{
		Profile: "citeck",
		Fields: []serverprofile.RuleField{
			{Key: "op", Label: "Operation", Type: "enum", Options: []string{"read", "write"}},
			{Key: "source_id", Label: "Source ID (glob)", Type: "text"},
			{Key: "workspace", Label: "Workspace (glob; not enforced for update/delete)", Type: "text"},
		},
	}
}

// ruleParams is the stored shape of a citeck rule's params blob.
type ruleParams struct {
	Op        string `json:"op"`        // "" | "read" | "write"
	SourceID  string `json:"source_id"` // glob; "" or "*" = any
	Workspace string `json:"workspace"` // glob; "" or "*" = any (ignored for update/delete)
}

func (profile) Match(rule serverprofile.Rule, op serverprofile.Operation) (string, bool, error) {
	var p ruleParams
	if err := json.Unmarshal(nonNil(rule.Params), &p); err != nil {
		return "", false, nil // a malformed rule never grants
	}
	if p.Op != "" && p.Op != op.Kind {
		return "", false, nil
	}
	if len(op.Resources) == 0 {
		return "", false, nil
	}
	for _, res := range op.Resources {
		if !matchSource(p.SourceID, res.Resource) || !matchWorkspace(p.Workspace, res.Scope) {
			return "", false, nil // every touched resource must pass
		}
	}
	return serverprofile.Deny, true, nil // placeholder; overwritten below
}
```

> NOTE — the outcome a profile returns is the **stored rule's outcome**, which the core passes in. But `serverprofile.Rule` only carries params + ID, not the outcome. Resolve this now: add an `Outcome string` field to `serverprofile.Rule` (it is generic — every profile rule has an outcome) and have the core fill it. Update `internal/serverprofile/serverprofile.go`:
>
> ```go
> type Rule struct {
> 	ID      string
> 	Outcome string // "allow" | "deny" | "require-approval" (the stored rule's outcome)
> 	Params  json.RawMessage
> }
> ```
>
> Then the citeck `Match` final return becomes:
>
> ```go
> 	return rule.Outcome, true, nil
> ```
>
> and the test helper `rule(...)` sets it: `serverprofile.Rule{ID: "r", Outcome: serverprofile.Allow, Params: json.RawMessage(params)}`.

Add the matching helpers (own glob, mirroring policy's `*`/`**` semantics, to avoid importing policy):

```go
func matchSource(ruleSrc, src string) bool {
	if ruleSrc == "" || ruleSrc == "*" {
		return true
	}
	return matchGlob(ruleSrc, src)
}

// matchWorkspace: an empty/"*" rule workspace matches anything (incl. all/unknown). A concrete rule
// workspace matches only a concrete request workspace via glob — never scopeAll/scopeUnknown (those
// cannot be proven to be within a specific workspace).
func matchWorkspace(ruleWs, scope string) bool {
	if ruleWs == "" || ruleWs == "*" {
		return true
	}
	switch scope {
	case scopeAll, scopeUnknown:
		return false
	default:
		return matchGlob(ruleWs, scope)
	}
}

var globCache = map[string]*regexp.Regexp{}

// matchGlob: '*' matches within one '/'-delimited segment, '**' across segments.
func matchGlob(pattern, s string) bool {
	re, ok := globCache[pattern]
	if !ok {
		var b strings.Builder
		b.WriteString("^")
		for i := 0; i < len(pattern); i++ {
			switch {
			case strings.HasPrefix(pattern[i:], "**"):
				b.WriteString(".*")
				i++
			case pattern[i] == '*':
				b.WriteString("[^/]*")
			default:
				b.WriteString(regexp.QuoteMeta(string(pattern[i])))
			}
		}
		b.WriteString("$")
		re = regexp.MustCompile(b.String())
		globCache[pattern] = re
	}
	return re.MatchString(s)
}
```

> `globCache` is written under the package's test/serial use; if concurrent classification is possible (it is — the proxy is concurrent), guard it with a `sync.Mutex` exactly like `policy.MatchGlob` does. Add:
> ```go
> var globMu sync.Mutex
> ```
> and lock/unlock around the map read+write. (Import `sync`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/serverprofile/... -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/serverprofile/
git add internal/serverprofile/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(citeck): Match (op/sourceId/workspace) + self-registration"
```

---

### Task 8: policy engine — classify via profile, route, skip profile rules in raw-http

**Files:**
- Modify: `internal/policy/decide.go` (`Input` 13-29; `Decide` 98-137)
- Test: `internal/policy/decide_test.go` (add tests; register a fake profile to avoid a citeck import in core tests)

**Interfaces:**
- Consumes: `serverprofile.Get`, `serverprofile.Request`, `serverprofile.Rule`, `serverprofile.Operation`; `Rule.Profile`/`ProfileParams`/`Outcome`.
- Produces: `Input.Profile string`; profile-aware `Decide`.

- [ ] **Step 1: Write the failing test**

```go
package policy_test

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/serverprofile"
	"github.com/stretchr/testify/require"
)

// a tiny fake profile: handles paths starting with "/fake", read for GET, write otherwise; one
// resource {"r","s"}; matches when params {"src":<glob>} matches "r".
type fakeProf struct{}

func (fakeProf) Name() string { return "fake" }
func (fakeProf) Classify(r serverprofile.Request) (serverprofile.Operation, bool, error) {
	if r.Path != "/fake" {
		return serverprofile.Operation{}, false, nil
	}
	kind := "write"
	if r.Method == "GET" {
		kind = "read"
	}
	return serverprofile.Operation{Kind: kind, Resources: []serverprofile.ResourceScope{{Resource: "r", Scope: "s"}}}, true, nil
}
func (fakeProf) Match(rule serverprofile.Rule, op serverprofile.Operation) (string, bool, error) {
	var p struct{ Src string `json:"src"` }
	_ = json.Unmarshal(rule.Params, &p)
	if p.Src == "r" || p.Src == "*" {
		return rule.Outcome, true, nil
	}
	return "", false, nil
}
func (fakeProf) RuleSchema() serverprofile.RuleSchema { return serverprofile.RuleSchema{Profile: "fake"} }

func TestDecideProfileHandledAllow(t *testing.T) {
	serverprofile.Register("fake", fakeProf{})
	reg := newDecideRegistry(t) // helper: a policy.Registry over a temp store
	_, err := reg.Create(policy.Rule{UpstreamID: "up", Outcome: policy.Allow, Profile: "fake", ProfileParams: json.RawMessage(`{"src":"r"}`)})
	require.NoError(t, err)

	d, err := reg.Decide(policy.Input{AgentID: "", UpstreamID: "up", Profile: "fake", Method: "GET", Path: "/fake", Query: url.Values{}})
	require.NoError(t, err)
	require.Equal(t, policy.Allow, d.Outcome)
}

func TestDecideProfileUnhandledFallsToRawHTTP(t *testing.T) {
	serverprofile.Register("fake", fakeProf{})
	reg := newDecideRegistry(t)
	// A raw-http rule (no profile) allowing GET /other.
	_, err := reg.Create(policy.Rule{UpstreamID: "up2", Outcome: policy.Allow, OpMethod: "GET", OpPathTemplate: "/other"})
	require.NoError(t, err)
	// A profile rule on the same upstream must be ignored on the raw-http path.
	_, err = reg.Create(policy.Rule{UpstreamID: "up2", Outcome: policy.Deny, Profile: "fake", ProfileParams: json.RawMessage(`{"src":"*"}`)})
	require.NoError(t, err)

	d, err := reg.Decide(policy.Input{UpstreamID: "up2", Profile: "fake", Method: "GET", Path: "/other", Query: url.Values{}})
	require.NoError(t, err)
	require.Equal(t, policy.Allow, d.Outcome, "non-/fake path uses raw-http rules; the profile deny rule is skipped")
}
```

> `newDecideRegistry` mirrors the existing decide tests' store setup; if those tests already build a `*policy.Registry` over a temp store, reuse that helper instead of adding a new one.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/policy/ -run TestDecideProfile -v`
Expected: FAIL — `Input.Profile` undefined / profile branch missing.

- [ ] **Step 3: Write minimal implementation**

Add the field to `Input` (after `UpstreamID`/`Method`/`Path`):

```go
	// Profile is the upstream's server profile ("raw-http" or a registered plugin name). When set
	// to a registered non-raw-http profile that CLAIMS the request, the profile's rules decide;
	// otherwise evaluation falls through to the raw-http/k8s path (which skips profile rules).
	Profile string
```

Rewrite `Decide` to add the profile branch and the raw-http skip:

```go
func (r *Registry) Decide(in Input) (Decision, error) {
	rules, err := r.ForUpstream(in.UpstreamID)
	if err != nil {
		return Decision{}, err
	}
	// Server-profile path: if the upstream has a registered profile that claims this request, its
	// own rules decide.
	if in.Profile != "" && in.Profile != "raw-http" {
		if prof, ok := serverprofile.Get(in.Profile); ok {
			op, handled, cerr := prof.Classify(serverprofile.Request{Method: in.Method, Path: in.Path, Query: in.Query, Body: in.Body})
			if cerr == nil && handled {
				return decideProfile(prof, in, op, rules)
			}
		} else {
			slog.Warn("unknown server profile; falling back to raw-http", "profile", in.Profile, "upstream", in.UpstreamID)
		}
	}
	k8s := in.Kind == "k8s"
	var agentTier, anyTier []candidate
	for _, rule := range rules {
		if rule.Profile != "" {
			continue // profile rules are evaluated only on the profile path
		}
		var c candidate
		var matched bool
		if k8s {
			if !k8sMatches(rule, in) {
				continue
			}
			c = candidate{rule: rule, outcome: rule.Outcome}
			matched = true
		} else {
			c, matched, err = r.evalHTTPRule(rule, in)
			if err != nil {
				return Decision{}, err
			}
			if !matched {
				continue
			}
		}
		_ = matched
		switch rule.SubjectAgentID {
		case in.AgentID:
			agentTier = append(agentTier, c)
		case "":
			anyTier = append(anyTier, c)
		}
	}
	if d, ok := resolveTier(agentTier); ok {
		return d, nil
	}
	if d, ok := resolveTier(anyTier); ok {
		return d, nil
	}
	return Decision{Outcome: Deny, Rule: nil}, nil
}

// decideProfile evaluates the upstream's profile rules (only those whose Profile == in.Profile)
// against the classified operation, applying the same tier precedence as raw-http.
func decideProfile(prof serverprofile.Profile, in Input, op serverprofile.Operation, rules []*Rule) (Decision, error) {
	var agentTier, anyTier []candidate
	vars := opVars(op)
	for _, rule := range rules {
		if rule.Profile != in.Profile {
			continue
		}
		outcome, matched, err := prof.Match(serverprofile.Rule{ID: rule.ID, Outcome: rule.Outcome, Params: rule.ProfileParams}, op)
		if err != nil {
			return Decision{}, err
		}
		if !matched {
			continue
		}
		c := candidate{rule: rule, outcome: outcome, vars: vars}
		switch rule.SubjectAgentID {
		case in.AgentID:
			agentTier = append(agentTier, c)
		case "":
			anyTier = append(anyTier, c)
		}
	}
	if d, ok := resolveTier(agentTier); ok {
		return d, nil
	}
	if d, ok := resolveTier(anyTier); ok {
		return d, nil
	}
	return Decision{Outcome: Deny, Rule: nil}, nil
}

// opVars renders a profile operation as audit variables (op kind + the touched resources/scopes).
func opVars(op serverprofile.Operation) map[string]string {
	if len(op.Resources) == 0 {
		return map[string]string{"op": op.Kind}
	}
	var srcs, scopes []string
	for _, res := range op.Resources {
		srcs = append(srcs, res.Resource)
		scopes = append(scopes, res.Scope)
	}
	return map[string]string{
		"op":        op.Kind,
		"sourceId":  strings.Join(srcs, ","),
		"workspace": strings.Join(scopes, ","),
	}
}
```

Add imports to `decide.go`: `log/slog` and `github.com/Sipaha/outwall/internal/serverprofile` (`strings` is already imported).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/policy/ -race -v`
Expected: PASS (existing decide tests stay green — they set `Profile: ""`, so the profile branch is skipped).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/policy/
git add internal/policy/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(policy): route requests through the server profile when it claims them"
```

---

### Task 9: proxy — pass the upstream profile into the policy input

**Files:**
- Modify: `internal/proxy/proxy.go` (the `policy.Input{...}` construction in `ServeHTTP`, ~line 184-200)
- Test: `internal/proxy/proxy_test.go` (add an integration test, OR rely on Task 8's unit coverage + a focused proxy test)

**Interfaces:**
- Consumes: `up.Profile` (Task 3), `Input.Profile` (Task 8).

- [ ] **Step 1: Write the failing test**

Add a proxy test that registers a profile-bearing upstream and a citeck-style allow rule, then asserts a Records read is allowed while a write is denied. If the proxy test harness is heavy, instead assert the smaller invariant that the constructed `policy.Input` carries the profile — but prefer the behavioral test:

```go
func TestProxyPassesProfileToPolicy(t *testing.T) {
	// Build the proxy test fixtures (mirror existing proxy_test.go setup): an http upstream created
	// with profile "citeck", an agent, and a citeck allow rule for read on "*".
	// Then POST /<upstream>/api/records/query with a body selecting sourceId emodel/type, workspaces:["w1"].
	// Expect the upstream to receive the forwarded request (200), proving policy allowed it.
	// And POST /api/records/mutate with no matching write rule → 403.
	t.Skip("implement against the existing proxy_test.go fixtures")
}
```

> Replace the `t.Skip` with a real test built on whatever fixtures `internal/proxy/proxy_test.go` already provides (it has a fake upstream server + a policy registry). The key assertions: a classified read is allowed by a citeck read rule; a write with no write rule is 403. Import `_ "github.com/Sipaha/outwall/internal/serverprofile/citeck"` in the test so the plugin is registered.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/proxy/ -run TestProxyPassesProfileToPolicy -v`
Expected: FAIL — profile not forwarded (mutate would be allowed, or read denied) before the change.

- [ ] **Step 3: Write minimal implementation**

In `proxy.go`, where the `policy.Input` is built, add the profile from the resolved upstream `up`:

```go
	dec, err := h.Policy.Decide(policy.Input{
		AgentID:    ag.ID,
		UpstreamID: up.ID,
		Profile:    up.Profile, // server profile: drives Records (citeck) vs raw-http evaluation
		Method:     r.Method,
		Path:       escRelPath,
		Query:      r.URL.Query(),
		Body:       bodyBytes,
		// k8s fields unchanged …
	})
```

(Keep every other field exactly as it is today; only add `Profile: up.Profile`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/proxy/ -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/proxy/
git add internal/proxy/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(proxy): forward the upstream server profile to policy"
```

---

### Task 10: bundle the citeck plugin in the binaries

**Files:**
- Modify: `cmd/outwall/main.go`, `cmd/outwall-desktop/main.go`
- Test: `internal/serverprofile/citeck` registration is already covered (Task 7 `TestRegistered`); add a build-level assertion via an existing cmd smoke test if present, else none.

**Interfaces:**
- Consumes: `citeck.init()` self-registration.

- [ ] **Step 1: Write the failing test**

No new unit test (this is wiring). Verify via build + a manual `Get("citeck")` at runtime. Skip the test step; the gate is the build.

- [ ] **Step 2: Establish the gap**

Confirm citeck is NOT registered in a fresh binary context: `grep -rn "serverprofile/citeck" cmd/` → no matches yet.

- [ ] **Step 3: Write minimal implementation**

Add to the import block of BOTH `cmd/outwall/main.go` and `cmd/outwall-desktop/main.go`:

```go
	// Bundle server-profile plugins (self-register via init()). The core never imports these; the
	// binary entrypoint opts each one in. Add a line here for each new platform plugin.
	_ "github.com/Sipaha/outwall/internal/serverprofile/citeck"
```

- [ ] **Step 4: Verify the build**

Run: `CGO_ENABLED=0 go build ./... && go build -tags desktop -o /tmp/owd ./cmd/outwall-desktop`
Expected: both succeed. (`grep -rn "serverprofile/citeck" cmd/` now shows both mains.)

- [ ] **Step 5: Commit**

```bash
git add cmd/outwall/main.go cmd/outwall-desktop/main.go
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(cmd): bundle the citeck server-profile plugin"
```

---

### Task 11: control-API — profile on upstream create/list; profile params on rules; `GET /profiles`

**Files:**
- Modify: `internal/daemon/admin.go` (`apiMux` 28-61; `hUpstreamCreate` 184-202; `hUpstreamList` 336-364; `hRuleCreate` 396-431; `hRuleList` 433-451)
- Test: `internal/daemon/admin_test.go` (mirror existing handler tests)

**Interfaces:**
- Consumes: `upstream.CreateProfiled` (Task 3), `policy.Rule.Profile`/`ProfileParams` (Task 4), `serverprofile.Names`/`Get` (Task 1).
- Produces: routes accept/return the new fields; `GET /profiles` returns `[{name, fields}]`.

- [ ] **Step 1: Write the failing test**

```go
func TestUpstreamCreateWithProfile(t *testing.T) {
	d := newTestDaemon(t) // existing helper with an unlocked vault
	rec := doJSON(t, d, "POST", "/upstreams", `{"name":"c.test","base_url":"https://c.test","profile":"citeck","auth":{"type":"none"}}`)
	require.Equal(t, 200, rec.Code)

	list := doJSON(t, d, "GET", "/upstreams", "")
	require.Contains(t, list.Body.String(), `"profile":"citeck"`)
}

func TestRuleCreateWithProfileParams(t *testing.T) {
	d := newTestDaemon(t)
	// create an upstream to bind to (or reuse an id); then a citeck rule:
	rec := doJSON(t, d, "POST", "/rules",
		`{"upstream_id":"up","outcome":"allow","profile":"citeck","profile_params":{"op":"read","source_id":"emodel/type","workspace":"*"}}`)
	require.Equal(t, 200, rec.Code)
	list := doJSON(t, d, "GET", "/rules", "")
	require.Contains(t, list.Body.String(), `"profile":"citeck"`)
	require.Contains(t, list.Body.String(), `"source_id":"emodel/type"`)
}

func TestProfilesEndpoint(t *testing.T) {
	d := newTestDaemon(t)
	rec := doJSON(t, d, "GET", "/profiles", "")
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Body.String(), `"citeck"`)
}
```

> Use the package's existing helpers (`newTestDaemon`, a JSON request helper). The names above are placeholders for whatever `admin_test.go` already uses. Import `_ ".../internal/serverprofile/citeck"` in the test so `/profiles` shows citeck.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run 'Profile' -v`
Expected: FAIL — fields not decoded / `/profiles` route missing.

- [ ] **Step 3: Write minimal implementation**

`hUpstreamCreate`: add `Profile` to the body struct and call `CreateProfiled`:

```go
	var body struct {
		Name    string              `json:"name"`
		BaseURL string              `json:"base_url"`
		Kind    string              `json:"kind"`
		Profile string              `json:"profile"`
		Auth    upstream.AuthConfig `json:"auth"`
	}
	// …decode…
	up, err := d.upstreams.CreateProfiled(body.Name, body.BaseURL, body.Kind, body.Profile, body.Auth)
```

`hUpstreamList`: include `"profile"` in the per-upstream map:

```go
		m := map[string]any{
			"id": u.ID, "name": u.Name, "base_url": u.BaseURL, "auth_type": u.AuthType,
			"kind": u.Kind, "profile": u.Profile,
		}
```

`hRuleCreate`: add the two fields to the body struct and pass them to `policy.Create`:

```go
		// server-profile rule fields:
		Profile       string          `json:"profile"`
		ProfileParams json.RawMessage `json:"profile_params"`
```
```go
	rule, err := d.policy.Create(policy.Rule{
		// …existing fields…
		Profile:       body.Profile,
		ProfileParams: body.ProfileParams,
	})
```
(Add `"encoding/json"` to admin.go imports if not present.)

`hRuleList`: include `"profile"` and `"profile_params"`:

```go
			"profile":        rule.Profile,
			"profile_params": rule.ProfileParams,
```
(`json.RawMessage` marshals through as raw JSON.)

Add the `/profiles` route in `apiMux` (next to the rules routes):

```go
	mux.HandleFunc("GET /profiles", d.hProfileList)
```

and the handler:

```go
// hProfileList returns the registered server profiles and their rule schemas, so the UI can render
// the right rule editor per profile. "raw-http" is implicit and not listed here.
func (d *Daemon) hProfileList(w http.ResponseWriter, _ *http.Request) {
	out := make([]serverprofile.RuleSchema, 0)
	for _, name := range serverprofile.Names() {
		if p, ok := serverprofile.Get(name); ok {
			out = append(out, p.RuleSchema())
		}
	}
	writeJSON(w, http.StatusOK, out)
}
```

(Add `github.com/Sipaha/outwall/internal/serverprofile` to admin.go imports.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/daemon/ -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/daemon/
git add internal/daemon/
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(controlapi): server profile on upstreams/rules + GET /profiles"
```

---

### Task 12: web — types + API client for profiles

**Files:**
- Modify: `web/src/lib/types.ts` (Upstream ~47-62; Rule ~97-111), `web/src/lib/api.ts` (createUpstream ~110-115; createRule ~201-202; add getProfiles)
- Test: covered by component tests in Tasks 13-14.

**Interfaces:**
- Produces: `Upstream.profile?: string`; `Rule.profile?: string`, `Rule.profile_params?: Record<string, unknown>`; `createUpstream(name, baseURL, auth, profile?)`; `getProfiles(): Promise<ProfileSchema[]>` with `ProfileSchema = { profile: string; fields: { key: string; label: string; type: string; options?: string[] }[] }`.

- [ ] **Step 1: Write the failing test**

(Type/client-only; assert via the Task 13/14 component tests. No standalone test here.)

- [ ] **Step 2: Establish the gap**

`grep -n "profile" web/src/lib/types.ts web/src/lib/api.ts` → no matches.

- [ ] **Step 3: Write minimal implementation**

In `types.ts`, add to `Upstream`: `profile?: string`. Add to `Rule`: `profile?: string` and `profile_params?: Record<string, unknown>`. Add:

```ts
export interface ProfileField { key: string; label: string; type: string; options?: string[] }
export interface ProfileSchema { profile: string; fields: ProfileField[] }
```

In `api.ts`, extend `createUpstream` to pass an optional profile and add `getProfiles`:

```ts
export function createUpstream(name: string, baseURL: string, auth: UpstreamAuthConfig, profile?: string) {
  return request<{ id: string }>('POST', '/upstreams', { name, base_url: baseURL, auth, profile })
}

export function getProfiles() {
  return request<ProfileSchema[]>('GET', '/profiles')
}
```

(`createRule` already posts the whole rule object; ensure its `Rule` type now permits `profile`/`profile_params` so callers can include them — no signature change needed.)

- [ ] **Step 4: Type-check**

Run: `cd web && pnpm lint`
Expected: no type errors.

- [ ] **Step 5: Commit**

```bash
cd web && git -c user.name=Sipaha -c user.email=sipahabk@gmail.com -C .. add web/src/lib/types.ts web/src/lib/api.ts
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com -C .. commit -m "feat(web): profile types + getProfiles client"
```

> (Run git from the repo root; the `-C ..` form above assumes cwd is `web/`. Simpler: `cd ..` first, then `git add web/src/lib/*.ts && git -c … commit`.)

---

### Task 13: web — Server-type selector on the add-host form

**Files:**
- Modify: `web/src/pages/Upstreams.tsx` (add-host modal ~532-575; `submitAdd` ~392)
- Test: `web/src/pages/Upstreams.test.tsx`

**Interfaces:**
- Consumes: `createUpstream(..., profile)` (Task 12), `getProfiles` (Task 12).
- Produces: a `Server type` `<select>` (`Raw HTTP` = `raw-http`, plus each profile name); selected value passed to `createUpstream`.

- [ ] **Step 1: Write the failing test**

```ts
it('submits the chosen server profile when adding a host', async () => {
  vi.spyOn(api, 'listUpstreams').mockResolvedValue([])
  vi.spyOn(api, 'getProfiles').mockResolvedValue([
    { profile: 'citeck', fields: [] },
  ])
  const createSpy = vi.spyOn(api, 'createUpstream').mockResolvedValue({ id: 'new' })
  render(<Upstreams />)

  fireEvent.click(screen.getByRole('button', { name: 'Add host' }))
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'c' } })
  fireEvent.change(screen.getByLabelText('Base URL'), { target: { value: 'https://c.test' } })
  fireEvent.change(screen.getByLabelText('Server type'), { target: { value: 'citeck' } })
  fireEvent.click(screen.getByRole('button', { name: 'Create' }))

  await waitFor(() =>
    expect(createSpy).toHaveBeenCalledWith('c', 'https://c.test', { type: 'none' }, 'citeck'),
  )
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && pnpm test -- Upstreams`
Expected: FAIL — no `Server type` control / profile not passed.

- [ ] **Step 3: Write minimal implementation**

In `Upstreams.tsx`: load profiles on mount (`getProfiles`) into state `profiles`; add a `profile` state defaulting to `'raw-http'`; render a labelled select in the add-host modal (before AuthFields):

```tsx
<FormField label="Server type">
  <select
    aria-label="Server type"
    value={profile}
    onChange={(e) => setProfile(e.target.value)}
  >
    <option value="raw-http">Raw HTTP</option>
    {profiles.map((p) => (
      <option key={p.profile} value={p.profile}>{p.profile}</option>
    ))}
  </select>
</FormField>
```

and in `submitAdd`, pass it: `await createUpstream(name, baseURL, auth, profile)`. (Mirror the existing `FormField`/select idiom already used by `AuthFields` for consistent styling.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && pnpm test -- Upstreams`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd web && pnpm lint && pnpm build && cd ..
git checkout -- internal/daemon/webdist/index.html
git add web/src/pages/Upstreams.tsx web/src/pages/Upstreams.test.tsx
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(web): server-type selector on the add-host form"
```

---

### Task 14: web — Citeck Records-rule editor in the rules screen

**Files:**
- Modify: `web/src/pages/Rules.tsx` (add-operation modal ~624-746; `submit` ~427)
- Test: `web/src/pages/Rules.test.tsx`

**Interfaces:**
- Consumes: `listUpstreams` (to know the selected upstream's `profile`), `createRule` with `profile`/`profile_params` (Task 12).
- Produces: when the selected upstream's profile is `citeck`, the modal shows Records-rule fields (op select read/write, sourceId text, workspace text) and posts `{ upstream_id, subject_agent_id, outcome, profile: 'citeck', profile_params: { op, source_id, workspace } }`; the existing raw-http section remains available for non-Records paths.

- [ ] **Step 1: Write the failing test**

```ts
it('creates a citeck Records rule', async () => {
  vi.spyOn(api, 'listUpstreams').mockResolvedValue([
    { id: 'up', name: 'c.test', base_url: 'https://c.test', auth_type: 'none', profile: 'citeck' },
  ])
  vi.spyOn(api, 'listRules').mockResolvedValue([])
  vi.spyOn(api, 'listAgents').mockResolvedValue([])
  const createSpy = vi.spyOn(api, 'createRule').mockResolvedValue({ id: 'r1' })
  render(<Rules />)

  fireEvent.click(screen.getByRole('button', { name: /add/i }))
  fireEvent.change(screen.getByLabelText('Host'), { target: { value: 'up' } })
  fireEvent.change(screen.getByLabelText('Records operation'), { target: { value: 'read' } })
  fireEvent.change(screen.getByLabelText('Source ID'), { target: { value: 'emodel/type' } })
  fireEvent.change(screen.getByLabelText('Workspace'), { target: { value: 'w1' } })
  fireEvent.click(screen.getByRole('button', { name: 'Create' }))

  await waitFor(() =>
    expect(createSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        upstream_id: 'up',
        outcome: 'allow',
        profile: 'citeck',
        profile_params: { op: 'read', source_id: 'emodel/type', workspace: 'w1' },
      }),
    ),
  )
})
```

> Adapt the mocked API names (`listAgents` etc.) and the "Add" button label to what `Rules.tsx` actually uses (the Explore map shows agents/upstreams are loaded; check the current code).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && pnpm test -- Rules`
Expected: FAIL — no Records-rule fields.

- [ ] **Step 3: Write minimal implementation**

In `Rules.tsx`, derive the selected upstream's profile from the chosen `upstream_id` and the loaded upstreams list. When `profile === 'citeck'`, render (in place of the HTTP method/path fields) a Records section:

```tsx
{selectedProfile === 'citeck' ? (
  <>
    <FormField label="Records operation">
      <select aria-label="Records operation" value={recOp} onChange={(e) => setRecOp(e.target.value)}>
        <option value="read">read (query)</option>
        <option value="write">write (mutate/delete)</option>
      </select>
    </FormField>
    <FormField label="Source ID">
      <input aria-label="Source ID" value={sourceId} onChange={(e) => setSourceId(e.target.value)} placeholder="emodel/type or *" />
    </FormField>
    <FormField label="Workspace">
      <input aria-label="Workspace" value={workspace} onChange={(e) => setWorkspace(e.target.value)} placeholder="* (not enforced for update/delete)" />
    </FormField>
  </>
) : (
  /* existing HTTP / k8s fields unchanged */
)}
```

In `submit`, branch on the profile:

```ts
if (selectedProfile === 'citeck') {
  await createRule({
    subject_agent_id: subject,
    upstream_id: upstreamID,
    outcome,
    rate_limit_per_min: rateLimit,
    profile: 'citeck',
    profile_params: { op: recOp, source_id: sourceId, workspace: workspace },
  })
} else {
  /* existing createRule(payload) for http/k8s */
}
```

Keep the existing raw-http/k8s editor reachable for a citeck upstream's non-Records paths — i.e. do NOT remove the HTTP fields globally; the simplest scope-correct approach is a small "Rule kind" toggle (`Records` | `Raw HTTP`) shown only when the profile is citeck, defaulting to `Records`. If a toggle is too much for this task, ship Records-only here and add the raw-http-on-citeck toggle as a follow-up task (note it in the plan's "Deferred" list).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && pnpm test -- Rules`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd web && pnpm lint && pnpm build && cd ..
git checkout -- internal/daemon/webdist/index.html
git add web/src/pages/Rules.tsx web/src/pages/Rules.test.tsx
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "feat(web): citeck Records-rule editor"
```

---

### Task 15: full gate + ADR-0034 + rule relaxation in AGENTS.md/memory

**Files:**
- Create: `docs/architecture/decisions/0034-server-profiles-and-citeck-plugin.md`
- Modify: `docs/INDEX.md` (ADR list), `CLAUDE.md`/`AGENTS.md` (the "No `citeck`" hard rule), the memory note `project-identity` (the user maintains memory; this repo edit covers AGENTS.md — leave the memory file to the agent's memory tooling).

**Interfaces:** none (docs + gate).

- [ ] **Step 1: Run the full gate**

```bash
gofmt -w . && go vet ./... && go test ./... -race && CGO_ENABLED=0 go build ./...
go build -tags desktop -o /tmp/owd ./cmd/outwall-desktop
cd web && pnpm test && pnpm lint && pnpm build && cd ..
git checkout -- internal/daemon/webdist/index.html
```
Expected: all green.

- [ ] **Step 2: Write ADR-0034**

Create `docs/architecture/decisions/0034-server-profiles-and-citeck-plugin.md` following the existing ADR format (status accepted, date 2026-06-22). Cover: the `Profile` interface + global self-registration registry; the per-upstream `profile` column and the rule `profile`/`profile_params` columns; how `decide.go` delegates to a profile that claims a request and skips profile rules on the raw-http path; the citeck Records classification + read/write × sourceId × workspace rule model and the asymmetric workspace enforceability (query empty=ALL rejected; update/delete sourceId-only); ecosType also gated; batch = every-resource-must-pass; bundling at the cmd entrypoints. **Decision: relax the previous "no citeck" hard rule to "no citeck except inside `internal/serverprofile/citeck` + the `"citeck"` profile-name value"** — record this explicitly as the deliberate, scoped exception with its rationale (operator wants per-platform read/write config; baking Records knowledge into a contained plugin is simpler to configure than a generic data-driven profile).

- [ ] **Step 3: Edit the hard rule in AGENTS.md**

In `CLAUDE.md`/`AGENTS.md` under "Hard rules", change the `No citeck` bullet to the scoped form and reference ADR-0034:

```
- ⚠️ **No `citeck`** strings / imports / branding in the core. The ONE exception (ADR-0034) is the
  server-profile plugin `internal/serverprofile/citeck` and the persisted profile-name value
  `"citeck"`. Everywhere else (core, proxy, daemon, store, policy, UI chrome) stays citeck-free.
```

- [ ] **Step 4: Link ADR in INDEX**

Add the ADR-0034 line to the `decisions/` list in `docs/INDEX.md`.

- [ ] **Step 5: Commit**

```bash
git add docs/ CLAUDE.md AGENTS.md
git -c user.name=Sipaha -c user.email=sipahabk@gmail.com commit -m "docs(adr-0034): server profiles + citeck plugin; scope the no-citeck rule"
```

> After this commit, push: `git push origin main` (durably authorized). Then update the agent memory note `project-identity` to record the scoped citeck exception (via the memory tooling, not a repo file).

---

## Deferred (out of this plan)

- Raw-http-on-citeck rule editor toggle in the UI (if Task 14 shipped Records-only) — a small follow-up so an operator can add e.g. `GET /gateway/observer/**` allow rules on a citeck upstream from the UI. The backend already supports it (a citeck upstream evaluates raw-http rules for non-Records paths).
- MCP surfacing of the profile to agents (not needed for operator-driven policy).
- Per-upstream origin / browser browsing — **separate plan** (`2026-06-22-per-upstream-origin.md`, ADR-0035).

## Self-review notes

- **Spec coverage:** Part A → Tasks 1-4, 8-11; Part B → Tasks 5-9; Part C → Tasks 12-14; plugin self-registration/bundling → Tasks 1,7,10; docs/rule-flip → Task 15. Part D is explicitly a separate plan.
- **Workspace semantics** (the spec's trickiest point) are pinned by tests: empty `workspaces` = ALL rejected by a concrete rule (Task 6/7), update/delete sourceId-only (Task 7), ecosType gated (Task 6), batch every-resource-must-pass (Task 7).
- **Naming consistency:** `CreateProfiled`, `Input.Profile`, `Rule.Profile`/`ProfileParams`, `serverprofile.Rule{ID,Outcome,Params}`, `Operation{Kind,Resources,Method,Path}`, `ResourceScope{Resource,Scope}`, scope sentinels `scopeAll`/`scopeUnknown`, profile name `"citeck"`, default `"raw-http"` — used identically across tasks.
</content>
