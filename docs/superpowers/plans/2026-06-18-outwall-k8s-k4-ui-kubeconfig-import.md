# K8s Gateway — Plan K4 (Clusters UI + kubeconfig auto-import + dark form controls) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Make the k8s gateway usable end-to-end from the desktop UI: register/list/remove clusters
and view an agent kubeconfig in a dedicated **Clusters** screen, **auto-import clusters from the
host's `~/.kube` config** on vault unlock (plus a manual re-import), and fix the unthemed native
form controls (the light `<select>`).

**Architecture:** Clusters are already `Kind="k8s"` upstreams (K1). This plan adds (1) a one-line
global CSS fix so WebKitGTK renders native controls dark; (2) a stdlib/yaml.v3 kubeconfig **parser**
+ an idempotent **importer** that upserts each kubeconfig context as a cluster, run automatically
after a successful vault unlock and exposed as `POST /clusters/import`; (3) a **Clusters** UI screen
(+ nav) reusing the existing upstream/kubeconfig admin API, with the HTTP **Upstreams** screen now
filtered to `kind=http` so the two don't double-list.

**Tech Stack:** Go 1.26 + `gopkg.in/yaml.v3` (already a dep) for kubeconfig parsing (NO
`k8s.io/client-go`), React 19 / Tailwind 4 / Zustand, `stretchr/testify`, Vitest.

## Global Constraints

- Module path `github.com/Sipaha/outwall` exact. **No `citeck`** anywhere.
- **No CGO** in the server binary (`CGO_ENABLED=0`); SQLite via `modernc.org/sqlite`. **No
  `k8s.io/client-go`** — parse kubeconfig with `yaml.v3` (the schema is small and stable).
- **No panics / `log.Fatal`** in library code — wrapped errors (`%w`). Panics only in `main`/tests.
- New deps at `@latest`; don't bump existing deps. Prefer reusing what exists.
- **No `Co-Authored-By`**; **no `git commit --amend`** / no rewriting commits. One commit per task.
  Author `Sipaha <sipahabk@gmail.com>`.
- **Alpha:** schema changes fine (DB reset), no legacy fallback. Note any reset in the REPORT.
- TDD; no flaky tests (`t.TempDir()`, per-test state, no fixed sleeps). Tests assert real behaviour.
- **DON'T** edit `docs/INDEX.md` / `docs/roadmap/current-phase.md` (supervisor owns them).
- After `make build`/`make build-desktop`, **restore** the rewritten
  `internal/daemon/webdist/index.html` (`git checkout --` it) before committing.

---

### Task 1: dark native form controls (the light `<select>` fix)

**Files:** Modify `web/src/index.css`. Test: manual + a Vitest that asserts the rule exists is overkill;
instead add `color-scheme` and verify via the existing component tests still pass.

**Root cause:** no `color-scheme` is set, so WebKitGTK paints native `<select>` (and its option
popup, scrollbars, checkboxes) with the light UA theme regardless of the Tailwind `bg-muted` class.

- [ ] **Step 1:** In `web/src/index.css`, add `color-scheme: dark;` to the `body` rule (and `:root`),
  so native controls render dark. Optionally also style `option { background: var(--color-muted); }`.
- [ ] **Step 2:** `cd web && pnpm test` (existing tests still green) and `pnpm lint` clean.
- [ ] **Step 3:** Commit `fix(ui): dark color-scheme for native form controls`.

---

### Task 2: kubeconfig parser (`internal/k8s/kubeconfigimport.go`)

**Files:** Create `internal/k8s/kubeconfigimport.go`, `internal/k8s/kubeconfigimport_test.go`.

**Interfaces:**
```go
package k8s
// ParsedCluster is one kubeconfig context flattened into an outwall cluster target.
type ParsedCluster struct {
    Name     string              // the context name
    Server   string              // cluster.server (API URL)
    Auth     upstream.AuthConfig // K8sAuth=token|client-cert|exec (+ CABundle / insecure)
}
// DiscoverKubeconfigPaths returns the kubeconfig files to read: $KUBECONFIG (os.PathListSeparator
// split) if set, else <home>/.kube/config. Missing files are skipped by the caller.
func DiscoverKubeconfigPaths() []string
// ParseKubeconfig flattens every context in one kubeconfig file's bytes into ParsedClusters,
// resolving file-path refs (certificate-authority, client-certificate, client-key, tokenFile)
// relative to baseDir, and base64 *-data fields. Unsupported/empty contexts are skipped with a
// reason in the returned warnings, never an error for the whole file.
func ParseKubeconfig(data []byte, baseDir string) (clusters []ParsedCluster, warnings []string, err error)
```
Map kubeconfig auth → `upstream.AuthConfig`:
- user `token` / `tokenFile` → `K8sAuth="token"`, `Token=...`.
- user `client-certificate(-data)` + `client-key(-data)` → `K8sAuth="client-cert"`, `ClientCert/ClientKey` PEM.
- user `exec` (command/args/env) → `K8sAuth="exec"`, `ExecCommand/ExecArgs/ExecEnv`.
- cluster `certificate-authority-data` (base64) or `certificate-authority` (file) → `CABundle` PEM.
- cluster `insecure-skip-tls-verify: true` → new `AuthConfig.K8sInsecureSkipVerify bool` (Task 3 wires it).

- [ ] **Step 1:** Failing test with an inline multi-context kubeconfig string covering: a `token`
  user, a `client-certificate-data`/`client-key-data` user, and an `exec` user (e.g. aws), each with
  `certificate-authority-data`; assert three `ParsedCluster`s with the right names/servers/auth and
  decoded CA PEM. Add a case: a context referencing a `certificate-authority` **file** under
  `t.TempDir()` resolves to that file's bytes; and an `insecure-skip-tls-verify: true` context sets
  the new flag.
- [ ] **Step 2:** `go test ./internal/k8s/ -run Kubeconfig -v` → FAIL.
- [ ] **Step 3:** Implement the yaml.v3 structs + flattening + path resolution.
- [ ] **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(k8s): parse kubeconfig into cluster targets`.

---

### Task 3: importer + `POST /clusters/import` + auto-import on unlock + insecure transport

**Files:** Create `internal/k8s/import.go` (the registry-upsert importer) + test; Modify
`internal/upstream/registry.go` (add `K8sInsecureSkipVerify bool` to `AuthConfig`),
`internal/authn/k8s.go` (honor the insecure flag in the cluster transport),
`internal/daemon/admin.go` (the endpoint + call it from `hVaultUnlock`), `internal/daemon/daemon.go`
(wire an importer dependency if needed). Tests in the respective `_test.go`.

**Interfaces:**
```go
// internal/k8s
type Importer struct{ Reg *upstream.Registry; Log *slog.Logger }
// Import reads each existing path, parses it, and registers every context whose name is not
// already an upstream (idempotent skip-existing). Returns the names added and skipped. Requires
// the vault unlocked (Create encrypts); a locked vault yields a wrapped error.
func (im *Importer) Import(paths []string) (added, skipped []string, err error)
```
- `POST /clusters/import` → runs `Importer.Import(DiscoverKubeconfigPaths())`, returns `{added,skipped}`.
- In `hVaultUnlock`, after a successful unlock, call the importer **best-effort** (log warn on error,
  never fail the unlock) so clusters auto-appear. Publish an event/log line with the counts.
- `authn` cluster transport: when `K8sInsecureSkipVerify` is set, build `tls.Config{InsecureSkipVerify:true}`
  (and skip CABundle requirement); otherwise unchanged (trust CABundle).
  **SECURITY — insecure is operator-explicit only, never a default:** this flag is set ONLY by an
  explicit `insecure-skip-tls-verify: true` in the operator's OWN kubeconfig (we mirror the trust
  decision they already made for that cluster, exactly as kubectl does). It is never a default, never
  settable by an agent, and never auto-applied to fix a CA error. When it is set, **log a prominent
  `slog.Warn`** ("cluster X registered with TLS verification DISABLED from its kubeconfig") at import,
  and the Clusters UI (Task 4) shows a red **"insecure"** badge on that cluster. Prefer importing the
  CA: if a context has both CA data and the insecure flag, use the CA and ignore the flag.

- [ ] **Step 1:** Failing tests: (a) `Importer.Import` with a temp kubeconfig registers N clusters,
  a second call adds 0 / skips N (idempotent); a locked vault → wrapped error, nothing added.
  (b) `hVaultUnlock` with a `KUBECONFIG` env pointing at a temp file results in the clusters being
  present in `GET /upstreams` (kind=k8s) afterward. (c) `authn` builds an insecure transport when the
  flag is set (assert `TLSClientConfig.InsecureSkipVerify`).
- [ ] **Step 2:** Run the three → FAIL.
- [ ] **Step 3:** Implement importer, endpoint, unlock hook, insecure transport.
- [ ] **Step 4:** Run `./internal/k8s/... ./internal/daemon/... ./internal/authn/...` under `-race` → PASS.
- [ ] **Step 5:** Commit `feat(k8s): kubeconfig auto-import on unlock + manual import endpoint`.

---

### Task 4: Clusters UI screen (+ nav) + filter HTTP Upstreams

**Files:** Create `web/src/pages/Clusters.tsx` + `web/src/pages/Clusters.test.tsx`; Modify
`web/src/App.tsx` (route), `web/src/components/Sidebar.tsx` (nav item), `web/src/pages/Upstreams.tsx`
(filter list to `kind!=="k8s"`), `web/src/lib/types.ts` + `api.ts` (cluster create/import/kubeconfig
calls; an upstream now has `kind`).

**Behavior:** a **Clusters** screen listing kind=k8s targets (name, API URL, auth type); an
"Add cluster" form (name, API URL, CA paste, auth select token|client-cert|exec with the matching
fields — reuse the now-dark `Select`/`FormField`); an **"Import from kubeconfig"** button calling
`POST /clusters/import` and toasting `added/skipped`; a per-cluster **"Kubeconfig"** action that
prompts for an agent + calls `POST /kubeconfig` and shows/downloads the YAML; delete. A cluster
imported with TLS verification disabled shows a red **"insecure"** badge (the `K8sInsecureSkipVerify`
flag). The HTTP **Upstreams** screen filters out kind=k8s so clusters live only under Clusters.

- [ ] **Step 1:** Vitest `Clusters.test.tsx`: renders a kind=k8s list from a mocked `GET /upstreams`;
  the "Import from kubeconfig" button calls the import endpoint and shows the result; the add form
  shows exec fields when auth=exec. Also assert `Upstreams` no longer lists a kind=k8s row.
- [ ] **Step 2:** `pnpm test` → FAIL.
- [ ] **Step 3:** Implement the screen, nav, route, filter, API calls. Keep the Darcula/Lens theme +
  Zustand/api.ts patterns.
- [ ] **Step 4:** `pnpm test` + `pnpm lint` → PASS; `make build-desktop` embeds the rebuilt bundle
  (then restore `webdist/index.html`).
- [ ] **Step 5:** Commit `feat(ui): Clusters screen + kubeconfig import + HTTP/k8s split`.

---

### Task 5: ADR + docs + full gate

**Files:** Create `docs/architecture/decisions/0011-k8s-clusters-ui-kubeconfig-import.md` (status
accepted, date 2026-06-18); update `docs/architecture/modules/k8s.md`, `authn.md`, `upstream.md`,
`webui.md`. **Do NOT** edit `docs/INDEX.md` / `docs/roadmap/current-phase.md`.

- [ ] **Step 1:** Write ADR-0011 (record: kubeconfig parsed with yaml.v3 not client-go; auto-import
  on unlock + idempotent skip-existing + the deleted-then-reimport caveat; insecure-skip-tls-verify
  support; the color-scheme fix; Clusters/Upstreams UI split) + module docs.
- [ ] **Step 2:** Full gate from `outwall/`:
  `make fmt && make vet && go test ./... -race` (capture to file, grep) `&& (cd web && pnpm test && pnpm lint) && make build && make build-desktop` (then restore index.html). All green.
- [ ] **Step 3:** Commit `docs(k8s): ADR-0011 + module docs for Clusters UI + kubeconfig import`.

## Self-Review

- Select bug → Task 1 (root cause: color-scheme). Cluster UI gap → Task 4. kubeconfig auto-import →
  Tasks 2+3 (parser + importer on unlock + manual). Insecure clusters → Task 3.
- Idempotency + locked-vault behaviour explicitly tested (Task 3). Type consistency: `ParsedCluster`,
  `Importer`, `AuthConfig.K8sInsecureSkipVerify`, the `kind` field on the UI upstream type are used
  identically across tasks.
- No `client-go`; kubeconfig schema parsed with yaml.v3 (already a dep).
