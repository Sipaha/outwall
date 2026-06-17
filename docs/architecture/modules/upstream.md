# module: internal/upstream

The registry of named external APIs and their auth config. The `AuthConfig` is
JSON-marshaled and encrypted via the vault before being stored in `auth_config`; it is
decrypted on read (so the vault must be unlocked to read upstreams).

**K1 (k8s clusters).** An `Upstream` carries a `Kind` (`"http"` default | `"k8s"`, stored in
the `upstreams.kind` column). A k8s cluster's `BaseURL` is the API-server URL and its
`AuthConfig` adds the cluster connection fields: `CABundle` (PEM), `K8sAuth`
(`token|client-cert|exec`), `ClientCert`/`ClientKey`, and `ExecCommand`/`ExecArgs`/`ExecEnv`
(`Token` is reused for `K8sAuth=="token"`). All encrypted at rest like any auth config.

## Public API

- `KindHTTP = "http"`, `KindK8s = "k8s"`.
- `AuthConfig struct { Type, Header, Token, Username, Password, TokenURL, ClientID, ClientSecret, Scope string; CABundle, K8sAuth, ClientCert, ClientKey, ExecCommand string; ExecArgs []string; ExecEnv map[string]string }`.
- `Upstream struct { ID, Name, BaseURL, Kind, AuthType string; Auth AuthConfig; CreatedAt time.Time }`
- `NewRegistry(s *store.Store, v *secret.Vault) *Registry`
- `(*Registry).Create(name, baseURL string, auth AuthConfig) (*Upstream, error)` — http kind; delegates to `CreateKind`.
- `(*Registry).CreateKind(name, baseURL, kind string, auth AuthConfig) (*Upstream, error)` — encrypts auth before storing.
- `(*Registry).GetByName(name string) (*Upstream, error)` — `ErrNotFound` if absent.
- `(*Registry).DeleteByName(name string) error` — `ErrNotFound` if absent.
- `(*Registry).List() ([]*Upstream, error)` — decrypts each (vault must be unlocked).
- Error: `ErrNotFound`.
