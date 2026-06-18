# ADR-0014: Operation-access engine (typed-variable operation templates)

- **Status:** accepted
- **Date:** 2026-06-18

## Context

Phase-1 gated HTTP egress with `(method, path-glob)` rules: the operator hand-authored a glob
per path an agent might hit. A glob (`/**`, `/projects/*/pipelines`) cannot express *which
concrete values* an agent is allowed to use, and it silently over-captures (`/**` matches
everything below a prefix). The operation-access design
(`docs/superpowers/specs/2026-06-18-outwall-operation-access-design.md`) replaces this with an
**operation-template + typed-variable** model: the operator approves a request *shape* once and
controls the variable *values* over time, while outwall enforces by **parsing the real request**
— never by trusting what the agent declares.

There is no released data, so the HTTP path-glob rule type is removed outright (no migration).
The k8s plane keeps its own `(namespace, resource, verb)` tuple untouched.

## Decision

**`internal/optemplate` (new pure package).** `Parse(method, pathTemplate, queryTemplate)` builds
a `Template` of segment-bounded `{name:type}` placeholders (`text`, `date`). `Match(method, path,
query)` checks structure and returns the extracted variable values:

- Each placeholder binds **exactly one** path segment (split on `/`); segment counts must be
  equal — no prefix/suffix slack, so a template never over-captures. A segment is decoded
  per-segment so a GitLab `%2F` inside one segment is preserved as the value.
- A declared query param must be present (and, for a literal, equal). An **undeclared,
  non-exempt** query param makes the request **not match** (the scope-bearing surface is covered
  by the approval). A short `ExemptQueryParams` allow-list (`page`, `per_page`, `pagination`)
  is scope-neutral and tolerated.
- A `date` placeholder additionally requires the extracted value to parse as a date (`IsDate`),
  so a scope-bearing value cannot ride a `date` slot.

**`policy.Rule` (HTTP variant) = operation rule.** The `rules` schema drops `method`/`path_glob`
and adds `op_method`, `op_path_template`, `op_query_template` (JSON), `op_value_policies` (JSON:
`varName → {type, mode:"set"|"any", values[]}`). `k8s_*` columns are unchanged.
`Registry.AddAllowedValue(ruleID, var, value)` extends a text variable's allowed-set
(idempotent).

**`policy.Decide` (HTTP branch).** `Input` gains `Query url.Values`; `Decision` gains
`Vars map[string]string` and `NewValues []VarValue`. For each http rule the engine parses (caches
by rule ID) and `Match`es the request; on the first structural match it gates each extracted text
value against its policy (`any`/`set`; `date` always allowed — Match already type-validated). All
allowed → the rule's outcome; a new text value lifts an allow/approval rule to **require-approval**
with the `NewValues` pairs; no template match → **deny** (default-deny). The existing agent-tier ›
any-tier › default-deny precedence and most-restrictive-within-tier resolution are reused.

**Proxy enforcement.** The HTTP path builds `policy.Input{…, Path: <escaped rel-path>,
Query: r.URL.Query()}` (escaped so `%2F` stays within a segment). On `require-approval` with
`NewValues`, the `approval.Pending` carries the rule ID + the `(var, value)` pairs + the matched
template; on approve the proxy calls `AddAllowedValue` for each pair and the request proceeds; on
deny → 403. The upstream is resolved by the first path segment as the host name (lazy host
creation is H2). Audit records the matched template (`operation`) and the extracted `vars`.

## Alternatives considered

- **Keep path-glob, add a value allow-list beside it.** Rejected: a glob still over-captures and
  cannot bind a value to a named variable; two overlapping mechanisms is worse than one.
- **Regex templates.** Rejected: regex over-captures across segments by default and is hard to
  render safely in an approval card; segment-bounded placeholders mirror the k8s parser's
  discipline and are auditable.
- **Trust agent-declared variable values (from the MCP request).** Rejected outright — the
  central guarantee is *parse the real request*; an agent must not be able to widen scope by
  mis-declaring.

## Consequences

- The operator approves a shape once and grows its value-sets; a new value is a one-click
  data-plane approval that **extends the same rule** rather than spawning a new one.
- Undeclared query params are denied by default — safer, at the cost of an exemption list for
  pagination (audited, not silently dropped).
- The `rules` and `audit_log` schemas changed; existing dev DBs must be reset (alpha, no compat).
- H2 adds the enriched MCP entry points (`request_host_access`/`request_access`) and lazy host
  creation; H3 adds the rich approval cards + Operations screen. H1 ships a minimal rule
  CLI/UI/admin surface over the new fields (method + path-template + value lines), not the rich
  per-variable editor.
- The template parse is cached by rule ID; within H1 a rule's template is immutable (only its
  value-sets grow), so the cache never goes stale. A future "edit a rule's template in place"
  would have to invalidate that cache by ID.
