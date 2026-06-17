# module: internal/grant

The Plan-1 stand-in for the policy engine: a flat allow-list of `(agent, upstream)` pairs
enforcing default-deny. Plan 2 replaces `Allowed` with the rule engine
(method/path/rate-limit/require-approval) behind the same proxy call site.

## Public API

- `NewRegistry(s *store.Store) *Registry`
- `(*Registry).Add(agentID, upstreamID string) error` — idempotent (`INSERT OR IGNORE`).
- `(*Registry).Allowed(agentID, upstreamID string) (bool, error)`.
