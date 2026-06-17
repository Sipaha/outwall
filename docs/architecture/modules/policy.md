# module: internal/policy

The default-deny rule engine (replaces Plan 1's `grant` allow-list). A `Rule` binds a subject
(a specific agent or "any") + upstream + method + path-glob to an outcome
(`allow`/`deny`/`require-approval`), with a per-rule rate limit. Rules live in the `rules` table.

`Decide` applies precedence: **agent-specific rules outrank any-subject rules outrank
default-deny**; within the first non-empty tier, **most-restrictive wins** (deny >
require-approval > allow). Path globs match the upstream-relative path: `*` within one segment,
`**` across segments (cached compiled regexps). The `Limiter` is a separate in-memory
fixed-window-per-minute counter the proxy consults when a matched rule sets a rate limit.

## Public API

- Outcome consts: `Allow = "allow"`, `Deny = "deny"`, `RequireApproval = "require-approval"`; `ValidOutcome(o string) bool`.
- `Rule struct { ID, SubjectAgentID, UpstreamID, Method, PathGlob, Outcome string; RateLimitPerMin int; CreatedAt time.Time }` (`SubjectAgentID=""` = any agent; `Method=""`/`"*"` = any method).
- `MatchGlob(pattern, path string) bool`.
- `NewRegistry(s *store.Store) *Registry`
- `(*Registry).Create(in Rule) (*Rule, error)` — assigns ID + CreatedAt; validates outcome and `RateLimitPerMin >= 0`; defaults `PathGlob` to `/**`.
- `(*Registry).List() ([]*Rule, error)`, `(*Registry).Delete(id string) error`, `(*Registry).ForUpstream(upstreamID string) ([]*Rule, error)`.
- `Input struct { AgentID, UpstreamID, Method, Path string }`, `Decision struct { Outcome string; Rule *Rule }`.
- `(*Registry).Decide(in Input) (Decision, error)` — `Outcome=Deny, Rule=nil` on default-deny.
- `NewLimiter() *Limiter`, `(*Limiter).Allow(key string, limitPerMin int, now time.Time) bool` (`limitPerMin<=0` ⇒ always true).
