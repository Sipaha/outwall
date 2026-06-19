# module: internal/upstream

The registry of named external APIs and their auth config. The `AuthConfig` is
JSON-marshaled and encrypted via the vault before being stored in `auth_config`; it is
decrypted on read (so the vault must be unlocked to read upstreams).

**K1 (k8s clusters).** An `Upstream` carries a `Kind` (`"http"` default | `"k8s"`, stored in
the `upstreams.kind` column). A k8s cluster's `BaseURL` is the API-server URL and its
`AuthConfig` adds the cluster connection fields: `CABundle` (PEM), `K8sAuth`
(`token|client-cert|exec`), `ClientCert`/`ClientKey`, and `ExecCommand`/`ExecArgs`/`ExecEnv`
(`Token` is reused for `K8sAuth=="token"`). All encrypted at rest like any auth config.

**K4 (insecure clusters).** `AuthConfig` adds `K8sInsecureSkipVerify bool` — set only from an
explicit `insecure-skip-tls-verify:true` in the operator's own kubeconfig (the CA wins when both
are present). `authn` honors it in the cluster transport; the Clusters UI badges it. New JSON
field inside the already-encrypted blob — no migration (defaults false on old rows). See ADR-0011.

**H2 (lazy host = upstream).** A host is registered lazily the first time an agent requests it.
`GetOrCreateByHost(host)` returns the host's http upstream, creating a credential-less one
(`Name = host`, `BaseURL = "https://"+host`, auth `none`) if absent; idempotent. At host-approval
time the operator attaches the credential via `SetAuth(id, AuthConfig)`, which re-encrypts the
blob (so the token is masked at rest exactly like `Create`). See ADR-0015.

## Public API

- `KindHTTP = "http"`, `KindK8s = "k8s"`.
- `AuthConfig struct { Type, Header, Token, Username, Password, TokenURL, ClientID, ClientSecret, Scope string; AWSAccessKeyID, AWSSecretAccessKey, AWSRegion, AWSService string (sigv4); HMACSecret, HMACHeader, HMACAlgo string (hmac); CABundle, K8sAuth, ClientCert, ClientKey, ExecCommand string (ClientCert/ClientKey/CABundle also back http mtls — ADR-0019); ExecArgs []string; ExecEnv map[string]string; K8sInsecureSkipVerify bool }`.
- `Upstream struct { ID, Name, BaseURL, Kind, AuthType string; Auth AuthConfig; CreatedAt time.Time }`
- `NewRegistry(s *store.Store, v *secret.Vault) *Registry`
- `(*Registry).Create(name, baseURL string, auth AuthConfig) (*Upstream, error)` — http kind; delegates to `CreateKind`.
- `(*Registry).CreateKind(name, baseURL, kind string, auth AuthConfig) (*Upstream, error)` — encrypts auth before storing.
- `(*Registry).GetOrCreateByHost(host string) (*Upstream, bool, error)` — lazy, idempotent host upstream; bool reports creation.
- `(*Registry).SetAuth(id string, auth AuthConfig) error` — re-encrypts + attaches the credential by ID; `ErrNotFound` if absent.
- `(*Registry).GetByName(name string) (*Upstream, error)` — `ErrNotFound` if absent.
- `(*Registry).DeleteByName(name string) error` — `ErrNotFound` if absent.
- `(*Registry).List() ([]*Upstream, error)` — decrypts each (vault must be unlocked).
- Error: `ErrNotFound`.
