# module: internal/proxy

The data plane: a localhost reverse proxy for `<METHOD> /<upstream>/<rest...>`. It halts when
the vault is locked (503), authenticates the calling agent's bearer token (401), resolves the
upstream by name (404), then evaluates the policy engine on the upstream-relative path
(`/<rest>`):

- `deny` → 403 `access denied`.
- `require-approval` → blocks on `approval.Submit`; approved ⇒ continue, denied/timeout ⇒ 403, ctx-canceled ⇒ 504.
- `allow` → continue.

If the matched rule sets a rate limit, the in-memory `policy.Limiter` is consulted (keyed by
`agentID|ruleID`); over the limit ⇒ 429. It then strips the agent's `Authorization`, applies the
upstream authenticator obtained from `authn.Manager` (so OIDC tokens are cached across requests),
and forwards via `httputil.ReverseProxy` (`Host` rewritten to the upstream, query preserved).

## Public API

- `Deps struct { Agents *agent.Registry; Upstreams *upstream.Registry; Policy *policy.Registry; Limiter *policy.Limiter; Approvals *approval.Queue; AuthManager *authn.Manager; Vault *secret.Vault; Logger *slog.Logger }`
- `New(d Deps) http.Handler` — builds the data-plane handler (defaults `Logger` to `slog.Default()`).
