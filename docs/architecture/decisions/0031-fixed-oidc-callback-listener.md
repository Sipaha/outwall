# ADR-0031: Fixed, configurable OIDC callback listener

- **Status:** accepted
- **Date:** 2026-06-22
- **Extends:** ADR-0021 (OIDC authorization-code browser login).

## Context

The OIDC browser-login callback was served on the **UI listener** (`http://<UIListen>/oauth/callback`,
default `127.0.0.1:8182`). An IdP requires the redirect URI to be **pre-registered** in the client
config and to match exactly. Tying it to the UI port is fragile: the UI bind is a general-purpose
port that an operator may change, and it couples "where the redirect URI points" to "where the web UI
happens to listen". Operators need one stable, well-known redirect URI to register once.

## Decision

Serve the OIDC callback on a **dedicated loopback listener** with a fixed default and make it
configurable.

- `Config.CallbackListen` (default `DefaultCallbackListen = 127.0.0.1:23312`). The redirect URI is
  therefore `http://127.0.0.1:23312/callback` by default — stable and independent of the UI port.
- **On-demand binding.** The listener (`callbackServer`, one route `/callback` → `hOAuthCallback`) is
  NOT bound at startup. `hOAuthLogin` `acquire()`s it (ref-counted) when a login begins; it is
  released — and the port freed — after the callback resolves or the login TTL (10 min) expires. So
  the fixed port is held only while a browser login is actually in flight, not for the daemon's whole
  life. `Daemon.Serve` force-stops it on shutdown.
- The default redirect URI used for both the authorize URL and the token exchange comes from
  `Config.CallbackListen` (`effectiveOAuthCfg`); a custom `RedirectURL` on the upstream still wins,
  and the handler stays mounted at `/oauth/callback` on the UI listener too for that case.
- Configurable: the `serve` command gains `--callback-listen` (default `DefaultCallbackListen`); the
  desktop wrapper sets it to the same fixed bind. `New` defaults an empty value so callers can omit
  it.
- `GET /oidc/redirect-uri` returns the effective URI; the Add-host OIDC form shows it ("register this
  in your IdP") so the operator copies the exact value to register — correct even if the port is
  customized.

## Alternatives considered

- **Keep the callback on the UI listener.** Rejected — couples the registerable redirect URI to a
  mutable, multi-purpose port; changing the UI bind silently breaks logins.
- **A runtime (KV `settings`) value instead of a startup config field.** Rejected — the listener
  binds at startup and the URI must be registered in the IdP ahead of time; a live-editable value
  that only takes effect on restart adds confusion over the existing `Default*`/flag pattern used for
  every other bind.
- **Random/ephemeral callback port per login (like some native CLIs).** Rejected — IdPs that require
  exact redirect-URI registration can't pre-register a random port; a fixed port is registerable once.

## Consequences

- One stable redirect URI (`http://127.0.0.1:23312/callback`) to register in the IdP, surfaced in the
  UI; overridable via `--callback-listen` and per-upstream `RedirectURL`.
- The fixed port is bound only during an in-flight login, so it doesn't permanently occupy 23312
  (two instances, tests, etc. don't collide while idle). Covered by `TestOAuthRedirectURIFixedCallback`
  (endpoint + authorize-URL `redirect_uri`) and the on-demand up/down assertions in
  `TestOAuthLoginAndCallbackStoresTokens`.
