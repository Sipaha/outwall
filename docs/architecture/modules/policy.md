# module: internal/policy

The default-deny rule engine (replaces Plan 1's `grant` allow-list). A `Rule` binds a subject
(a specific agent or "any") + upstream to an outcome (`allow`/`deny`/`require-approval`), with a
per-rule rate limit. Rules live in the `rules` table.

`Decide` applies precedence: **agent-specific rules outrank any-subject rules outrank
default-deny**; within the first non-empty tier, **most-restrictive wins** (deny >
require-approval > allow). The `Limiter` is a separate in-memory fixed-window-per-minute counter
the proxy consults when a matched rule sets a rate limit.

**H1 (operation rules — HTTP).** An HTTP rule is an **operation rule**: a `(op_method,
op_path_template, op_query_template)` parsed by `internal/optemplate` plus a per-variable
**value policy** (`op_value_policies`: `varName → {type, mode:"set"|"any", values[]}`). The old
`(method, path_glob)` HTTP rule type is **removed** (no migration — ADR-0014). When
`Input.Kind != "k8s"`, `Decide` matches the request against each rule's template (cached parse
by rule ID), extracts the variable values, and gates each **text** value against its set
(`any` auto-allows; `date` is always allowed — `optemplate.Match` already type-validated it):

- all values allowed → the rule's outcome, with the extracted `Decision.Vars`.
- a `text` value not in its set → **require-approval** + `Decision.NewValues` (the `(var,value)`
  pairs). On approve the proxy calls `AddAllowedValue` to **extend** the set; the request proceeds.
- no template matched → **deny** (default-deny).

`AddAllowedValue(ruleID, var, value)` extends a text variable's allowed-set (idempotent). The
tier/precedence resolution is unchanged — only the per-rule HTTP predicate changed.

**H2 (operation approval → create/extend).** The MCP operation approval resolve (in
`internal/daemon`, ADR-0015) is the other writer of operation rules: it `Create`s an `allow` rule
for a template the upstream does not yet have (looked up by `optemplate.Template.Key()`), or
extends an existing one. `SetVariableAny(ruleID, var)` flips a text variable to mode `any` (drops
its set) for the operator's "trust any value"; both it and `AddAllowedValue` share an internal
load-mutate-save helper.

**K1 (k8s clusters).** A `Rule` also carries `Namespace`/`Resource`/`Verb` (globs) for k8s
clusters, stored in `rules.k8s_namespace`/`k8s_resource`/`k8s_verb`. When `Input.Kind=="k8s"`,
the per-rule match predicate is `verbMatches && nsMatches && resourceMatches` **instead of**
method+path-glob — the tier/precedence resolution is unchanged. `resourceMatches` compares
against `resource` and, when a subresource is present, also `resource/subresource` (supports
`*` and `resource/*`). **Namespace-safety:** an empty request namespace (cluster-scoped /
all-namespaces) matches **only** a rule whose namespace is `*` — never a concrete-namespace
rule (see ADR-0008).

**K2 (mutating verbs).** The mutating RBAC verbs (create/update/patch/delete/deletecollection)
need no special-casing here: `internal/k8s.Parse` produces them and the generic `verbMatches`
matches them like any verb, so a `require-approval` rule on `verb=patch` resolves through the
unchanged tier/precedence logic. The proxy then parks the request on the approval queue (see
`proxy.md` / ADR-0009). No verb whitelist is enforced at rule-creation time — an unknown verb
simply never matches a real request.

## Public API

- Outcome consts: `Allow = "allow"`, `Deny = "deny"`, `RequireApproval = "require-approval"`; `ValidOutcome(o string) bool`.
- `Rule struct { ID, SubjectAgentID, UpstreamID, Outcome string; RateLimitPerMin int; CreatedAt time.Time; OpMethod, OpPathTemplate string; OpQueryTemplate map[string]string; OpValuePolicies map[string]ValuePolicy; Namespace, Resource, Verb string }` (`SubjectAgentID=""` = any agent; `Op*` set on HTTP rules; `Namespace`/`Resource`/`Verb` set on k8s rules only).
- `ValuePolicy struct { Type, Mode string; Values []string }` (`Type`: `"text"|"date"`; `Mode`: `"set"|"any"`).
- `MatchGlob(pattern, path string) bool` (used by the k8s namespace/resource globs).
- `NewRegistry(s *store.Store) *Registry`
- `(*Registry).Create(in Rule) (*Rule, error)` — assigns ID + CreatedAt; validates outcome and `RateLimitPerMin >= 0`; marshals `OpQueryTemplate`/`OpValuePolicies` to JSON columns.
- `(*Registry).AddAllowedValue(ruleID, varName, value string) error` — extends a text variable's allowed-set (idempotent on a present value).
- `(*Registry).SetVariableAny(ruleID, varName string) error` — flips a text variable to mode `any`, dropping its set (idempotent).
- `(*Registry).List() ([]*Rule, error)`, `(*Registry).Delete(id string) error`, `(*Registry).ForUpstream(upstreamID string) ([]*Rule, error)`.
- `Input struct { AgentID, UpstreamID, Method, Path string; Query url.Values; Kind, Namespace, Resource, Subresource, Verb string }` (HTTP: set `Method`/`Path`/`Query`; k8s: set `Kind="k8s"` + the tuple).
- `VarValue struct { Var, Value string }`, `Decision struct { Outcome string; Rule *Rule; Vars map[string]string; NewValues []VarValue }`.
- `(*Registry).Decide(in Input) (Decision, error)` — `Outcome=Deny, Rule=nil` on default-deny.
- `NewLimiter() *Limiter`, `(*Limiter).Allow(key string, limitPerMin int, now time.Time) bool` (`limitPerMin<=0` ⇒ always true).
