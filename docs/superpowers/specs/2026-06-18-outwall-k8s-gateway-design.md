# outwall — Kubernetes Gateway Design Spec

**Date:** 2026-06-18
**Status:** Draft (brainstorming) — pending user approval, then implementation plans
**Builds on:** `2026-06-17-outwall-design.md` (Phase 1, shipped). This spec extends the
existing two-plane gateway; it does not replace it.

## 1. Purpose

Give AI agents controlled access to **Kubernetes clusters** through the same
request-rights + approval + audit machinery outwall already provides for HTTP APIs — so an
agent can read logs and operate workloads **without ever holding the cluster's real
credentials and without being able to wreck production**.

Two concrete user goals drive the design:

- **(a)** grant an agent access to **all logs in a namespace**;
- **(b)** let an agent **change deployments** (e.g. patch an image tag to roll a new
  version) — but mutating actions go through **operator approval**.

Target clusters are **managed** (EKS / GKE / AKS).

**Core invariant (unchanged):** the cluster's real credentials never leave outwall. The
agent only ever sees a local URL and **its own existing outwall bearer token**. outwall
terminates TLS, so every API request/response is fully visible for policy and audit.

## 2. Integration model — Kubernetes API reverse-proxy (decided: A, not a CLI wrapper)

`kubectl`, `client-go`, `helm` and a raw `curl` are all just HTTP clients of the
Kubernetes **REST API**. outwall proxies that REST API. We do **not** wrap or shell out to
the `kubectl` binary.

Why (rejected: a server-side `kubectl(namespace, args)` command tool):

- Deciding allow/deny would mean **parsing kubectl's argv** — hundreds of flags, aliases
  (`po`=pods), `-f file`, stdin (`apply -f -`). One missed flag is a policy hole. Killer
  case: a rule pinned to namespace `X` is escaped by `--namespace other` or
  `--all-namespaces` (which *overrides* the namespace arg).
- At the **HTTP layer** the namespace / resource / verb are unambiguous in the URL + method.
  `--all-namespaces` simply becomes a different URL (`/api/v1/pods`, no namespace segment)
  that policy plainly sees and can deny.
- Reuses the entire Phase-1 data plane: vault, reverse proxy, audit (req/resp bodies = the
  real API traffic), blocking approval, rate limit.
- Tool-agnostic: any k8s client works through one endpoint.

The agent reaches the proxy two ways, both identical on outwall's side:

- **raw HTTP** (curl / client-go): URL + `Authorization: Bearer <agent-token>`. No file.
- **`kubectl`**: the binary refuses to run without a **kubeconfig** file (server + CA +
  token). That file requirement is kubectl's, not outwall's — it is the same URL + token,
  in kubectl's config format. We support it because LLM agents write `kubectl get pods -n
  prod` far more reliably than raw REST paths. The kubeconfig is **client-side ergonomics,
  not a credential** (see §7).

## 3. Routing — cluster selector in the first path segment

Multi-cluster from the start. A cluster reuses the existing `<first-segment>` routing that
`proxy.ServeHTTP` already does for upstreams:

```
https://outwall:PORT/<cluster>/api/v1/namespaces/<ns>/pods/<pod>/log?follow=true
                     └ cluster ┘└────────────── raw k8s API path ──────────────┘
```

The agent's kubeconfig sets `server: https://outwall:PORT/<cluster>`. Multi-cluster falls
out of the existing prefix routing for free — `<cluster>` is the same first segment that
`<upstream>` is today, resolved via `Upstreams.GetByName`.

## 4. Cluster as a new kind of target

A **cluster** is registered like an upstream (same registry, same first-segment routing),
but carries k8s-specific connection data. Concretely, `upstream.Upstream` gains a `Kind`
field (`"http"` default | `"k8s"`); a k8s target's config holds what's parsed from the
operator's kubeconfig:

- **API server URL** (the real `server:`),
- **CA bundle** to trust the API server,
- **cluster auth** — one of `token` / `client-cert` / `exec` (§6),
- (optional) a pinned default namespace, ignored for policy.

All of this is **encrypted in the vault**, exactly like upstream auth configs today.

### 4.1 Connection needs a transport seam, not just a header (design finding)

Phase-1 `authn.Authenticator` only does `Apply(req *http.Request)` — it mutates **headers**.
That is enough for a bearer token, but **not** for a cluster:

- the API server is HTTPS with its **own CA** → outwall's outbound client must trust that
  CA (transport-level `tls.Config.RootCAs`);
- **client-cert** auth presents a **client certificate** (mTLS) → transport-level, not a
  header.

So a k8s target needs a per-cluster **`http.RoundTripper` / `tls.Config`**, built once from
the kubeconfig and cached, in addition to (for token/exec) a header-injecting step. The
header-only `Authenticator` interface is extended (or paralleled by a `Transport(cluster)`
provider on the auth Manager) so the proxy can obtain both the transport and the header
mutation for the matched target. This is recorded as an ADR decision.

## 5. Policy — extend the existing engine (not a parallel `k8spolicy`)

The Phase-1 rule shape generalizes one-to-one; we keep the engine, its precedence
(agent-specific > any > default-deny) and its most-restrictive-wins tier resolution.

| HTTP rule dimension | k8s rule dimension |
|---|---|
| `UpstreamID` | `ClusterID` (same field, a k8s-kind target) |
| `Method` | `Verb` (get/list/watch/create/update/patch/delete/…) |
| `PathGlob` | `Namespace` + `Resource` (+ subresource), with wildcards |
| `RateLimitPerMin` | unchanged |
| `Outcome` | unchanged (allow / deny / require-approval) |

So a k8s rule is **`(agent, cluster, namespace, resource[/subresource], verb) → outcome`**,
with `*` wildcards on namespace / resource / verb. This is **exactly the k8s RBAC tuple**
(`namespace, apiGroup, resource, subresource, verb`), so operators already reason in these
terms.

Implementation: `policy.Input` and `policy.Rule` gain optional k8s fields; when the matched
target is k8s-kind, the per-rule match predicate uses (namespace, resource, verb) matching
instead of (method, path-glob). The **tiering / precedence code is untouched** — only the
match predicate branches. The single `Policy.Decide(...)` call-site in `proxy.ServeHTTP`
stays the one seam.

### 5.1 Parsing an API request into (namespace, resource, subresource, verb)

The proxy parses the raw k8s path + method + query into the RBAC tuple, mirroring
kube-apiserver's own `RequestInfo` resolution:

- `/api/v1/...` = core group; `/apis/<group>/<version>/...` = named group.
- `/.../namespaces/<ns>/<resource>[/<name>[/<subresource>]]` → namespaced.
- No `namespaces/<ns>` segment → **cluster-scoped** (`namespace = ""`), e.g. `/api/v1/nodes`,
  `/api/v1/namespaces` (list namespaces), CRD definitions.
- **verb** from method + shape + query:
  - `GET` collection (`/pods`) = **list**; `GET` named (`/pods/x`) = **get**;
    `?watch=true` (or `?follow=` on logs) ⇒ **watch** semantics.
  - `POST` = **create**; `PUT` = **update**; `PATCH` = **patch**;
    `DELETE` named = **delete**; `DELETE` collection = **deletecollection**.
  - subresources are part of the resource key: `pods/log`, `pods/exec`,
    `deployments/scale`, `deployments/status`.

### 5.2 Namespace scoping — the safety property

A request with **no namespace** (cluster-scoped, or an all-namespaces list like
`/api/v1/pods`) has `namespace = ""`. It matches **only** rules whose namespace pattern is
`*` (explicitly all-namespaces) — never a rule scoped to a concrete namespace. So granting
`prod` does **not** leak an all-namespaces list. Cluster-scoped resources (nodes, etc.) are
default-deny until a `*`-namespace (or cluster-scoped) rule is added.

### 5.3 The two user goals expressed as rules

- **(a) all logs in namespace `prod`** — allow rules:
  `(agent, cluster, ns=prod, resource=pods, verb=get|list|watch)` and
  `(agent, cluster, ns=prod, resource=pods/log, verb=get)`.
  (Reading logs of a Deployment is sugar kubectl resolves to its pods, so pods + pods/log
  is sufficient; optionally add `deployments,replicasets get|list` so `kubectl logs
  deploy/x` can resolve the pod.)
- **(b) change deployments / image tag** — a **require-approval** rule:
  `(agent, cluster, ns=prod, resource=deployments, verb=patch|update) → require-approval`.
  The mutating call blocks until the operator allows it (§8).

## 6. Cluster auth (outwall → API server) — managed clusters first

These are the cluster's **real** credentials, held by outwall, never seen by the agent.
Three methods, **all first-class in the first plan** because the target is managed k8s:

- **token** — a bearer/ServiceAccount token. `Authorization: Bearer <token>` injected
  outbound. Simplest; covers most self-hosted.
- **client-cert** — client certificate + key (classic kubeadm admin). Transport-level mTLS
  (§4.1), no header.
- **exec** — the kubeconfig names an external binary (`aws eks get-token`,
  `gke-gcloud-auth-plugin`, `kubelogin`) that prints a short-lived token. **This is how
  EKS/GKE/AKS authenticate**, so it is not deferrable.

### 6.1 exec-plugin implications (baked into the design)

- The outwall **host** must have the cloud CLIs installed **and cloud credentials**
  (env / `~/.aws` / `~/.config/gcloud` / instance role). These cloud creds are simply
  **another secret outwall holds**; they never reach the agent.
- The exec plugin emits **short-lived** tokens → cached & refreshed via the **existing**
  `authn.Manager` token-cache (the same machinery built for OIDC client-credentials — reuse,
  do not rewrite). Cache key = cluster.
- The exec binary comes from the **operator-registered** kubeconfig, not from any agent.
  "outwall executes a binary named in a config" is therefore a **trusted-input boundary**,
  not a hole — but it is called out explicitly in the ADR, with hardening notes (no
  agent-influenced argv/env; bounded timeout; the plugin's stdout token is treated as a
  secret and masked in audit).

## 7. Agent credential & kubeconfig (ergonomics, not a secret)

The agent needs **no new credential**. It reuses its existing outwall bearer token (minted
at MCP registration in Phase 1). The "kubeconfig" the agent uses is just packaging:

```yaml
clusters:  [{ cluster: { server: https://outwall:PORT/<cluster>,
                         certificate-authority-data: <outwall local CA> } }]
users:     [{ user: { token: <agent's existing outwall bearer token> } }]
```

Real cluster creds are **never** in it. A raw-HTTP agent needs no file at all. We ship an
**optional helper** that prints this file — a CLI (`outwall kubeconfig <agent> <cluster>`)
and/or an MCP tool — assembling it from already-known values. It is convenience, **not a
secret-issuance subsystem**.

### 7.1 Client→outwall TLS = a local CA (decided)

kubectl/client-go **validate the server cert** at `server:`. So the data plane must present
a cert they trust:

- On first run outwall generates a **local CA**, and issues a data-plane **server cert**
  signed by it. The kubeconfig helper embeds the CA in `certificate-authority-data`.
  kubectl validates honestly — **no `--insecure-skip-tls-verify`**. CA key lives in the
  data-dir / vault. (Rejected: documenting `--insecure-skip-tls-verify` — demo-grade; the
  agent would not validate who it is talking to.)
- This local-CA machinery is reusable later to put TLS on the HTTP data plane too.

## 8. Approval, streaming, exec — the live behaviors

- **Mutating verbs** (patch/update/create/delete) are the natural `require-approval`
  candidates; read verbs auto-allow once granted. Approval reuses the Phase-1 **blocking
  long-poll** queue (`approval.Submit` before proxying): the API call hangs until the
  operator clicks allow/deny in the UI; on allow it proceeds and the agent gets the **real**
  API response. The approval card shows the parsed tuple + purpose + (for patch) the request
  body diff.
- **Streaming reads** — `kubectl logs -f` (`?follow=true`) and `get -w` (`?watch=true`) are
  long-lived chunked HTTP responses. The reverse proxy must **flush** them through
  (immediate flush / `FlushInterval`). Audit's capped body tee must **not** buffer an
  unbounded stream: it captures up to the 256 KB cap then stops (Phase-1 behavior), and the
  journal entry is written when the stream finally closes (agent disconnects). In scope.
- **exec / attach / cp / port-forward** — connection **upgrade** (modern k8s = WebSocket
  `*.channel.k8s.io`; older = SPDY). Go's `httputil.ReverseProxy` already proxies `Upgrade`
  / 101 bidirectional streams, so the transport largely works through the same proxy.
  Auditing here is **not** request/response bodies but an interactive duplex stream → audit
  records **metadata** (agent, cluster, namespace, pod, container, the command from the
  query, start/end, bytes) rather than the full transcript (optionally a capped capture).
  `require-approval` on `exec` blocks the upgrade until approved, same as any verb. In scope,
  delivered as its own plan because the transport + audit handling differ.

## 9. Control plane (MCP) surface

The agent discovers and requests cluster access through the same MCP server:

- `list_upstreams` also lists **clusters** (kind=k8s) with the agent's status per cluster.
- `request_access(target, purpose)` accepts a k8s target, scoped as `cluster` or
  `cluster/namespace`, with mandatory **purpose** (stored, shown at approval + audit) — same
  contract as HTTP.
- `get_access(cluster)` returns the base path + a short memo of allowed namespaces / verbs.
- an MCP tool to **emit the kubeconfig** (§7) for a granted cluster.

Default-deny is unchanged: a freshly registered agent can do nothing on any cluster until
rules are added.

## 10. Audit

Reuses the Phase-1 journal. Per k8s request: agent, cluster, namespace, resource(+sub),
verb, the raw path+query, status, duration, sizes, decision (+ approver/when), req/resp
bodies up to 256 KB (a `patch` body is exactly the change made — high-value), injected
cluster credentials **masked**. Streaming/exec entries as in §8. (Phase-1 audit is data-
plane-only; the MCP access-request intent log already captures `request_access` purpose.)

## 11. Delivery plans (no feature cut — full product)

Decomposed for delivery, but **every feature above ships**. Proposed plan split (ADR
numbers continue from 0007; next free is **ADR-0008**):

- **Plan K1 — Cluster target + connection + read access.** `Kind=k8s` target registry +
  vault config; transport seam (§4.1) with **token / client-cert / exec** auth (§6) incl.
  exec token-cache reuse; local-CA + data-plane server cert (§7.1); k8s path→tuple parser
  (§5.1); policy extension to (namespace, resource, verb) with default-deny + namespace
  safety (§5.2); read verbs (get/list/watch) + **log/watch streaming** (§8); kubeconfig
  helper (§7); MCP cluster discovery (§9); audit of k8s requests (§10). Delivers user goal
  **(a)**. ADR-0008.
- **Plan K2 — Mutating verbs + approval.** create/update/patch/delete through the policy +
  **blocking approval** with the patch-diff approval card (§8). Delivers user goal **(b)**.
  ADR-0009.
- **Plan K3 — exec / attach / cp / port-forward.** Upgrade (WebSocket/SPDY) proxying +
  metadata audit + approval-on-upgrade (§8). ADR-0010.
- **UI** (folded across the plans): a Clusters screen (register cluster, paste/point
  kubeconfig, pick namespaces), k8s-aware Rules editor (namespace/resource/verb), and the
  enriched approval card. Reuses the Phase-1 React console.

## 12. Out of scope (explicit YAGNI)

- **In-cluster RBAC authoring** — outwall gates at the proxy; it does not manage the
  cluster's own RBAC. The cluster credential outwall holds should itself be least-privilege,
  but provisioning it is the operator's job.
- **Admission-style content policy** beyond verb/resource/namespace (e.g. "patch may set
  image but not replicas") — a later phase; MVP gates at the verb granularity + approval.
- **CRD-aware resource validation** — the parser handles arbitrary `apis/<group>` generically;
  it does not need a resource catalog.
- **Network-exposed / multi-operator** — outwall stays desktop-only, localhost-only.
