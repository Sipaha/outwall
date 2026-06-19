# ADR-0019: Additional upstream auth schemes — mTLS, AWS SigV4, HMAC

- **Status:** accepted
- **Date:** 2026-06-19

## Context

`authn` shipped with `none`/`static`/`basic`/`oidc-client-credentials` header authenticators
(ADR-0001/0002) plus a per-target TLS **transport seam** introduced for Kubernetes client-cert /
CA trust (ADR-0008: `Manager.build` returns `(Authenticator, http.RoundTripper, error)`; the proxy
applies both). Real upstreams also require **mutual TLS** (client certificate), **AWS Signature V4**
(API Gateway / managed AWS APIs), and ad-hoc **HMAC request signatures**. The agent must never see
any of this material — it is vault-encrypted and injected by outwall, exactly like the existing
schemes.

## Decision

Three new `AuthConfig.Type` values for http upstreams; secrets live in the existing encrypted
`AuthConfig` (new fields) and the fingerprint cache key is extended so an edit rebuilds the cached
authenticator/transport.

- **`mtls`** — reuses the transport seam. `Manager.build` returns `noneAuth` (no header) plus an
  `*http.Transport` (cloned from `http.DefaultTransport`) whose `TLSClientConfig` presents
  `tls.X509KeyPair(ClientCert, ClientKey)` and, when `CABundle` is set, trusts only it. Reuses the
  existing `ClientCert`/`ClientKey`/`CABundle` fields. (`mtls.go`.)
- **`sigv4`** — a header authenticator using `aws-sdk-go-v2/aws/signer/v4` (pure Go, CGO-free).
  `Apply` computes the SHA-256 payload hash (the well-known empty hash for a body-less request; for a
  body it reads, hashes, then **restores** `r.Body`/`GetBody`/`ContentLength`) and calls
  `signer.SignHTTP`, setting `Authorization`/`X-Amz-Date`. New fields: `AWSAccessKeyID`,
  `AWSSecretAccessKey`, `AWSRegion`, `AWSService`. The clock is injectable for deterministic tests.
  (`sigv4.go`.)
- **`hmac`** — outwall's own generic signing scheme (NOT a vendor standard). The canonical string is
  `"{METHOD}\n{request-uri}\n{unix-timestamp}"` (request-uri = `r.URL.RequestURI()`); the hex
  `HMAC(algo, secret, canonical)` goes in the configured `HMACHeader` and the timestamp in
  `X-Timestamp` so a verifier can rebuild it. Algo is `sha256` (default) or `sha512`. New fields:
  `HMACSecret`, `HMACHeader`, `HMACAlgo`. Clock injectable for tests. (`hmac.go`.)

`For(cfg)` handles `sigv4`/`hmac` (and returns `noneAuth` for `mtls`); `Manager.build` additionally
handles `mtls`'s transport. `fingerprint` now folds in all seven new fields. The UI Upstreams auth
form gained the three new types and their inputs.

## Alternatives considered

- **Hand-rolled SigV4.** Rejected: the canonicalization (header set, double URI-encoding,
  chunked-payload edge cases) is error-prone; the official `aws-sdk-go-v2` signer is pure Go, so it
  costs nothing against the no-CGO rule and is correct by construction.
- **mTLS as a header.** Impossible — a client certificate is a TLS-handshake credential, which is
  exactly why the transport seam exists. mTLS slots straight into it.
- **Adopting a named HMAC standard (e.g. draft-cavage HTTP signatures).** Deferred: most ad-hoc
  HMAC integrations are bespoke; a simple, documented canonical string covers them now, and a named
  scheme can be added later as another type without disturbing this one.

## Consequences

- outwall can now front mTLS, AWS-signed, and HMAC-signed APIs with the credential held in the
  vault — the agent calls the plain proxy URL and never sees the secret or the certificate.
- One new pure-Go dependency tree (`aws-sdk-go-v2` + `smithy-go`); `make build` stays CGO-free.
- The HMAC canonical string is outwall-specific and pinned here; a verifier on the upstream side must
  match it (method, `RequestURI`, unix timestamp, the two headers).
- Adding a further scheme (SigV4a, draft-cavage, OAuth1) is the same recipe: new `AuthConfig` fields,
  a `For`/`build` branch, a fingerprint addition, and a UI block.
