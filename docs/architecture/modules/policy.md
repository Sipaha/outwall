# module: internal/policy

The default-deny rule engine (replaces Plan 1's `grant` allow-list). A `Rule` binds a subject
(a specific agent or "any") + upstream + method + path-glob to an outcome
(`allow`/`deny`/`require-approval`), with a per-rule rate limit. Rules live in the `rules` table.

`Decide` applies precedence: **agent-specific rules outrank any-subject rules outrank
default-deny**; within the first non-empty tier, **most-restrictive wins** (deny >
require-approval > allow). Path globs match the upstream-relative path: `*` within one segment,
`**` across segments (cached compiled regexps). The `Limiter` is a separate in-memory
fixed-window-per-minute counter the proxy consults when a matched rule sets a rate limit.

**K1 (k8s clusters).** A `Rule` also carries `Namespace`/`Resource`/`Verb` (globs) for k8s
clusters, stored in `rules.k8s_namespace`/`k8s_resource`/`k8s_verb`. When `Input.Kind=="k8s"`,
the per-rule match predicate is `verbMatches && nsMatches && resourceMatches` **instead of**
method+path-glob — the tier/precedence resolution is unchanged. `resourceMatches` compares
against `resource` and, when a subresource is present, also `resource/subresource` (supports
`*` and `resource/*`). **Namespace-safety:** an empty request namespace (cluster-scoped /
all-namespaces) matches **only** a rule whose namespace is `*` — never a concrete-namespace
rule (see ADR-0008).

## Public API

- Outcome consts: `Allow = "allow"`, `Deny = "deny"`, `RequireApproval = "require-approval"`; `ValidOutcome(o string) bool`.
- `Rule struct { ID, SubjectAgentID, UpstreamID, Method, PathGlob, Outcome string; RateLimitPerMin int; CreatedAt time.Time; Namespace, Resource, Verb string }` (`SubjectAgentID=""` = any agent; `Method=""`/`"*"` = any method; `Namespace`/`Resource`/`Verb` set on k8s rules only).
- `MatchGlob(pattern, path string) bool`.
- `NewRegistry(s *store.Store) *Registry`
- `(*Registry).Create(in Rule) (*Rule, error)` — assigns ID + CreatedAt; validates outcome and `RateLimitPerMin >= 0`; defaults `PathGlob` to `/**`.
- `(*Registry).List() ([]*Rule, error)`, `(*Registry).Delete(id string) error`, `(*Registry).ForUpstream(upstreamID string) ([]*Rule, error)`.
- `Input struct { AgentID, UpstreamID, Method, Path string; Kind, Namespace, Resource, Subresource, Verb string }` (set `Kind="k8s"` + the tuple for cluster requests), `Decision struct { Outcome string; Rule *Rule }`.
- `(*Registry).Decide(in Input) (Decision, error)` — `Outcome=Deny, Rule=nil` on default-deny.
- `NewLimiter() *Limiter`, `(*Limiter).Allow(key string, limitPerMin int, now time.Time) bool` (`limitPerMin<=0` ⇒ always true).
