# Current phase

> Forward-only. What's active now and what's queued next. Finished work lives in git.

## Active phase

**Plan 2 — Policy engine + blocking approval + OIDC client-credentials + rate limit.**

Goal: replace the flat `grant` allow-list with a real `policy` engine — rules
`(subject: agent|any, upstream, method+path-glob, rate-limit) → allow|deny|require-approval`,
with specific-agent precedence over global-upstream over default-deny; an `internal/approval`
blocking queue (the data-plane request long-polls until the operator decides, surfaced on the
admin API); and an `oidc-client-credentials` authenticator (token cache + refresh) behind the
existing `authn.For` seam. Not yet started — needs a plan written (invoke writing-plans).

## Done

- **Plan 1 — Foundation & data-plane skeleton.** Shipped: vault (Argon2id+AES-GCM), SQLite
  store, upstream/agent/grant registries, none/static/basic authenticators, data-plane reverse
  proxy (default-deny + auth injection + 503-when-locked), daemon `serve` + unix-socket admin
  API + CLI. 9 packages, all tested; e2e proxy flow verified against httpbin. See git
  `a0a678b..e665a19`.

## Queued candidates (Phase 1, later plans)
- **Plan 3** — MCP control plane (streamable HTTP; `list_upstreams`, `request_access`,
  `get_access`, `whoami`; dynamic agent self-registration).
- **Plan 4** — audit (request journal + body store ≤256 KB, masking, retention).
- **Plan 5** — daemon control API + SSE for the UI.
- **Plan 6** — web UI (React 19 + Vite + Tailwind 4 + Zustand screens).
- **Plan 7** — Wails 3 desktop wrapper.

## Phase 2+ (deferred by design)

OIDC authorization-code (browser login), request body/param filters, ticket/async approval
fallback, audit auto-prune, additional authenticators (mTLS/SigV4/HMAC), headless server mode.
