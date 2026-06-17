# module: internal/k8s

Kubernetes-specific helpers, with no dependency on `k8s.io/client-go`.

- **Request parsing** — `Parse(method, path, query)` decomposes a raw k8s API request (path
  already stripped of the `/<cluster>` prefix) into the RBAC tuple, mirroring kube-apiserver's
  RequestInfoResolver with pure string logic. `/api/v1/...` is the core group;
  `/apis/<group>/<version>/...` a named group; `.../namespaces/<ns>/<resource>[/<name>[/<sub>]]`
  is namespaced (the literal `namespaces/<name>` resource is cluster-scoped). The verb comes
  from method + shape + query: GET collection ⇒ `list`, GET named ⇒ `get`, `?watch=true` (or
  `?follow=true` on `pods/log`) ⇒ `watch`, POST ⇒ `create`, PUT ⇒ `update`, PATCH ⇒ `patch`,
  DELETE named ⇒ `delete`, DELETE collection ⇒ `deletecollection`. Discovery/health paths
  (`/healthz`, `/version`, `/api`, `/apis`, `/openapi/...`) return `IsResource=false`.
- **Upgrade subresources** — `RequestInfo.IsUpgrade()` is true for the interactive pod
  subresources `exec`, `attach`, `portforward` (`cp` rides on `exec`). These negotiate an HTTP
  connection upgrade (WebSocket on modern clusters, SPDY on older ones) and are authorized as
  the `create` verb regardless of the wire method — `verbFor` maps them to `create` before the
  method switch, so a WebSocket exec (`GET`) and an SPDY exec (`POST`) both resolve to `create`
  and a rule `(ns, pods/exec, create)` grants them (see ADR-0010).
- **Kubeconfig** — `Kubeconfig(serverURL, clusterName, caPEM, agentToken)` assembles the agent
  kubeconfig YAML pointing at the data plane (`server: <dataPlaneURL>/<cluster>`), with the
  local CA in `certificate-authority-data` and the agent's **own** outwall token in
  `users[0].user.token`. The cluster's real credentials are never present (see ADR-0008 §7).

## Public API

- `type RequestInfo struct { IsResource bool; Namespace, APIGroup, Resource, Subresource, Name, Verb string }`
- `(RequestInfo).IsUpgrade() bool` — true for subresources `exec`/`attach`/`portforward`.
- `Parse(method, path string, query url.Values) RequestInfo`
- `Kubeconfig(serverURL, clusterName, caPEM, agentToken string) ([]byte, error)`
