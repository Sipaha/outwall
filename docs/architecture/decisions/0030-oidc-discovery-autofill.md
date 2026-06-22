# ADR-0030: OIDC discovery — auto-fill the host form from the well-known document

- **Status:** accepted
- **Date:** 2026-06-22

## Context

Configuring an `oidc-authorization-code` host required the operator to type the Authorization URL,
Token URL and scopes by hand. Operators usually only know the **issuer** (or a realm URL); the exact
endpoint paths are provider-specific and easy to get wrong. OpenID Connect already standardises a
discovery document at `{issuer}/.well-known/openid-configuration` that lists every endpoint.

## Decision

Add generic OIDC discovery — no provider-specific assumptions.

- **`internal/oidcdisc`** (new, leaf package): `DiscoveryURL(raw)` normalises an operator-entered URL
  (issuer → append `/.well-known/openid-configuration`; a full discovery URL is used as-is; must be
  absolute http(s)). `Discover(ctx, hc, raw)` GETs the document (10s timeout, 1 MiB cap), parses it,
  and returns the subset outwall needs (`issuer`, `authorization_endpoint`, `token_endpoint`,
  `end_session_endpoint`, `scopes_supported`), erroring if the authorization/token endpoints are
  absent.
- **Admin endpoint** `POST /oidc/discover` `{url}` → the discovered endpoints. It is operator-only
  (admin API) and reads no secrets, so it needs no vault unlock.
- **UI**: the Add-host / set-credential form (OIDC authorization-code) gains an **Issuer / discovery
  URL** field + a **Discover** button that calls the endpoint and fills Authorization URL, Token URL
  and a sensible default Scope. The operator can still edit any field afterwards.

## Alternatives considered

- **Discover at request time inside the auth manager** (resolve endpoints lazily from the issuer when
  proxying). Rejected — discovery is a config-time convenience; resolving per request adds latency and
  a network dependency on the hot path. We persist the concrete endpoints, as today.
- **Provider-specific templates (e.g. build Keycloak paths from realm).** Rejected — couples outwall
  to a vendor; the standard well-known document already returns the exact endpoints for any compliant
  provider.
- **Discover entirely client-side (browser fetches the well-known doc).** Rejected — the browser may
  not reach an internal IdP, and CORS on the discovery endpoint is not guaranteed; the daemon fetch is
  reliable and consistent with how outwall reaches upstreams.

## Consequences

- Operators paste an issuer URL and click Discover; the OIDC endpoints fill in. No need to know
  provider-specific paths.
- New leaf package `internal/oidcdisc` (unit-tested) and a thin admin handler (`TestOIDCDiscoverEndpoint`).
- The endpoint performs a server-side GET of an operator-supplied URL — acceptable as an operator-only
  admin action configuring their own IdP; bounded by timeout and response cap.
