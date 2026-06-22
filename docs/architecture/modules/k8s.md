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
  only when no CA is present (the CA always wins). `DiscoverKubeconfigPaths()` returns the
  `$KUBECONFIG` entries (when set) **plus every regular file directly under `<home>/.kube/`**
  (subdirs like `cache`/`http-cache` skipped), de-duplicated — a deliberate divergence from
  kubectl's `$KUBECONFIG`-only precedence so the operator's clusters spread across sibling files
  (the way Lens aggregates them) all import (K5, ADR-0012). The testable seam is
  `discoverKubeconfigPathsIn(kubeDir, kubeconfigEnv)`.
- **Importer (K4/K5)** — `Importer{Reg, Log}.Import(paths, update)` reads each existing path, parses
  it, and `CreateKind`s every context whose name is not already an upstream. A discovered file that
  is **not** a kubeconfig (junk in `~/.kube`) is skipped with a `slog.Warn`, never fatal. Needs the
  vault unlocked (Create encrypts) — a locked vault yields a wrapped `secret.ErrLocked`. Missing
  paths are skipped. Returns non-nil `(added, updated, skipped []string, err)` (a nil slice would
  JSON-encode to `null` and break the UI toast). It `slog.Warn`s loudly when registering a cluster
  whose kubeconfig disabled TLS verification (see ADR-0011). `update` controls existing-name
  handling: `false` skips (the **init-only** auto-scan), `true` refreshes the cluster in place via
  `upstream.UpdateTarget` — same upstream ID, so its rules survive (see ADR-0026). The daemon runs
  the auto-scan best-effort **only on vault init** (first run — seeds an empty vault); it is **no
  longer** run on unlock. Later imports go through `POST /clusters/import`.
- **`ImportContent` (K5)** — `Importer.ImportContent(data, baseDir, update)` parses one
  operator-uploaded kubeconfig document (the Clusters file-picker) and registers its contexts.
  Unlike the auto-scan, a non-kubeconfig upload is a **real error** (the operator explicitly chose
  the file). `POST /clusters/import` with a non-empty request body imports it; an empty body
  re-scans. Both explicit paths pass `update=true` so the operator can repair/rotate a cluster's
  credential. Shares the core with `Import` (see ADR-0012, ADR-0026).

## Public API

- `type RequestInfo struct { IsResource bool; Namespace, APIGroup, Resource, Subresource, Name, Verb string }`
- `(RequestInfo).IsUpgrade() bool` — true for subresources `exec`/`attach`/`portforward`.
- `Parse(method, path string, query url.Values) RequestInfo`
- `Kubeconfig(serverURL, clusterName, caPEM, agentToken string) ([]byte, error)`
- `type ParsedCluster struct { Name, Server string; Auth upstream.AuthConfig }`
- `DiscoverKubeconfigPaths() []string`
- `ParseKubeconfig(data []byte, baseDir string) (clusters []ParsedCluster, warnings []string, err error)`
- `type Importer struct { Reg *upstream.Registry; Log *slog.Logger }`
- `(*Importer).Import(paths []string, update bool) (added, updated, skipped []string, err error)`
- `(*Importer).ImportContent(data []byte, baseDir string, update bool) (added, updated, skipped []string, err error)`
