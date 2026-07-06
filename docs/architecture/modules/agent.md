# module: internal/agent

The registry of agents that connect through outwall. A registered agent gets a bearer
token `owa_<base64url-32B>`, shown once; only the token's SHA-256 hex is persisted. New
agents start in status `new` (default-deny).

**Last-activity tracking.** `agents.last_seen_at` (nullable) is best-effort touched by
`Authenticate` after the token resolves — never before, and a touch failure is logged
(slog) and swallowed, never failing authentication. `Authenticate` is on the hot path for
**both** the data-plane proxy and every agent-socket (MCP) call, so this one touch point
captures activity across both. `LastSeenAt` is the zero `time.Time` for an agent that has
never authenticated (NULL column); `GetByID`/`List` parse it the same way.

## Public API

- `Agent struct { ID, Name, Status string; CreatedAt, LastSeenAt time.Time }` (`LastSeenAt` zero
  when never authenticated).
- `StatusNew` — the default status constant (`"new"`).
- `NewRegistry(s *store.Store) *Registry`
- `(*Registry).Register(name string) (*Agent, string, error)` — returns the agent and its token.
- `(*Registry).Authenticate(token string) (*Agent, error)` — `ErrUnknownToken` if no match; touches `last_seen_at` on success (best-effort).
- `(*Registry).GetByID(id string) (*Agent, error)` — `ErrNotFound` if absent.
- `(*Registry).List() ([]*Agent, error)` — newest first (`ORDER BY created_at DESC`).
- `(*Registry).Delete(id string) error` — removes the agent row (callers cascade its policy rules first, see `policy.md`).
- Errors: `ErrUnknownToken`, `ErrNotFound`.
