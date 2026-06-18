# Operation-Access — Plan H1 (template engine + proxy enforcement + host model) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the HTTP path-glob policy with an **operation-template + typed-variable** engine:
match each real request against approved templates, extract variable values from the URL, and gate
on per-variable value policies — enforced by parsing the request, never by trusting the agent.

**Architecture:** A new pure `internal/optemplate` package parses a `(method, path-template,
query-template)` into segment-bounded typed placeholders and `Match`es a real request, returning the
extracted variable values. `policy.Rule` becomes an **operation rule** (template + per-variable value
policy: `text` set/`any`, `date` `any`). `policy.Decide`'s HTTP branch uses `optemplate.Match` +
value gating and returns the outcome plus the matched rule and any not-yet-allowed `(variable,value)`.
The proxy's single decision seam consumes it; an unknown template → deny, a new `text` value →
require-approval that, on approve, **extends** the rule's value-set. The k8s plane is untouched.

**Tech Stack:** Go 1.26, `net/url`, `stretchr/testify`, `modernc.org/sqlite`. No new deps.

## Global Constraints

- Module path **`github.com/Sipaha/outwall`** — exact. **No `citeck`** in code/commits.
- **No CGO** in the server binary (`CGO_ENABLED=0`); SQLite via `modernc.org/sqlite`.
- **No panics / `log.Fatal`** in library code — wrapped errors (`%w`). Panics only in `main`/tests.
- No new deps; don't bump existing. **No `Co-Authored-By`** / no amend. One commit per task.
  Author `Sipaha <sipahabk@gmail.com>`.
- **No released data / no migration:** redefine the `rules` schema for operation rules; the old HTTP
  `method`/`path_glob` columns are **removed**, not kept. (Existing dev DBs get reset — note it.)
- `gofmt`/`go vet` clean, `log/slog`, `stretchr/testify`, TDD.
- **k8s rules are untouched** (their `k8s_*` columns + `Decide` k8s branch stay exactly as-is).

---

### Task 1: `internal/optemplate` — parse + match + extract (pure core)

**Files:** Create `internal/optemplate/optemplate.go`, `internal/optemplate/optemplate_test.go`.

**Interfaces (Produces):**
```go
package optemplate
type VarType string
const ( Text VarType = "text"; Date VarType = "date" )
// Variable is a typed placeholder declared in a template.
type Variable struct { Name string; Type VarType }
// Template is a parsed operation shape: a method + a path of literal/placeholder segments + a set of
// query params each a literal or a placeholder. Query params NOT named here are scope-bearing and
// cause a request to NOT match (except ExemptQueryParams).
type Template struct { /* unexported parsed form */ }
// ExemptQueryParams are scope-neutral params allowed even when undeclared (pagination).
var ExemptQueryParams = map[string]struct{}{"page": {}, "per_page": {}, "pagination": {}}
// Parse builds a Template. pathTemplate uses {name:type} placeholders, each binding exactly ONE path
// segment (no '/'); a literal segment matches itself. queryTemplate maps a param name to either a
// literal value or a "{name:type}" placeholder. Returns an error on a malformed placeholder, an
// unknown type, or a duplicate variable name.
func Parse(method, pathTemplate string, queryTemplate map[string]string) (Template, error)
// Vars returns the template's declared variables (path then query, declaration order).
func (t Template) Vars() []Variable
// Key is a stable identity string for the template (method + normalized path-template +
// sorted query-template) — two requests with the same shape share one rule.
func (t Template) Key() string
// Match reports whether method+path+query fit the template's STRUCTURE and, if so, returns the
// extracted variable values (name -> raw decoded value). It does NOT check value policies (that is
// policy's job). Match fails if: method differs; segment count differs; a literal segment differs; a
// declared query param is absent or (for a literal) differs; or an undeclared, non-exempt query
// param is present. A Date-typed placeholder additionally requires the value to parse as a date.
func (t Template) Match(method, path string, query url.Values) (vars map[string]string, ok bool)
// IsDate reports whether s parses as a supported date/datetime (RFC3339, "2006-01-02", and the
// common date-time forms). Exposed for policy + tests.
func IsDate(s string) bool
```

**Behavior notes:** path is split on `/` (decoded per-segment so GitLab `%2F` inside one segment is
preserved as the segment value); a `{p:text}` captures one segment verbatim; segment counts must be
equal (no prefix/suffix slack — this is the "no over-capture" guarantee). A `Date` placeholder whose
value fails `IsDate` → `ok=false` (the request is treated as not matching that template).

- [ ] **Step 1:** Write a table-driven `TestMatch` covering:
  `Parse("GET","/api/v4/projects/{project_path:text}/pipelines", {"updated_after":"{since:date}"})`
  then: a request `GET /api/v4/projects/infra%2Fhelm/pipelines?updated_after=2026-06-01` →
  `ok, {project_path:"infra/helm", since:"2026-06-01"}`; wrong method → `!ok`; extra path segment
  (`/pipelines/9`) → `!ok`; a different literal (`/builds`) → `!ok`; an **undeclared** query param
  (`&secret=1`) → `!ok`; an exempt param (`&page=2`) → still `ok`; a non-date `updated_after=foo` →
  `!ok`. Plus `TestKeyStable` (same shape → equal `Key()`, different shape → different) and
  `TestIsDate` (RFC3339 + `2006-01-02` true; `"infra/helm"` false).
- [ ] **Step 2:** Run `go test ./internal/optemplate/ -v` → FAIL (undefined).
- [ ] **Step 3:** Implement `Parse`/`Match`/`Vars`/`Key`/`IsDate` (pure string/url logic).
- [ ] **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit `feat(optemplate): typed-variable operation template parse + match`.

---

### Task 2: operation rule = template + per-variable value policy (storage + registry)

**Files:** Modify `internal/store/migrate.go` (rules schema), `internal/policy/rule.go`,
`internal/policy/registry.go`. Test: `internal/policy/registry_test.go`.

**Schema (redefine `rules`, no migration):** drop `method` + `path_glob`; add
`op_method TEXT NOT NULL DEFAULT ''`, `op_path_template TEXT NOT NULL DEFAULT ''`,
`op_query_template TEXT NOT NULL DEFAULT '{}'` (JSON map), `op_value_policies TEXT NOT NULL DEFAULT '{}'`
(JSON: varName → `{type:"text|date", mode:"set|any", values:["…"]}`). Keep `k8s_*` columns.

**Interfaces:** `policy.Rule` (http variant) gains:
```go
// HTTP operation rule (Kind == "" / "http"):
OpMethod        string
OpPathTemplate  string
OpQueryTemplate map[string]string
OpValuePolicies map[string]ValuePolicy   // varName -> policy
type ValuePolicy struct { Type string; Mode string; Values []string } // Mode: "set" | "any"
```
Registry methods:
```go
func (r *Registry) Create(in Rule) (*Rule, error)           // marshals OpQueryTemplate/OpValuePolicies to JSON
func (r *Registry) ForUpstream(upstreamID string) ([]*Rule, error)  // unchanged signature; unmarshals JSON
// AddAllowedValue extends a text variable's set on an existing rule (idempotent on a present value);
// the proxy/approval calls it when the operator approves a new value.
func (r *Registry) AddAllowedValue(ruleID, varName, value string) error
```

- [ ] **Step 1:** Failing test: create an http operation Rule with an `OpValuePolicies` of
  `{project_path:{text,set,[a]}, since:{date,any,[]}}`, reload via `ForUpstream`, assert the template
  fields + policies round-trip (JSON columns); `AddAllowedValue(id,"project_path","b")` then reload →
  set is `[a,b]`; adding `a` again is a no-op.
- [ ] **Step 2:** Run `go test ./internal/policy/ -run Registry -v` → FAIL.
- [ ] **Step 3:** Redefine the schema (remove method/path_glob, add op_* JSON columns), update the
  struct, Create/scan JSON marshal-unmarshal, and `AddAllowedValue`.
- [ ] **Step 4:** Run → PASS (k8s rule tests still green).
- [ ] **Step 5:** Commit `feat(policy): operation rules (template + per-variable value policies)`.

---

### Task 3: `policy.Decide` HTTP branch = match template + gate values

**Files:** Modify `internal/policy/decide.go`. Test: `internal/policy/decide_test.go`.

**Interfaces:** `policy.Input` (http) keeps `AgentID, UpstreamID, Method, Path` and gains
`Query url.Values`. `Decision` gains:
```go
type Decision struct {
    Outcome string
    Rule    *Rule
    Vars        map[string]string // extracted variable values (matched http template)
    NewValues   []VarValue        // text (variable,value) pairs not yet in the allowed set
}
type VarValue struct { Var, Value string }
```
HTTP `Decide` logic (when `Input.Kind != "k8s"`): over the agent-tier then any-tier rules for the
upstream, for each http rule build `optemplate.Parse(rule.OpMethod, rule.OpPathTemplate,
rule.OpQueryTemplate)` (cache by `rule.ID`), `Match` the request; on the FIRST structural match:
- compute, per declared text variable, whether the extracted value is in its `set` (or the var is
  `any`); `date` vars are always allowed (Match already type-validated).
- all allowed → `Decision{Outcome: rule.Outcome, Rule: rule, Vars}` (precedence/most-restrictive
  tiering reused as today for the matched rules).
- some text value not allowed → `Decision{Outcome: RequireApproval, Rule: rule, Vars, NewValues:[…]}`.
No template matched → `Decision{Outcome: Deny}` (default-deny). Keep the existing tier resolution
helpers; only the per-rule predicate changes for http.

- [ ] **Step 1:** Failing `TestDecideHTTPOperation`: a rule (template `projects/{project_path:text}/pipelines`,
  `project_path` set `[infra/helm]`, outcome allow). Input matching with `project_path=infra/helm` →
  Allow + `Vars[project_path]=infra/helm`. Input with `project_path=other` → RequireApproval +
  `NewValues=[{project_path,other}]`. Input on a non-matching path (`/builds`) → Deny. A `date` var
  request with a valid date → Allow; with a non-date → Deny (no match). Agent-tier deny outranks
  any-tier allow on the same matched template.
- [ ] **Step 2:** Run `go test ./internal/policy/ -run DecideHTTP -v` → FAIL.
- [ ] **Step 3:** Implement the http branch (template cache + match + value gating + NewValues).
- [ ] **Step 4:** Run `./internal/policy/... -race` (k8s + http) → PASS.
- [ ] **Step 5:** Commit `feat(policy): operation-template HTTP decision (match + value gating)`.

---

### Task 4: proxy enforcement + host resolution + new-value approval extend

**Files:** Modify `internal/proxy/proxy.go`, `internal/approval/queue.go` (Pending carries the
operation context), `internal/daemon/admin.go` (the approval-resolve path calls `AddAllowedValue` on
an approved new-value). Test: `internal/proxy/proxy_test.go` (http operation cases).

**Interfaces (consumes Tasks 1-3):**
- The proxy's http path builds `policy.Input{AgentID, UpstreamID, Method: r.Method, Path: relPath,
  Query: r.URL.Query()}` and calls `Decide`.
- Host resolution: the upstream is looked up by the first path segment as the **host name** (today's
  `GetByName(name)`; the registered upstream's `Name` is the host, e.g. `gitlab.citeck.ru`). No change
  needed beyond treating the name as a host (H2 adds lazy creation; H1 assumes the host upstream
  exists).
- On `RequireApproval` with `NewValues`, `approval.Pending` carries `RuleID`, and the new
  `(variable,value)` list so the UI/CLI can show "new value"; on approve, the resolve handler calls
  `policy.Registry.AddAllowedValue(ruleID, varName, value)` for each, then the request proceeds (the
  existing long-poll unblocks). On deny → 403.
- Audit: record the matched template (`rule.OpPathTemplate`) + extracted `Vars` on the entry
  (extend `audit.Entry` with an optional `Operation`/`Vars` field, or fold into existing fields).

- [ ] **Step 1:** Failing integration test in `proxy_test.go`: a fake upstream; register a host
  upstream `example.test` + an allow operation rule (`/projects/{project_path:text}/pipelines`,
  set `[a]`); a request to `/example.test/projects/a/pipelines` → 200 proxied; a request with
  `project_path=b` → blocks on approval, and after the test approves it (via the queue) the value is
  added and the request returns 200; a request to `/example.test/other` → 403.
- [ ] **Step 2:** Run `go test ./internal/proxy/ -run Operation -v` → FAIL.
- [ ] **Step 3:** Implement the http Input build, the NewValues→Pending plumbing, the resolve→
  AddAllowedValue extend, and audit of the matched template+vars.
- [ ] **Step 4:** Run `./internal/proxy/... -race` (http + k8s) → PASS.
- [ ] **Step 5:** Commit `feat(proxy): operation enforcement + new-value approval extends the set`.

---

### Task 5: remove the old path-glob surface + ADR + docs + gate

**Files:** Remove the now-dead path-glob HTTP rule bits from the rule CLI/admin API that referenced
`method`/`path_glob` (keep k8s + the new op fields; H2/H3 build the rich MCP/UI — H1 just stops
exposing the removed columns). Create `docs/architecture/decisions/0014-operation-access-engine.md`
(per the template, accepted, 2026-06-18); update `docs/architecture/modules/policy.md`, `proxy.md`,
and add `optemplate.md`. **Do NOT** edit `docs/INDEX.md` / `docs/roadmap/current-phase.md`.

- [ ] **Step 1:** Grep for `path_glob` / `PathGlob` / the removed CLI flags and delete/replace the
  dead HTTP-rule surface (a rule CLI that set a path-glob now sets an operation template, or is
  deferred to H2 with a clear `// TODO(H2)`-free removal — prefer removal over a stub). Keep k8s rule
  CLI working.
- [ ] **Step 2:** Write ADR-0014 + module docs (record: template model, parse-from-request
  enforcement, value-set extend, undeclared-query denial + exemption, no-migration schema reset).
- [ ] **Step 3:** Full gate: `make fmt && make vet && go test ./... -race` (capture to file, grep)
  `&& make build`. All green; CGO-free.
- [ ] **Step 4:** Commit `feat(policy): drop path-glob HTTP rules; docs(ADR-0014) operation engine`.

## Self-Review

- Spec §2.2 template → Task 1; §2.3 rule/value-policy → Task 2; §2.4 enforcement → Tasks 3-4;
  §5 types (text/date) → Tasks 1-3; §6 rewrite/remove path-glob → Tasks 2,5; §7 undeclared-query
  denial + date-can't-dodge → Task 1; §8 audit → Task 4. (§3 MCP + §4 UI are H2/H3.)
- Type consistency: `optemplate.Template/Variable/Match/Key/IsDate`, `policy.ValuePolicy`,
  `Decision.Vars/NewValues`, `Registry.AddAllowedValue` are used identically across tasks.
- No new dep; k8s plane untouched; no-migration schema reset noted in the REPORT.
