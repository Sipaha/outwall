# module: internal/authn

Injects upstream credentials into proxied requests. `For` is the pluggable seam for auth
schemes. Plan 1 supports `none`/`""`, `static` (header name + value), and `basic`; Plan 2 adds
`oidc-client-credentials` (fetches a bearer token via the client-credentials grant and caches it
until ~30 s before `expires_in`).

`For` returns a fresh, stateless instance per call, so cross-request token caching is done by
`Manager`: it holds one authenticator per upstream ID, keyed by an auth-config fingerprint, and
rebuilds when the config changes. The proxy uses `Manager.Authenticator`, not `For`.

**K1 (k8s clusters).** A header-only `Authenticator` cannot do mTLS or trust a custom CA, so
`Manager` also exposes `Transport` — a per-cluster `http.RoundTripper` whose `tls.Config`
trusts `AuthConfig.CABundle` and (for client-cert auth) presents the client cert. For http
upstreams `Transport` returns `nil` (default transport). The transport is cached alongside the
authenticator (same fingerprint, now including the k8s fields). The header step branches on
`Kind=="k8s"` + `K8sAuth`: `token` ⇒ static bearer, `exec` ⇒ exec-plugin bearer (cached
`ExecCredential` token, bounded 30 s run, operator-only argv/env — see ADR-0008), `client-cert`
⇒ no header (identity rides the transport's cert).

## Public API

- `Authenticator interface { Apply(req *http.Request) error }`
- `For(cfg upstream.AuthConfig) (Authenticator, error)` — factory; `ErrUnsupported` for unknown types.
- `NewManager(hc *http.Client) *Manager` (nil ⇒ `http.DefaultClient`).
- `(*Manager).Authenticator(up *upstream.Upstream) (Authenticator, error)` — cached per upstream ID.
- `(*Manager).Transport(up *upstream.Upstream) (http.RoundTripper, error)` — k8s cluster transport; `nil` for http.
- Error: `ErrUnsupported`.
