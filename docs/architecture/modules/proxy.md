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

## Audit (optional)

When `Deps.Audit != nil` the handler records each request to the audit journal (see `audit.md`
and ADR-0004). The inbound request body is wrapped with a capped capture before forwarding; the
upstream response body is wrapped in `ModifyResponse`, and the audit row is written from that
capture's `onClose` — after the response has fully streamed to the agent. The entry carries the
agent/upstream id+name, method, upstream-relative path, query, status, duration, req/resp sizes,
the decision + matched rule id, the masked request headers, and both captured bodies. Upstream
transport failures are recorded via the `ErrorHandler` as a `502`. The early policy outcomes are
recorded inline before returning: `deny → 403`, approval-denied `→ 403`, rate-limited `→ 429`. The
pre-policy guards (`401`/`404`/`503`) are not recorded. When `Audit == nil` the proxy behaves
exactly as in Plans 1–3 (no capture, no recording).

## Public API

- `Deps struct { Agents *agent.Registry; Upstreams *upstream.Registry; Policy *policy.Registry; Limiter *policy.Limiter; Approvals *approval.Queue; AuthManager *authn.Manager; Vault *secret.Vault; Audit *audit.Recorder; Logger *slog.Logger }` (`Audit` optional/nil-safe).
- `New(d Deps) http.Handler` — builds the data-plane handler (defaults `Logger` to `slog.Default()`).
