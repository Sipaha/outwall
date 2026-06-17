# module: internal/proxy

The data plane: a localhost reverse proxy for `<METHOD> /<upstream>/<rest...>`. It halts
when the vault is locked (503), authenticates the calling agent's bearer token (401),
resolves the upstream by name (404), enforces the grant default-deny (403), then strips the
agent's `Authorization`, applies the upstream authenticator, and forwards via
`httputil.ReverseProxy` (`Host` rewritten to the upstream, query preserved).

## Public API

- `Deps struct { Agents *agent.Registry; Upstreams *upstream.Registry; Grants *grant.Registry; Vault *secret.Vault; Logger *slog.Logger }`
- `New(d Deps) http.Handler` — builds the data-plane handler (defaults `Logger` to `slog.Default()`).
