# module: internal/authn

Injects upstream credentials into proxied requests. `For` is the pluggable seam for future
auth schemes (OIDC, mTLS, SigV4, HMAC) — Plan 2 adds cases here without touching callers.
Plan 1 supports `none`/`""`, `static` (a header name + value), and `basic`.

## Public API

- `Authenticator interface { Apply(req *http.Request) error }`
- `For(cfg upstream.AuthConfig) (Authenticator, error)` — factory; `ErrUnsupported` for unknown types.
- Error: `ErrUnsupported`.
