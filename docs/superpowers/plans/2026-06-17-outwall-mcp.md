# outwall — Plan 3: MCP Control Plane (streamable HTTP)

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development or superpowers:executing-plans.

**Goal:** The single MCP server agents talk to. Over streamable HTTP on a localhost port, it
exposes four tools — `list_upstreams`, `request_access(host_or_upstream, purpose)`,
`get_access(upstream)`, `whoami` — auto-registers an agent on first contact, mints its
data-plane bearer token, and records access-request intents (with the agent's stated purpose)
for the operator. Access itself remains rule-derived (Plan 2 policy); `request_access` reports
the current status and logs the intent.

**Architecture:** A SDK-free domain service `internal/mcpsvc` holds all the logic (resolve a
host/upstream, derive the agent's per-upstream status from policy rules, build tool responses)
and is fully unit-tested. A thin adapter `internal/mcp` wires `mcpsvc` to the official
`github.com/modelcontextprotocol/go-sdk` — registers the four tools, serves
`StreamableHTTPHandler`, and binds each MCP session to a get-or-created agent (session = agent
presence). A new `internal/access` registry persists access-request intents. The daemon serves
`/mcp` on a **separate** localhost listener (`MCPListen`, default `127.0.0.1:8181`) so the
privileged control plane is not co-mingled with the data-plane proxy port.

**Tech Stack:** Plan 1/2 stack + `github.com/modelcontextprotocol/go-sdk` (official Go MCP SDK).
**Use the latest released version** (`go get github.com/modelcontextprotocol/go-sdk/mcp@latest`)
— do not pin to an old tag. Per ADR-0001 this external dep is acceptable in alpha.

## Global Constraints

(All Plan 1/2 constraints apply.) Plus:
- The MCP adapter is the ONLY package importing the go-sdk. `mcpsvc` must NOT import it (keeps
  the logic SDK-version-independent and unit-testable).
- **Always use the latest released dependency versions** (user directive). For the go-sdk, `go
  get …@latest`; if any other dep is added, take its latest stable too. Do not introduce old pins.
- **Verify the SDK API against the resolved version with `go doc` before writing adapter code** —
  the signatures below are from the v1.2.0 docs and may differ in the latest; confirm
  `mcp.AddTool`, the handler signature, `mcp.StreamableHTTPHandler`, the client/transport
  constructors, and the session/client-info accessors with `go doc
  github.com/modelcontextprotocol/go-sdk/mcp`. Adapt to the real signatures; do not fight the
  compiler against a doc snippet.

## File Structure

```
Create:  internal/access/registry.go        # access_requests table + CRUD (intent log)
         internal/access/registry_test.go
         internal/mcpsvc/service.go           # SDK-free domain logic for the 4 tools
         internal/mcpsvc/service_test.go
         internal/mcp/server.go               # go-sdk adapter: tools + StreamableHTTPHandler + session→agent
         internal/mcp/server_test.go          # integration test via the SDK client
Modify:  internal/store/migrate.go            # + access_requests table
         internal/daemon/daemon.go            # build mcpsvc + mcp handler; serve MCPListen; Config.MCPListen
         internal/daemon/admin.go             # GET /access-requests, POST /access-requests/{id}/resolve
         internal/daemon/admin_test.go
         internal/cli/root.go                 # --mcp-listen flag
         internal/cli/access.go (new)         # `outwall access list|resolve`
         go.mod / go.sum                       # + go-sdk
```

---

### Task 1: access-request registry

**Files:** Modify `internal/store/migrate.go`; create `internal/access/registry.go`, `internal/access/registry_test.go`.

**Interfaces:**
- New table:
```sql
CREATE TABLE IF NOT EXISTS access_requests (
	id          TEXT PRIMARY KEY,
	agent_id    TEXT NOT NULL,
	upstream_id TEXT NOT NULL,
	purpose     TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL DEFAULT 'pending',   -- pending | granted | denied | dismissed
	created_at  TEXT NOT NULL,
	resolved_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS access_requests_by_status ON access_requests(status);
```
- `access.Request struct { ID, AgentID, UpstreamID, Purpose, Status string; CreatedAt time.Time; ResolvedAt string }`
- `access.NewRegistry(s *store.Store) *access.Registry`
- `(*Registry).Create(agentID, upstreamID, purpose string) (*Request, error)` — status `pending`.
- `(*Registry).List() ([]*Request, error)` — newest first.
- `(*Registry).Pending() ([]*Request, error)` — status=pending.
- `(*Registry).Resolve(id, status string) error` — sets status (`granted`/`denied`/`dismissed`) + resolved_at=now; validates status; `ErrNotFound` if absent.
- `access.ErrNotFound`.

- [ ] **Step 1: failing test** — `internal/access/registry_test.go`:
```go
package access

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/store"
)

func newReg(t *testing.T) *Registry {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "acc.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return NewRegistry(s)
}

func TestAccessRequestLifecycle(t *testing.T) {
	reg := newReg(t)
	r, err := reg.Create("a1", "u1", "triage issues")
	require.NoError(t, err)
	require.Equal(t, "pending", r.Status)

	p, err := reg.Pending()
	require.NoError(t, err)
	require.Len(t, p, 1)
	require.Equal(t, "triage issues", p[0].Purpose)

	require.NoError(t, reg.Resolve(r.ID, "granted"))
	require.ErrorIs(t, reg.Resolve("nope", "granted"), ErrNotFound)
	require.Error(t, reg.Resolve(r.ID, "bogus")) // invalid status

	p, _ = reg.Pending()
	require.Empty(t, p) // no longer pending
}
```

- [ ] **Step 2: run** → FAIL. **Step 3: implement** (mirror the Plan 2 `policy.Registry` CRUD
  style — `newID()` via crypto/rand+hex, `time.RFC3339Nano`). Validate `status ∈
  {pending,granted,denied,dismissed}` in Resolve. **Step 4: run** → PASS. **Step 5: commit**
  `feat(access): access-request intent registry`.

---

### Task 2: `mcpsvc` domain service (SDK-free)

**Files:** Create `internal/mcpsvc/service.go`, `internal/mcpsvc/service_test.go`.

**Interfaces:**
```go
type Service struct {
	agents    *agent.Registry
	upstreams *upstream.Registry
	policy    *policy.Registry
	access    *access.Registry
}
func New(a *agent.Registry, u *upstream.Registry, p *policy.Registry, ac *access.Registry) *Service

// Result types (plain structs the adapter marshals to tool output):
type UpstreamInfo struct { Name, BaseURL, Status string } // Status: open|needs-request|denied
type AccessResult struct { Status, BasePath, Memo string } // Status: granted|pending-approval|denied
type Identity struct { AgentID, Name, Status string; Accesses []string }

func (s *Service) ListUpstreams(agentID string) ([]UpstreamInfo, error)
func (s *Service) RequestAccess(agentID, hostOrUpstream, purpose string) (AccessResult, error)
func (s *Service) GetAccess(agentID, upstreamName string) (AccessResult, error)
func (s *Service) WhoAmI(agentID string) (Identity, error)
```

Status derivation (single source of truth — `statusFor`):
- Gather `policy.ForUpstream(up.ID)`, keep rules whose subject is `agentID` or `""`.
- If any **agent-tier** rule with outcome `deny` ⇒ `"denied"`.
- Else if any rule (either tier) with outcome `allow` or `require-approval` ⇒ `"open"`.
- Else ⇒ `"needs-request"`.
- (Maps to AccessResult: open→granted, needs-request→pending-approval, denied→denied.)

`RequestAccess`: resolve the upstream (by name; else by base-URL host match); if none ⇒
`AccessResult{Status:"denied", Memo:"no such upstream — ask the operator to add it"}` and do
NOT create a record. Else `access.Create(agentID, up.ID, purpose)` (log the intent) and return
the rule-derived status. `GetAccess`: like RequestAccess but no record + requires the upstream
to be already `open` (else Status reflects the derivation; Memo lists matching allow/approval
rules as `METHOD PATH`). `BasePath` is `"/"+up.Name` when granted/open.

- [ ] **Step 1: failing test** — `internal/mcpsvc/service_test.go`:
```go
package mcpsvc

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/access"
	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
	"github.com/Sipaha/outwall/internal/upstream"
)

func build(t *testing.T) (*Service, *agent.Registry, *upstream.Registry, *policy.Registry) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "m.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	v := secret.NewVault(s)
	require.NoError(t, v.Init("pw"))
	ag := agent.NewRegistry(s)
	up := upstream.NewRegistry(s, v)
	pol := policy.NewRegistry(s)
	acc := access.NewRegistry(s)
	return New(ag, up, pol, acc), ag, up, pol
}

func TestRequestAccessFlow(t *testing.T) {
	svc, ag, up, pol := build(t)
	a, _, _ := ag.Register("claude")
	u, _ := up.Create("github", "https://api.github.com", upstream.AuthConfig{Type: "none"})

	// No rule yet → pending-approval, and an access-request is logged.
	res, err := svc.RequestAccess(a.ID, "github", "triage issues")
	require.NoError(t, err)
	require.Equal(t, "pending-approval", res.Status)

	// Resolving by HOST also works.
	res, _ = svc.RequestAccess(a.ID, "api.github.com", "via host")
	require.Equal(t, "pending-approval", res.Status)

	// Unknown upstream → denied, no record.
	res, _ = svc.RequestAccess(a.ID, "nope.example.com", "x")
	require.Equal(t, "denied", res.Status)

	// Operator grants via an allow rule → granted with base path.
	_, err = pol.Create(policy.Rule{UpstreamID: u.ID, Method: "*", PathGlob: "/**", Outcome: policy.Allow})
	require.NoError(t, err)
	res, _ = svc.GetAccess(a.ID, "github")
	require.Equal(t, "granted", res.Status)
	require.Equal(t, "/github", res.BasePath)

	// list_upstreams reflects open status.
	list, _ := svc.ListUpstreams(a.ID)
	require.Len(t, list, 1)
	require.Equal(t, "open", list[0].Status)

	// whoami
	id, _ := svc.WhoAmI(a.ID)
	require.Equal(t, a.ID, id.AgentID)
	require.Contains(t, id.Accesses, "github")

	// agent-specific deny overrides → denied.
	_, err = pol.Create(policy.Rule{SubjectAgentID: a.ID, UpstreamID: u.ID, Method: "*", PathGlob: "/**", Outcome: policy.Deny})
	require.NoError(t, err)
	res, _ = svc.GetAccess(a.ID, "github")
	require.Equal(t, "denied", res.Status)
}
```

- [ ] **Step 2: run** → FAIL. **Step 3: implement** `service.go` (complete, SDK-free) per the
  Interfaces + status derivation. Host resolution: try `upstreams.GetByName`; on `ErrNotFound`,
  `upstreams.List()` and compare `url.Parse(u.BaseURL).Hostname()` to the input (also strip a
  leading scheme if the agent passed `https://…`). **Step 4: run** → PASS. **Step 5: commit**
  `feat(mcpsvc): SDK-free domain service for the four MCP tools`.

---

### Task 3: MCP adapter over the official go-sdk

**Files:** Create `internal/mcp/server.go`, `internal/mcp/server_test.go`. Add the dep at the
**latest** version: `go get github.com/modelcontextprotocol/go-sdk/mcp@latest` then `go mod tidy`.
Record the resolved version in the REPORT.

**Behavior (confirm exact SDK signatures with `go doc` first):**
- `mcp.NewHandler(deps Deps) (http.Handler, error)` where
  `type Deps struct { Svc *mcpsvc.Service; Agents *agent.Registry; Logger *slog.Logger }`.
  Returns an `*sdkmcp.StreamableHTTPHandler` wrapping a configured `*sdkmcp.Server`.
- Register four tools via `sdkmcp.AddTool(server, &sdkmcp.Tool{Name, Description}, handler)`.
  Tool I/O structs (the SDK infers JSON schema from these):
  - `list_upstreams`: In `struct{}`; Out `struct{ Upstreams []mcpsvc.UpstreamInfo \`json:"upstreams"\` }`.
  - `request_access`: In `struct{ Host string \`json:"host"\`; Purpose string \`json:"purpose"\` }`; Out `mcpsvc.AccessResult`.
  - `get_access`: In `struct{ Upstream string \`json:"upstream"\` }`; Out `mcpsvc.AccessResult`.
  - `whoami`: In `struct{}`; Out `struct{ mcpsvc.Identity; Token string \`json:"token"\` }`.
- **Session → agent identity (session = agent presence).** Maintain an in-memory map
  `sessionID → {agentID, token}` guarded by a mutex. On each tool call, derive a stable session
  key from the request (confirm the accessor: e.g. `req.Session.ID()` / `req.Extra` — check
  `go doc`). `agentFor(sessionKey, clientName)`: if unseen, `Agents.Register(name)` where `name`
  = the MCP client's name (from the session's initialize params if reachable, else
  `"mcp-agent"`) + `"-" + shortSessionSuffix`; cache `{agentID, token}`; return it. `whoami`
  returns the cached token (the registry only stores the hash, so the token must come from this
  map — it is minted once at registration).
- Validation: `request_access` with empty `purpose` ⇒ return a tool error
  (`mcp.CallToolResult` with `IsError`) "purpose is required", per the design contract.
- The handler must require the vault be unlocked for `request_access`/`get_access`/`list_upstreams`
  to return real data — if `Deps` is given a `Locked func() bool` (pass the vault's), a locked
  vault makes the tools answer with a clear "vault locked — ask the operator to unlock" message
  rather than erroring opaquely. (Add `Locked func() bool` to `Deps`.)

- [ ] **Step 1: failing integration test** — `internal/mcp/server_test.go`. Use the SDK's
  **client** against an `httptest.Server` wrapping the handler (confirm client API via `go doc`):
```go
package mcp_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	owmcp "github.com/Sipaha/outwall/internal/mcp"
	"github.com/Sipaha/outwall/internal/access"
	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/mcpsvc"
	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
	"github.com/Sipaha/outwall/internal/upstream"
)

func TestMCPWhoamiAndListUpstreams(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "i.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	v := secret.NewVault(s)
	require.NoError(t, v.Init("pw"))
	ag := agent.NewRegistry(s)
	up := upstream.NewRegistry(s, v)
	pol := policy.NewRegistry(s)
	acc := access.NewRegistry(s)
	_, _ = up.Create("github", "https://api.github.com", upstream.AuthConfig{Type: "none"})

	h, err := owmcp.NewHandler(owmcp.Deps{
		Svc: mcpsvc.New(ag, up, pol, acc), Agents: ag, Locked: v.Locked,
	})
	require.NoError(t, err)
	ts := httptest.NewServer(h)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Connect an MCP client over streamable HTTP (confirm the exact client/transport
	// constructor names via `go doc github.com/modelcontextprotocol/go-sdk/mcp`).
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-agent", Version: "0"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	require.NoError(t, err)
	defer session.Close()

	// whoami → returns an agent id + token (auto-registered on first contact).
	who, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "whoami"})
	require.NoError(t, err)
	require.False(t, who.IsError, "%+v", who)

	// list_upstreams → contains github, status needs-request (no rule yet).
	got, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "list_upstreams"})
	require.NoError(t, err)
	require.False(t, got.IsError)
	// Assert on structured content (decode got.StructuredContent / Content[0] text per the SDK).
	require.NotEmpty(t, got.Content)

	// an agent was registered.
	agents, _ := ag.List()
	require.GreaterOrEqual(t, len(agents), 1)
}
```
  (If the exact client constructor / result-shape differs in the pinned version, adapt the test
  to the real API — the REQUIRED assertions are: whoami succeeds, list_upstreams returns github,
  and `ag.List()` shows the auto-registered agent. Keep those assertions meaningful.)

- [ ] **Step 2: run** → FAIL. **Step 3: implement** `server.go` against the real SDK API
  (`go doc` first). **Step 4: run** `go test ./internal/mcp/` → PASS. **Step 5: commit**
  `feat(mcp): streamable-HTTP MCP server (4 tools, session=agent, token mint)`.

---

### Task 4: daemon wiring + admin API + CLI

**Files:** Modify `internal/daemon/daemon.go`, `internal/daemon/admin.go`, `internal/daemon/admin_test.go`, `internal/cli/root.go`; create `internal/cli/access.go`.

**Behavior:**
- `daemon.Config` gains `MCPListen string` (default `127.0.0.1:8181`). `daemon.New` builds
  `access.NewRegistry`, `mcpsvc.New(...)`, and `mcp.NewHandler(...)` (passing `vault.Locked`),
  storing the handler. `Serve` starts a **third** listener (alongside data plane + admin socket)
  on `MCPListen` serving the MCP handler. Shutdown closes it too.
- Admin API: `GET /access-requests` → list (newest first, joined with agent + upstream names
  where convenient — at minimum the ids + purpose + status); `POST /access-requests/{id}/resolve
  {status}` → 200 `{ok:true}` (status ∈ granted/denied/dismissed) or 404. (Granting actual
  access is still done by creating rules via `POST /rules`; resolving the request just clears the
  operator's queue and records the decision.)
- CLI: `--mcp-listen` persistent flag on root (wired into `daemon.Config` in `serve`). New
  `outwall access list` and `outwall access resolve <id> --status granted|denied|dismissed`.

- [ ] **Step 1: failing test** — extend `internal/daemon/admin_test.go`:
```go
func TestAdminAccessRequests(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	// resolving an unknown request → 404
	require.Equal(t, http.StatusNotFound, req(t, h, "POST", "/access-requests/nope/resolve", `{"status":"granted"}`).Code)
	// list is empty-but-OK initially
	require.Equal(t, http.StatusOK, req(t, h, "GET", "/access-requests", "").Code)
}
```

- [ ] **Step 2: run** → FAIL. **Step 3: implement** the wiring (mirror Plan 2 admin handlers;
  `POST /access-requests/{id}/resolve` uses `r.PathValue("id")`; map `access.ErrNotFound` → 404).
  In `daemon.New`, ensure the MCP handler is built AFTER the registries. **Step 4: run**
  `go test ./...` → PASS.

- [ ] **Step 5: full gate + commit**
```bash
gofmt -l . ; go vet ./... ; go test ./... > /tmp/outwall_plan3.txt 2>&1 ; grep -E "FAIL|panic|^ok" /tmp/outwall_plan3.txt ; make build
git add -A && git commit -m "feat: serve MCP listener + access-request admin API/CLI"
```

- [ ] **Step 6: e2e smoke** — `outwall serve` (vault unlocked via pty), then drive the MCP
  endpoint. Easiest is a tiny Go program or `go test`-style client already covered in Task 3;
  for a manual check, confirm `outwall agent list` shows an agent after an MCP `whoami`, and
  `outwall access list` shows the logged intent after a `request_access`. Note any limitation.

---

## Self-Review

- **Spec coverage:** one MCP server, streamable HTTP (Tasks 3–4) ✓; four tools with the exact
  contract (Task 2 logic, Task 3 wiring) ✓; dynamic registration + token mint on first contact
  (Task 3 session=agent) ✓; mandatory `purpose` (Task 3 validation) ✓; intent logged for the
  operator (Tasks 1–2, surfaced Task 4) ✓; vault-locked messaging (Task 3) ✓.
- **Deferred:** audit (Plan 4), control API + SSE to stream access-requests/approvals to the UI
  (Plan 5), web UI (Plan 6), Wails (Plan 7).
- **SDK risk:** the adapter (Task 3) is the only SDK-coupled code and the only place to adapt to
  real signatures; the domain logic (Tasks 1–2) is SDK-free and fully unit-tested regardless.
- **Type consistency:** `mcpsvc.New(agents, upstreams, policy, access)`; result types
  `UpstreamInfo/AccessResult/Identity` consumed by the adapter; `daemon.Config.MCPListen`.

## ADR + module docs (finalize)

ADR-0003 (the implementer writes it): the MCP control-plane design — session=agent identity
(in-memory session→{agentID,token} map; reconnect = new agent record, a known limitation; a
future "claim existing token" handshake is the planned improvement), the SDK-free `mcpsvc` /
thin `mcp` adapter split, access-requests as an intent log with rule-derived access, separate
MCP listener port. Module docs: `access.md`, `mcpsvc.md`, `mcp.md` (new); update `daemon.md`,
`store.md`.
