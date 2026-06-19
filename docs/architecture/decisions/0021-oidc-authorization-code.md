# ADR-0021: OIDC authorization-code (browser login) upstream auth

- **Status:** accepted
- **Date:** 2026-06-19

## Context

`oidc-client-credentials` (ADR-0002) authenticates outwall *as itself* (a service identity). Many
APIs instead require a **user** identity obtained through an interactive browser login
(authorization-code + PKCE) — GitHub/GitLab OAuth apps, Google, Okta, any standard OIDC IdP. The
operator should sign in once in a browser; outwall then holds the resulting access/refresh tokens and
injects them on the data plane, so the agent calls the plain proxy URL and never sees the token.

## Decision

A new upstream auth type `oidc-authorization-code` built on `golang.org/x/oauth2` (promoted to a
direct dependency), with the login flow coordinated by the daemon.

- **`authn`** — pure helpers `OAuthConfig`, `AuthCodeURL(cfg, state, verifier)` (offline access +
  S256 PKCE challenge), `ExchangeCode(ctx, cfg, code, verifier)`, `GenerateVerifier`. The
  `oidcAuthCode` authenticator wraps an `oauth2.ReuseTokenSource` seeded from the stored tokens: it
  refreshes transparently and, on a refresh, calls an optional `persist(Tokens)` hook so a rotated
  refresh token is written back. `Manager.build` constructs it (erroring if not yet logged in) and
  wires the per-upstream persister via the new `Manager.SetOAuthPersister`.
- **`upstream.AuthConfig`** — new fields `AuthURL`, `RedirectURL` (config) and `AccessToken`,
  `RefreshToken`, `TokenExpiry` (runtime, populated by the callback, refreshed in place, encrypted at
  rest). Added `Registry.GetByID` so the persister can reload + merge before `SetAuth`.
- **`daemon`** — an in-memory pending-login store keyed by a random CSRF `state` (10-min TTL,
  one-shot). `POST /upstreams/{name}/oauth/login` mints state + PKCE verifier and returns the
  authorize URL (the UI `window.open`s it). `GET /oauth/callback` (served **top-level** on the UI
  listener, CSRF-exempt — a browser redirect cannot carry the header; the random state is the
  binding) exchanges the code and persists the tokens. The daemon wires
  `Manager.SetOAuthPersister(d.persistOAuthTokens)` so in-flight refreshes survive a restart. The
  `RedirectURL` defaults to the UI listener's `/oauth/callback` and is computed identically for the
  authorize URL and the exchange.
- **UI** — the Upstreams auth form gains the type + its fields; a **Log in** button on an
  authorization-code host starts the flow and opens the browser.

## Alternatives considered

- **Hand-roll the OAuth2 flow.** Rejected — `x/oauth2` is the canonical, well-tested implementation
  (PKCE helpers, `ReuseTokenSource` auto-refresh), pure Go, already an indirect dependency.
- **Store tokens in a separate table.** Rejected — they are upstream credentials; the encrypted
  `AuthConfig` already is the credential store, so they ride there with everything else masked at
  rest and out of list responses.
- **A dedicated callback port.** Rejected — the UI listener already exists on loopback; serving the
  callback there avoids another bound port and keeps the redirect URL stable.
- **Open the browser server-side.** Rejected — the UI runs in a browser already; returning the URL
  and letting the front-end `window.open` it works for both desktop and headless-with-a-browser.

## Consequences

- Operators can front user-authenticated OIDC APIs; the token is obtained once via browser, then
  auto-refreshed, and the agent never sees it.
- Refresh-token rotation survives a daemon restart (tokens are persisted on refresh). If an IdP
  invalidates the refresh token out-of-band, `Apply` surfaces an error telling the operator to
  re-login.
- `golang.org/x/oauth2` is now a direct dependency (CGO-free).
- The pending-login store is in-memory: a daemon restart mid-login invalidates an outstanding
  authorize redirect (the operator just clicks Log in again). Acceptable — the window is seconds.
