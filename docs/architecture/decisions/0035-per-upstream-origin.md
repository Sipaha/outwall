# ADR-0035: Per-upstream origin (subdomain data plane) for browser browsing

- **Status:** accepted
- **Date:** 2026-06-22

## Context

The data plane is a path-prefix reverse proxy on ONE shared loopback origin:
`https://127.0.0.1:<port>/<upstream>/…`. That works for API clients (explicit URLs) and kubectl
(server URL), but it cannot transparently serve a **web app to a browser**. A page proxied under
`/<upstream>/…` breaks the moment it uses a root-relative link (`/static/x` resolves to
`127.0.0.1:<port>/static/x` — the wrong upstream), an absolute link or a redirect to its own real
host (the browser leaves the proxy entirely), or a `Set-Cookie; Domain=<real-host>` (the browser
drops it — wrong origin). ADR-0033 added the `outwall_token` cookie so a browser can authenticate to
the data plane, and explicitly deferred "a distinct origin per upstream" — this ADR is that fix.

Two ways to map an upstream to its own origin were considered: a dedicated loopback **port** per
upstream, or a **subdomain** per upstream. Ports are weak for isolation — cookies and several
security boundaries ignore the port, so two upstreams on different ports share a cookie jar on
`127.0.0.1` — and require managing a port pool and N listeners. Subdomains give real per-host origin
isolation with one listener and one wildcard-ish cert story.

## Decision

Give each http upstream its own **browser origin** `https://<name>.<browse-domain>:<port>/…`
(default browse domain `outwall.localhost`, configurable via `--browse-domain` /
`Config.BrowseDomain`), served by the **same** data-plane listener. The path-prefix model on
`127.0.0.1`/`localhost` is unchanged — API clients and kubectl keep working exactly as before.

- **Host-based routing.** The proxy inspects the request `Host`: a `<name>.<browse-domain>` host
  selects upstream `<name>` and forwards the **full** request path (no prefix strip), so a browser's
  root-relative and relative URLs resolve to the same upstream. Any other host
  (`127.0.0.1`/`localhost`) keeps the path-prefix model byte-for-byte. (`proxy.go` ServeHTTP.)
- **Per-SNI TLS.** The data-plane `tls.Config` uses a `GetCertificate` callback: for an SNI ending
  in `.<browse-domain>` it returns a CA-signed leaf issued (and cached) for exactly that SNI
  (`tlsca.CA.ServerCertFor`); for an IP/`localhost`/empty SNI it returns the static loopback cert.
  Per-SNI issuance (not a `*.<browse-domain>` wildcard) is required because a one-label wildcard does
  NOT match a **dotted** upstream name like `enterprise.ecos24.ru.outwall.localhost`. Chromium
  resolves any `*.localhost` to loopback natively, so Playwright needs no `/etc/hosts` entry — only
  to trust the outwall CA (or run with `ignoreHTTPSErrors`).
- **Bounded browse-mode response rewriting (host-routed requests only).** A `Location` header that
  points at the upstream's real origin is rewritten to the browse origin (relative `Location` is left
  untouched — it already resolves correctly); every `Set-Cookie` loses its `Domain=` attribute so the
  cookie binds host-only to the browse origin (`Path`/`Secure`/`SameSite`/`HttpOnly` kept). **Page
  content (HTML/CSS/JS) is NOT rewritten** — root-relative/relative URLs already resolve under the
  per-upstream origin; a JS-built absolute URL to the real host remains the known residual (rare for
  same-origin SPAs). The path-prefix mode keeps `ModifyResponse` audit-only, unchanged.
- **Discovery.** `get_access` (and `RequestHostAccess`/`RequestAccess`) and `GET /upstreams` return a
  `browse_url` for http upstreams whose name is a valid DNS host (`https://<name>.<browse-domain>:<port>`);
  k8s clusters and names containing `@`/`/` get none. The Hosts UI shows it.

## Closes the ADR-0033 confused-deputy

Under the shared origin, a page already proxied through the data plane (upstream A's HTML) was
*same-origin* with the proxy, so its JS could `fetch('/B/…')` to reach another upstream B carrying the
cookie. With a distinct origin per upstream, A's page lives on `https://A.<browse-domain>` and a
`fetch` to `https://B.<browse-domain>` is **cross-site** — the browser does not auto-send B's cookie,
and the `Sec-Fetch-Site` guard (ADR-0033) rejects cookie auth on a cross-site request. The
`outwall_token` cookie is now scoped per browse origin. Header auth is unchanged.

## Alternatives considered

- **Dedicated loopback port per upstream.** Rejected: cookies/`outwall_token` ignore the port, so
  isolation between upstreams is weak (shared `127.0.0.1` cookie jar) and the confused-deputy is not
  closed; plus port-pool management and N listeners.
- **`*.outwall.localhost` wildcard cert.** Rejected: a one-label wildcard does not cover dotted
  upstream names (`enterprise.ecos24.ru.outwall.localhost`). Per-SNI issuance covers any name.
- **HTML/CSS/JS content rewriting.** Rejected as fragile (JS-built URLs can't be reliably rewritten).
  Per-upstream origin makes root-relative/relative URLs resolve without any content rewriting; only
  the `Location` header and `Set-Cookie` domain need touching.

## Consequences

- A browser/Playwright can load a full web app (e.g. an OIDC-protected Citeck app the operator logged
  into) through outwall at `https://<name>.outwall.localhost:<port>/`, with the agent carrying
  `outwall_token` as a per-origin cookie — the agent never sees the upstream credential.
- Per-upstream origin strengthens isolation and closes the ADR-0033 residual confused-deputy.
- The path-prefix model is fully preserved for API clients and kubectl (every change gates on
  `browse`/Host; non-browse requests are unchanged). Covered by `internal/proxy` tests (Host routing,
  Location + Set-Cookie rewriting), `internal/tlsca` (per-SNI cert incl. a dotted name),
  `internal/daemon` (cert selection), and `internal/mcpsvc`/`internal/daemon` (browse_url discovery).
- **Residual:** JS-built absolute URLs to the real host still escape (documented; rare for same-origin
  SPAs). Non-Chromium clients need `--resolve` to reach a browse origin — but they use the path-prefix
  model anyway. The end-to-end browser run against a real upstream is an operator-driven verification
  (the daemon can't drive the operator's login/browser).
</content>
