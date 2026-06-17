# module: internal/upstream

The registry of named external APIs and their auth config. The `AuthConfig` is
JSON-marshaled and encrypted via the vault before being stored in `auth_config`; it is
decrypted on read (so the vault must be unlocked to read upstreams).

## Public API

- `AuthConfig struct { Type, Header, Token, Username, Password, TokenURL, ClientID, ClientSecret, Scope string }` — superset covering none/static/basic/oidc-client-credentials.
- `Upstream struct { ID, Name, BaseURL, AuthType string; Auth AuthConfig; CreatedAt time.Time }`
- `NewRegistry(s *store.Store, v *secret.Vault) *Registry`
- `(*Registry).Create(name, baseURL string, auth AuthConfig) (*Upstream, error)` — encrypts auth before storing.
- `(*Registry).GetByName(name string) (*Upstream, error)` — `ErrNotFound` if absent.
- `(*Registry).List() ([]*Upstream, error)` — decrypts each (vault must be unlocked).
- Error: `ErrNotFound`.
