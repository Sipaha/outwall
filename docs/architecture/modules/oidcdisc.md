# module: internal/oidcdisc

Generic OpenID Connect **discovery** client (ADR-0030): given an issuer (or full discovery) URL it
fetches `{issuer}/.well-known/openid-configuration` and returns the endpoints outwall needs, so the
operator can auto-fill the OIDC host form instead of typing them. No provider-specific assumptions.

- `DiscoveryURL(raw) (string, error)` — normalises the input: append `/.well-known/openid-configuration`
  to an issuer URL (trailing slash trimmed); use a full discovery URL as-is; require absolute http(s).
- `Discover(ctx, hc *http.Client, raw) (Config, error)` — GET the document (caller supplies the
  client; the daemon uses a 10s-timeout client), capped at 1 MiB, parsed into `Config`. Errors if the
  document lacks `authorization_endpoint` / `token_endpoint`.
- `Config` — `Issuer`, `AuthorizationEndpoint`, `TokenEndpoint`, `EndSessionEndpoint`,
  `ScopesSupported`.

Wired via the admin endpoint `POST /oidc/discover {url}` (operator-only; reads no secrets, no vault
unlock needed). The Add-host UI's **Discover** button calls it and fills Authorization/Token URL +
Scope.
