# ADR-0008: Kubernetes gateway — read access (Plan K1)

- **Status:** accepted
- **Date:** 2026-06-18

## Context

outwall must let an agent read Kubernetes resources and stream logs/watches in granted
namespaces of a registered cluster, through the existing data plane, without the agent ever
holding the cluster's real credentials. The design spec
(`docs/superpowers/specs/2026-06-18-outwall-k8s-gateway-design.md`) decided the integration
model: outwall proxies the Kubernetes REST API rather than wrapping `kubectl`. Plan K1
delivers the read half (get/list/watch + log/watch streaming); mutating verbs + approval are
K2, and exec/attach/port-forward are K3.

Several forces shaped the implementation:

- The Phase-1 data plane is an HTTP reverse proxy with a header-only credential seam
  (`authn.Authenticator.Apply`). A cluster is HTTPS with its **own CA**, and one auth method
  (client-cert) is **mTLS** — neither is expressible as a header.
- Managed clusters (EKS/GKE/AKS) authenticate via an **exec credential plugin** that prints a
  short-lived token. outwall must run an operator-named binary.
- kubectl/client-go **validate the server cert** at `server:`, so the data plane must present
  a cert they trust — no `--insecure-skip-tls-verify`.
- Policy must gate on the Kubernetes RBAC tuple `(namespace, resource[/subresource], verb)`,
  and an all-namespaces / cluster-scoped request (`namespace==""`) must not be granted by a
  rule scoped to a concrete namespace.

## Decision

**Reverse-proxy integration.** A cluster is an `upstream.Upstream` with `Kind="k8s"`, whose
`BaseURL` is the real API-server URL and whose encrypted `AuthConfig` carries the cluster CA
(`CABundle`) plus the cluster auth (`K8sAuth` ∈ `token|client-cert|exec`, with `Token` /
`ClientCert`+`ClientKey` / `ExecCommand`+`ExecArgs`+`ExecEnv`). Routing reuses the existing
first-path-segment dispatch (`/<cluster>/api/v1/...`). The proxy parses the raw path+method+
query into the RBAC tuple (`internal/k8s.Parse`, mirroring kube-apiserver's
RequestInfoResolver — pure string logic, **no `k8s.io/client-go`**), evaluates the extended
policy engine, attaches a per-cluster TLS transport, and forwards with `FlushInterval=-1` so
`logs -f` / `-w` stream live.

**Transport-seam extension (the §4.1 finding).** The header-only `Authenticator` cannot do
mTLS or trust a custom CA. We added `(*authn.Manager).Transport(up) (http.RoundTripper,
error)`: for `Kind=="k8s"` it returns an `*http.Transport` whose `tls.Config` trusts
`CABundle` and (for client-cert) presents the client cert; for http upstreams it returns
`nil` (default transport). The transport is cached alongside the `Authenticator`, keyed on a
fingerprint extended to include the k8s fields. The header step still applies: token/exec set
`Authorization: Bearer <token>`; client-cert sets **no** header (identity rides the cert).

**Namespace-safety property.** In `policy.Decide`, when `Input.Kind=="k8s"`, the match
predicate is `verbMatches && nsMatches && resourceMatches` (tier/precedence resolution
unchanged). `nsMatches` enforces: an empty request namespace matches **only** a rule whose
namespace is `*` — never a concrete-namespace rule. Granting `prod` therefore cannot leak an
all-namespaces list or a cluster-scoped resource.

**exec-plugin trusted-input boundary.** `internal/authn.execTokenSource` runs the
operator-registered plugin and caches its `ExecCredential` token until ~30 s before expiry.
Hardening: the command/args/env come **only** from the operator's stored config (never any
agent input); the run is bounded by a 30 s `CommandContext` timeout; a non-zero exit returns
a wrapped error (never a panic); the emitted token is a secret, masked in audit like any
injected credential.

**Local CA + TLS data plane.** `internal/tlsca` loads-or-creates a local CA (ECDSA P-256,
persisted `ca.crt`/`ca.key` at 0600 under the data dir) and issues the data-plane server cert
for `127.0.0.1`/`localhost`. The data plane is served over TLS with that cert; the agent
kubeconfig (`internal/k8s.Kubeconfig`) embeds the CA in `certificate-authority-data` and the
agent's **own** outwall bearer token in `users[0].user.token` — the cluster's real creds are
never in it. Helpers: `outwall kubeconfig <cluster> --token <agent-token>` and the MCP
`get_kubeconfig(cluster)` tool.

## Alternatives considered

- **Server-side `kubectl(namespace, args)` tool** — rejected: deciding allow/deny would mean
  parsing kubectl's argv (hundreds of flags; `--all-namespaces` overrides the namespace arg —
  one missed flag is a policy hole). At the HTTP layer the tuple is unambiguous in URL+method.
- **`k8s.io/client-go` for path parsing / transport** — rejected: a very large dependency for
  what is a few hundred lines of string parsing and a `tls.Config`. We parse paths ourselves.
- **Documenting `--insecure-skip-tls-verify`** — rejected: demo-grade; the agent would not
  validate who it is talking to. The local CA lets kubectl validate honestly.
- **A parallel `k8spolicy` engine** — rejected: the rule shape generalizes one-to-one; only
  the per-rule match predicate branches, so precedence/tiering stays single-sourced.

## Consequences

- Multi-cluster falls out of the existing prefix routing for free.
- The data plane is now HTTPS (CA-issued cert). Existing http upstreams are reached at
  `https://127.0.0.1:PORT/<upstream>/...`; clients must trust the local CA. This also lays the
  groundwork for TLS on the whole data plane.
- **Alpha schema change:** `upstreams` gained a `kind` column; `rules` gained
  `k8s_namespace`/`k8s_resource`/`k8s_verb`. No ALTER-migration fallback is written — existing
  dev DBs are reset (acceptable in alpha).
- Agent tokens are write-once (only their hash is stored), so the `kubeconfig` CLI takes the
  token explicitly via `--token`; the MCP `get_kubeconfig` has the session token naturally.
- K2 will reuse the k8s tuple already carried on `approval.Pending` (Namespace/Resource/Verb)
  for the approval card; K3 adds Upgrade (WebSocket/SPDY) proxying for exec/attach.
- A future revisit (e.g. rotating the local CA, or per-cluster server names) would touch
  `internal/tlsca` and the data-plane listener bootstrap in `internal/daemon`.
