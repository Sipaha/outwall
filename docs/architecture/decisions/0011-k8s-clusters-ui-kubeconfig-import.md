# ADR-0011: Clusters UI + kubeconfig auto-import (Plan K4)

- **Status:** accepted
- **Date:** 2026-06-18

## Context

K1–K3 built the k8s gateway data path: a cluster is an `upstream.Upstream` with `Kind="k8s"`,
and the proxy/policy/transport machinery routes, authorizes, and forwards Kubernetes API
traffic (including exec/attach/port-forward). But there was no way to *manage* clusters from the
desktop UI, and registering one by hand meant transcribing a server URL, a CA bundle, and the
cluster auth out of an existing kubeconfig — error-prone and tedious when the operator already
has a working `~/.kube/config`. Three concrete gaps:

1. **No Clusters screen.** k8s clusters, if created at all (via the CLI), showed up mixed into
   the HTTP **Upstreams** list with no k8s-specific affordances (auth type, kubeconfig export).
2. **No import.** The operator's own kubeconfig is the authoritative source of the clusters they
   can reach; outwall should ingest it rather than make them re-enter everything.
3. **A light native `<select>`.** WebKitGTK paints native form controls (the `<select>` and its
   option popup) with the light UA theme regardless of the Tailwind `bg-muted` class, because no
   `color-scheme` was declared — jarring on the otherwise-dark console.

A force specific to import: some kubeconfig contexts carry `insecure-skip-tls-verify: true`
(common for kind/minikube/self-signed dev clusters). outwall must decide whether to honor that.

## Decision

**Parse kubeconfig with `gopkg.in/yaml.v3`, not `k8s.io/client-go`.** The kubeconfig schema is
small and stable; `internal/k8s.ParseKubeconfig(data, baseDir)` flattens every *context* into a
`ParsedCluster{Name, Server, Auth}` — the context name becomes the upstream name, `cluster.server`
the `BaseURL`, and the user/cluster pair an `upstream.AuthConfig` (`K8sAuth` ∈ `token|client-cert|
exec`). It decodes the base64 `*-data` fields and resolves file-path refs (`certificate-authority`,
`client-certificate`, `client-key`, `tokenFile`) relative to `baseDir`. A context that can't be
turned into a usable target is skipped with a warning — never a whole-file error. This keeps the
"no client-go" decision from ADR-0008 consistent across parsing *and* path/transport.

**Idempotent importer, run on unlock + on demand.** `internal/k8s.Importer.Import(paths)` reads
each existing path, parses it, and `CreateKind`s every context whose name is not already an
upstream (skip-existing — never mutates an existing one). It requires the vault unlocked (Create
encrypts the auth); a locked vault yields a wrapped `secret.ErrLocked`. `POST /clusters/import`
drives it manually and returns `{added, skipped}`. `hVaultUnlock` calls it **best-effort** after a
successful unlock so clusters auto-appear — any error is logged and swallowed, never failing the
unlock. `DiscoverKubeconfigPaths()` returns `$KUBECONFIG` (split on the OS list separator) else
`<home>/.kube/config`; missing paths are skipped silently.

**`insecure-skip-tls-verify` is operator-explicit only.** A new `AuthConfig.K8sInsecureSkipVerify`
is set **only** when the operator's own kubeconfig carried an explicit `insecure-skip-tls-verify:
true` for that cluster — outwall mirrors the trust decision they already made, exactly as kubectl
does. It is never a default, never settable by an agent, and never used to paper over a CA error.
If a context has **both** a CA and the insecure flag, the CA wins and the flag stays false. When
the flag is on, the importer emits a prominent `slog.Warn` and the Clusters UI shows a red
**"insecure"** badge. `authn.k8sTransport` honors it by building `tls.Config{InsecureSkipVerify:
true}` (and skipping the CA-pool requirement) — only in that one branch.

**Clusters UI + HTTP/k8s split.** A new `web/src/pages/Clusters.tsx` lists kind=k8s upstreams
(name + insecure badge, API URL, auth type), with an "Add cluster" form (token/client-cert/exec),
an "Import from kubeconfig" button, a per-cluster "Kubeconfig" action (pick agent → paste token →
`POST /kubeconfig` → show/download YAML), and delete. The HTTP **Upstreams** screen filters to
`kind!=="k8s"` so the two never double-list. `GET /upstreams` additionally surfaces the non-secret
`k8s_auth`/`k8s_insecure` metadata the screen needs.

**The `<select>` fix.** `color-scheme: dark` on `:root`/`body` in `index.css` (plus an explicit
`option` background) makes WebKitGTK render native controls dark. Root cause, one declaration.

## Alternatives considered

- **`k8s.io/client-go`'s `clientcmd` to load kubeconfig** — rejected: a very large dependency for
  a small, stable YAML schema; contradicts ADR-0008's no-client-go stance. yaml.v3 (already a dep)
  is enough.
- **Import on daemon start instead of on unlock** — rejected: the vault is locked at start, so
  Create can't encrypt the auth. Unlock is the first moment import can succeed; tying it there
  also means the clusters appear exactly when the operator opens the console.
- **Refuse `insecure-skip-tls-verify` entirely** — rejected: it would make outwall unable to front
  the kind/minikube clusters developers actually use, and the operator already accepted that trust
  in their kubeconfig. We honor it narrowly, loudly, and never as a default (CA always wins).
- **Mutate/update existing clusters on re-import** — rejected for now: skip-existing is predictable
  and avoids silently overwriting an operator's hand-tuned cluster. Re-import after a delete
  re-adds it (the caveat below).

## Consequences

- The operator's clusters appear automatically on unlock; a manual re-import picks up newly-added
  kubeconfig contexts. Re-import is idempotent (skip-existing).
- **Caveat:** because import is skip-existing keyed on the context *name*, deleting a cluster and
  then unlocking/importing again **re-adds** it from the kubeconfig. That is intended (the
  kubeconfig is the source of truth) but worth knowing — to keep a cluster gone, remove it from
  the kubeconfig too.
- A cluster registered insecure is functional but unverified; the red badge + the import-time
  `slog.Warn` make that visible. The CA path remains strongly preferred.
- **Alpha schema change:** `AuthConfig` gained `K8sInsecureSkipVerify` (a new JSON field inside
  the already-encrypted `auth_config` blob). No migration is needed — it defaults to false and old
  rows simply lack it; no DB reset required.
- A future revisit (update-on-reimport, or pruning clusters whose context disappeared from the
  kubeconfig) would touch `internal/k8s.Importer` and the unlock hook only.
