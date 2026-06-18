# Operation-Access — Plan H2 (enriched MCP + approval entry points) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Let an agent request access by declaring the **operation it needs** (host, method,
path/query template, typed variables, values, purpose) over MCP, and let the operator approve it —
creating or extending an operation rule — plus lazily register the host and attach its credential.

**Architecture:** Extend the SDK-free `mcpsvc` (+ thin `mcp` adapter) with `request_host_access` and
an enriched `request_access`; `list_upstreams` lists hosts; both feed the existing blocking approval
queue. Approving a host lazily creates the host upstream and lets the operator attach a credential;
approving an operation creates/extends the H1 operation rule. The data-plane new-value approval from
H1 is the second entry point — H2 only adds the rich MCP one.

**Tech Stack:** Go 1.26; the official go-sdk MCP (v1.6.1, already used); reuses H1 `policy`/`optemplate`
+ the `approval` queue + `upstream` registry + vault. No new deps.

## Global Constraints

Same as H1 (module path `github.com/Sipaha/outwall`, no citeck, CGO-free server, no panics in lib
code, no Co-Authored-By/amend, no new deps, TDD, alpha schema-reset OK, author `Sipaha <sipahabk@gmail.com>`).
Builds on H1 (`internal/optemplate`, operation rules, `Registry.AddAllowedValue`). k8s untouched.

---

### Task 1: lazy host upstream + credential attach on host approval

**Files:** Modify `internal/upstream/registry.go` (lazy `GetOrCreateByHost`), `internal/mcpsvc/…`,
`internal/daemon/admin.go` (host approval resolve attaches the credential). Test in those packages.

**Interfaces:** `upstream.Registry.GetOrCreateByHost(host string) (*Upstream, bool, error)` — returns
the host upstream, creating a credential-less one if absent (BaseURL `https://<host>`), and whether it
was created. Host approval resolve sets the upstream's auth (the operator-entered token) via the
existing encrypt path.

- [ ] **Step 1:** Failing test: `GetOrCreateByHost("gitlab.example")` creates it once (idempotent
  second call returns the same, created=false); approving a host access request attaches a token that
  round-trips (decrypted) and is masked in any listing.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement lazy create + credential attach.
- [ ] **Step 4:** Run `-race` → PASS.
- [ ] **Step 5:** Commit `feat(upstream): lazy host upstream + credential attach on host approval`.

---

### Task 2: MCP `request_host_access` + enriched `request_access`

**Files:** Modify `internal/mcpsvc/service.go` (+ the `mcp` adapter), the access intent log. Test:
`internal/mcpsvc/service_test.go`.

**Interfaces (MCP tools):**
- `request_host_access(host, purpose) -> {status: granted|pending|denied}` — enqueues a host approval.
- `request_access(host, method, path_template, query_template, variables, values, purpose) ->
  {status}` — `variables` = `[{name,type}]` (`text|date`); `values` = `{name: value}`. Enqueues an
  operation approval carrying the parsed template (validated via `optemplate.Parse`; a bad template →
  immediate error, not a pending) + the requested values + purpose. `pending` → the agent polls
  `get_access`; the MCP call does not block.
- `list_upstreams` returns the hosts the agent has access to (+ status); `get_access(host)` returns a
  short memo (granted operations/values). `whoami` unchanged.

- [ ] **Step 1:** Failing test: an agent session calls `request_access` with a valid template +
  `project_path=infra/helm` + purpose; assert a pending operation approval was enqueued with the
  template key, the value, and the purpose; a malformed template → a tool error (no pending).
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement the two tools + list/get; validate templates with `optemplate.Parse`.
- [ ] **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit `feat(mcp): request_host_access + enriched request_access (typed operations)`.

---

### Task 3: approval resolve → create/extend the operation rule (+ trust-any)

**Files:** Modify `internal/daemon/admin.go` (operation-approval resolve) + `internal/approval` if the
Pending needs the operation payload. Test: `internal/daemon/admin_test.go`.

**Behavior:** approving an **operation** approval creates the H1 operation rule for `Template.Key()`
if absent (outcome `allow`), then adds the requested values to each text variable's set (or sets the
variable to `any` when the operator chose "trust any value"); `date` vars are `any`. Approving on an
**existing** template extends its sets (reuses `Registry.AddAllowedValue`). Deny → no rule change.
The `trust_any` flag is per-variable in the resolve request.

- [ ] **Step 1:** Failing test: resolve(approve) an operation approval → an operation rule exists with
  the value in `project_path`'s set; a second approve for a new value on the same template extends the
  set (one rule, two values); resolve with `trust_any:[project_path]` → that var's policy is `any`;
  deny → no rule.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement the resolve→rule create/extend + trust-any.
- [ ] **Step 4:** Run `-race` → PASS.
- [ ] **Step 5:** Commit `feat(daemon): operation approval creates/extends the operation rule`.

---

### Task 4: ADR + docs + full gate

**Files:** `docs/architecture/decisions/0015-operation-access-mcp-approval.md` (accepted, date when
implemented); update `mcpsvc.md`, `upstream.md`, `approval.md`, `policy.md`. **Don't** edit INDEX /
current-phase.

- [ ] **Step 1:** ADR-0015 + module docs.
- [ ] **Step 2:** Full gate: `make fmt && make vet && go test ./... -race` (file+grep) `&& make build`.
- [ ] **Step 3:** Commit `docs(opaccess): ADR-0015 + module docs for MCP + approval`.

## Self-Review

Covers spec §3 (two channels, rich MCP entry point), §4 host/operation cards' backing actions, the
tier-1/tier-2 two-step flow, and approve-extends-the-set. Reuses H1 engine + the blocking queue. UI is
H3. No new dep; k8s untouched.
