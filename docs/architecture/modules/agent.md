# module: internal/agent

The registry of agents that connect through outwall. A registered agent gets a bearer
token `owa_<base64url-32B>`, shown once; only the token's SHA-256 hex is persisted. New
agents start in status `new` (default-deny).

## Public API

- `Agent struct { ID, Name, Status string; CreatedAt time.Time }`
- `StatusNew` — the default status constant (`"new"`).
- `NewRegistry(s *store.Store) *Registry`
- `(*Registry).Register(name string) (*Agent, string, error)` — returns the agent and its token.
- `(*Registry).Authenticate(token string) (*Agent, error)` — `ErrUnknownToken` if no match.
- `(*Registry).List() ([]*Agent, error)`
- Error: `ErrUnknownToken`.
