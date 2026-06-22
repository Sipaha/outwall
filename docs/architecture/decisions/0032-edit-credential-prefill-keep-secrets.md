# ADR-0032: Replace-credential pre-fills current settings and keeps secrets left blank

- **Status:** accepted
- **Date:** 2026-06-22

## Context

"Replace credential" opened a blank form defaulting to Static header — it did not reflect the host's
current auth (e.g. an `oidc-authorization-code` host with its issuer endpoints, client id, scope).
Worse, `SetAuth` replaces the **whole** config, so saving wiped the client secret and the
server-managed OIDC tokens — changing one field (e.g. the client id) forced re-entering the secret
and a fresh browser login. The operator could not make a small edit.

## Decision

- **Pre-fill from current settings.** `GET /upstreams` now returns, for http upstreams, an `auth`
  object with the **non-secret** fields (`AuthConfig.Public()` — type, endpoints, client id,
  header/username, scope, region, …; every secret zeroed). The replace-credential form initialises
  from it; secrets come back blank.
- **Keep secrets left blank on save.** `POST /upstreams/{name}/auth` merges (`mergeKeepSecrets`):
  when the auth **type is unchanged**, any blank secret field (token, password, client secret, HMAC
  secret, client key, AWS secret) keeps the stored value, and the server-managed OIDC tokens
  (access/refresh/expiry) are always carried over. So editing one field and leaving secrets blank
  preserves the secret and the existing login. Changing the **type** is a full reconfigure — taken as
  sent (no carryover).
- UI hint on the replace modal: "Current settings are pre-filled. Leave secret fields blank to keep
  the stored value (and any OIDC login)."

## Alternatives considered

- **Return the actual secrets to pre-fill the form.** Rejected — secrets must never leave the vault/
  daemon; the form only needs the non-secret shape, and blank-means-keep covers edits.
- **Always replace wholesale (status quo).** Rejected — it makes any edit destructive (re-enter
  secret, re-login), which is exactly the reported pain.
- **A dedicated PATCH endpoint for single fields.** Rejected — the blank-means-keep merge on the
  existing SetAuth route covers the need without a new endpoint or partial-update semantics to design.

## Consequences

- Operators can tweak one OIDC field (client id, scope, endpoint) without losing the client secret or
  the browser login. Setting a credential on a fresh host still starts on Static.
- `GET /upstreams` carries non-secret auth for http upstreams (`AuthConfig.Public()`); secrets remain
  daemon-only. Covered by `TestSetAuthKeepsSecretsOnSameTypeReplace`.
