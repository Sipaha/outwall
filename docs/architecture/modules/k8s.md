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
- **Kubeconfig import (K4)** — `ParseKubeconfig(data, baseDir)` flattens every *context* of an
  operator kubeconfig into `[]ParsedCluster` (the context name → upstream name, `cluster.server`
  → `BaseURL`, the user/cluster pair → `upstream.AuthConfig`), decoding base64 `*-data` fields and
  resolving file-path refs (`certificate-authority`, `client-certificate`, `client-key`,
  `tokenFile`) relative to `baseDir`. Unsupported/empty contexts are skipped with a warning, never
  a whole-file error; a YAML parse failure is a real error. Parsed with `gopkg.in/yaml.v3` — **no
  `k8s.io/client-go`**. `cluster.insecure-skip-tls-verify: true` sets `K8sInsecureSkipVerify`, but
  only when no CA is present (the CA always wins). `DiscoverKubeconfigPaths()` returns `$KUBECONFIG`
  (split on the OS list separator) else `<home>/.kube/config`.
- **Importer (K4)** — `Importer{Reg, Log}.Import(paths)` reads each existing path, parses it, and
  `CreateKind`s every context whose name is not already an upstream (idempotent skip-existing).
  Needs the vault unlocked (Create encrypts) — a locked vault yields a wrapped `secret.ErrLocked`.
  Missing paths are skipped. Returns `(added, skipped []string, err)`. It `slog.Warn`s loudly when
  registering a cluster whose kubeconfig disabled TLS verification (see ADR-0011). The daemon runs
  it best-effort on vault unlock and via `POST /clusters/import`.

## Public API

- `type RequestInfo struct { IsResource bool; Namespace, APIGroup, Resource, Subresource, Name, Verb string }`
- `(RequestInfo).IsUpgrade() bool` — true for subresources `exec`/`attach`/`portforward`.
- `Parse(method, path string, query url.Values) RequestInfo`
- `Kubeconfig(serverURL, clusterName, caPEM, agentToken string) ([]byte, error)`
- `type ParsedCluster struct { Name, Server string; Auth upstream.AuthConfig }`
- `DiscoverKubeconfigPaths() []string`
- `ParseKubeconfig(data []byte, baseDir string) (clusters []ParsedCluster, warnings []string, err error)`
- `type Importer struct { Reg *upstream.Registry; Log *slog.Logger }`
- `(*Importer).Import(paths []string) (added, skipped []string, err error)`
