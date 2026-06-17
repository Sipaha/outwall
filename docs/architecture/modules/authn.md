# module: internal/authn

Injects upstream credentials into proxied requests. `For` is the pluggable seam for auth
schemes. Plan 1 supports `none`/`""`, `static` (header name + value), and `basic`; Plan 2 adds
`oidc-client-credentials` (fetches a bearer token via the client-credentials grant and caches it
until ~30 s before `expires_in`).

`For` returns a fresh, stateless instance per call, so cross-request token caching is done by
`Manager`: it holds one authenticator per upstream ID, keyed by an auth-config fingerprint, and
rebuilds when the config changes. The proxy uses `Manager.Authenticator`, not `For`.

## Public API

- `Authenticator interface { Apply(req *http.Request) error }`
- `For(cfg upstream.AuthConfig) (Authenticator, error)` — factory; `ErrUnsupported` for unknown types.
- `NewManager(hc *http.Client) *Manager` (nil ⇒ `http.DefaultClient`).
- `(*Manager).Authenticator(up *upstream.Upstream) (Authenticator, error)` — cached per upstream ID.
- Error: `ErrUnsupported`.
