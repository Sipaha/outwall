# ADR-0033: Data-plane agent token via `outwall_token` cookie (browser use)

- **Status:** accepted
- **Date:** 2026-06-22

## Context

Agents authenticate to the data plane with their outwall token in the `Authorization: Bearer` header.
That works for programmatic HTTP clients, but not for a **real browser**: when an agent drives
Playwright to open a site that is behind OIDC (the operator logs in once via the browser flow,
ADR-0021, and outwall injects the token), the browser navigates to the data-plane URL
(`https://127.0.0.1:<port>/<host>/...`) and there is no clean, automatic way to attach a bearer
header to every top-level navigation and sub-resource request. Browsers do, however, send **cookies**
automatically on every request to an origin.

## Decision

Accept the agent's outwall token from an `outwall_token` **cookie** as an alternative to the
`Authorization: Bearer` header on the data plane.

- `proxy.agentToken` reads the token from `Authorization: Bearer` first, else from the
  `outwall_token` cookie. Authentication and policy are otherwise unchanged.
- Before forwarding upstream, the proxy **strips** the `outwall_token` cookie (alongside the existing
  `Authorization` strip) so the upstream never sees the agent's outwall token; **other cookies pass
  through** (the upstream's own session cookies are untouched).
- The cookie value is already masked in the audit log (the `Cookie` header is in the masked set).

Usage: the agent sets `outwall_token=<its token>` for the data-plane origin in its Playwright
browser context, then navigates to `https://127.0.0.1:<port>/<host>/...`. outwall authenticates the
agent from the cookie, enforces policy, injects the upstream credential (e.g. the operator's OIDC
access token), and forwards — so the agent's browser renders an authenticated page without ever
seeing the upstream credential.

## Alternatives considered

- **`context.setExtraHTTPHeaders` to add the bearer header in Playwright.** Works for some requests
  but is awkward and easy to get wrong across navigations/sub-resources; a cookie is the browser-
  native, automatic mechanism and needs no per-request wiring.
- **A dedicated browser-auth endpoint that sets the cookie via Set-Cookie.** Rejected as unnecessary
  ceremony — the agent already has its token (`whoami`) and can set the cookie directly in its
  browser context.
- **Rewrite upstream Set-Cookie domains to the loopback origin.** Out of scope here — OIDC resource
  servers typically accept the injected bearer per request, so the page loads without depending on
  the upstream's own cookies. Cookie-domain rewriting can be revisited if a specific site needs it.

## Consequences

- A browser-driven agent (Playwright) can browse upstreams — including OIDC-protected sites the
  operator logged into — through the data plane by carrying `outwall_token` as a cookie; the token is
  stripped before forwarding and masked in audit. Covered by `TestProxyCookieTokenAuthAndStrip`.
- The header path is unchanged; the cookie is purely additive.
