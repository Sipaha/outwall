# K8s Gateway — Plan K5 (kubeconfig import fixes: toast, scan-all-files, file picker) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Fix the three kubeconfig-import problems found in real use: (1) the "Failed to import
clusters" toast that fires even though import succeeds; (2) only 1 of the user's 3 clusters is
imported because we read only `~/.kube/config`; (3) the "Import from kubeconfig" button should let
the user **pick a kubeconfig file**, not silently re-scan a fixed path.

**Root causes (already investigated — do not re-debug, implement the fixes):**
1. Backend returns `{"added":null,...}` (Go encodes a nil slice as JSON `null`); the UI does
   `res.added.length` (`web/src/pages/Clusters.tsx:119`) → `null.length` throws → caught → generic
   error toast. The import actually returned HTTP 200.
2. `k8s.DiscoverKubeconfigPaths()` returns only `$KUBECONFIG` else `~/.kube/config`. The user's other
   clusters live in sibling files (`~/.kube/citeck-prod-cluster.yaml`, `~/.kube/cloud-clan.conf`),
   each a separate kubeconfig with its own context — Lens aggregates all files in `~/.kube/`, we don't.
3. The button calls `importClusters()` (auto-scan) with no file selection.

**Architecture:** Keep clusters = `Kind="k8s"` upstreams + the existing `Importer`/`ParseKubeconfig`.
Change discovery to scan **all** kubeconfig files in `~/.kube/` (+ `$KUBECONFIG`); add an
import-from-uploaded-content path used by a file-picker button; make the import response a non-nil
list and null-guard the UI.

**Tech Stack:** Go 1.26, `yaml.v3` (no client-go), React 19, Vitest. No new deps.

## Global Constraints

- Module path `github.com/Sipaha/outwall` exact. **No `citeck`** in code/commits (the user's cluster
  names contain "citeck" — that is their data in the DB, never hardcode such strings in source).
- **No CGO** server (`CGO_ENABLED=0`); SQLite `modernc.org/sqlite`; **no `k8s.io/client-go`**.
- **No panics / `log.Fatal`** in lib code — wrapped errors. Panics only in `main`/tests.
- No new deps; don't bump existing. **No `Co-Authored-By`** / no amend. One commit per task.
  Author `Sipaha <sipahabk@gmail.com>`.
- TDD; no flaky tests (`t.TempDir()`, per-test state). Tests assert real behaviour.
- **Don't** edit `docs/INDEX.md` / `docs/roadmap/current-phase.md`. After `make build`/`build-desktop`
  restore `internal/daemon/webdist/index.html`.

---

### Task 1: import response is a non-nil list + UI null-guard (the false-error toast)

**Files:** Modify `internal/daemon/admin.go` (`hClustersImport`) and/or `internal/k8s/import.go` so
`added`/`skipped` are always non-nil (`[]string{}`); Modify `web/src/pages/Clusters.tsx` (`runImport`)
to null-guard. Test: `internal/daemon/admin_test.go`, `web/src/pages/Clusters.test.tsx`.

**Behavior:** `POST /clusters/import` returns `{"added":[],"skipped":[...]}` (never `null`). The UI
shows `Imported clusters — added N, skipped M` using `(res.added ?? []).length` / `(res.skipped ?? []).length`.

- [ ] **Step 1:** Failing Go test: `hClustersImport` when everything already exists returns a JSON
  body whose `added` is `[]` (not `null`) — `require.Equal(t, json.RawMessage(...))` or unmarshal into
  `struct{Added []string; Skipped []string}` and assert `NotNil(res.Added)`. Failing Vitest: mock the
  import call resolving `{added: null, skipped: ['x']}`; clicking import shows a **success** toast
  ("added 0, skipped 1"), NOT "Failed to import clusters".
- [ ] **Step 2:** Run both → FAIL (Go returns null; UI throws on null.length).
- [ ] **Step 3:** Coerce nil→`[]string{}` in the handler (and have `Importer` init `added/skipped`
  to non-nil), and null-guard the toast in `runImport`.
- [ ] **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit `fix(k8s): import returns [] not null + UI null-guard (false error toast)`.

---

### Task 2: auto-import scans ALL kubeconfig files in ~/.kube (not just config)

**Files:** Modify `internal/k8s/kubeconfigimport.go` (`DiscoverKubeconfigPaths` + a testable helper);
`internal/k8s/import.go` (skip files that don't parse as a kubeconfig, with a warning, never error).
Test: `internal/k8s/kubeconfigimport_test.go` / `import_test.go`.

**Behavior:** discovery now returns: every entry of `$KUBECONFIG` (if set) **plus** every regular file
directly in `<home>/.kube/` (skip subdirectories like `cache`/`http-cache`), de-duplicated. The
importer attempts each; a file that doesn't parse as a kubeconfig (no `clusters`/`contexts`, or a YAML
error) is **skipped with a warn**, not fatal. Standard kubectl precedence note: when `$KUBECONFIG` is
set kubectl uses only those — but for outwall's "pull everything from ~/.kube" UX we additionally scan
the dir; document this in the ADR as a deliberate divergence.

Add a testable seam:
```go
// discoverKubeconfigPathsIn returns $KUBECONFIG entries (env, may be empty) plus every regular file
// directly under kubeDir, de-duplicated, in a stable order. Exposed for tests; DiscoverKubeconfigPaths
// calls it with <home>/.kube and the real env.
func discoverKubeconfigPathsIn(kubeDir string, kubeconfigEnv string) []string
```

- [ ] **Step 1:** Failing test: a `t.TempDir()` posing as `~/.kube` containing `config` (1 context),
  `extra.yaml` (1 different context), a `cache/` subdir (with a file inside), and `notes.txt`
  (non-kubeconfig); `discoverKubeconfigPathsIn(dir, "")` returns the three regular files (not the
  subdir file); running `Importer.Import(...)` over them registers **both** contexts and skips
  `notes.txt` with no error.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement the dir scan + non-kubeconfig skip.
- [ ] **Step 4:** Run `./internal/k8s/... -race` → PASS.
- [ ] **Step 5:** Commit `feat(k8s): auto-import scans all kubeconfig files in ~/.kube`.

---

### Task 3: "Import from kubeconfig" = file picker (upload content) + import-from-content

**Files:** Modify `internal/k8s/import.go` (add `ImportContent`), `internal/daemon/admin.go`
(`hClustersImport` accepts an optional request body = uploaded kubeconfig bytes), `web/src/lib/api.ts`
(+ `importKubeconfigContent(text)`), `web/src/pages/Clusters.tsx` (hidden `<input type="file">` +
handler). Tests: `internal/k8s/import_test.go`, `internal/daemon/admin_test.go`,
`web/src/pages/Clusters.test.tsx`.

**Interfaces:**
```go
// ImportContent parses one kubeconfig document (uploaded by the operator) and registers its
// contexts idempotently. baseDir resolves any file-path refs ("" = none; managed-cluster configs
// use inline *-data and need no baseDir). Returns non-nil added/skipped.
func (im *Importer) ImportContent(data []byte, baseDir string) (added, skipped []string, err error)
```
- `POST /clusters/import`: if the request body is non-empty → `ImportContent(body, "")`; else →
  `Import(DiscoverKubeconfigPaths())` (the existing auto path, kept for any internal use). Always
  returns non-nil `{added,skipped}`.
- UI: the button triggers a hidden `<input type="file" accept=".yaml,.yml,.conf,.config,*">`; on
  select, read the file text (`file.text()`), POST it via `importKubeconfigContent`, toast
  `added N / skipped M` (null-guarded). Keep `disabled` while importing.

- [ ] **Step 1:** Failing tests: (a) Go `ImportContent` with an inline kubeconfig string registers its
  context(s), idempotent on a second call; (b) `hClustersImport` with a kubeconfig body registers from
  that body (assert the cluster appears) and returns non-nil lists; (c) Vitest: selecting a file on the
  hidden input reads it and calls the content import, then shows the success toast and reloads the list.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement `ImportContent`, the body branch in the handler, the api.ts call, the
  file-input UI. Keep the Darcula/Lens look.
- [ ] **Step 4:** `go test ./... -race` (touched pkgs) + `cd web && pnpm test && pnpm lint` → PASS.
- [ ] **Step 5:** Commit `feat(k8s): import-from-file picker for kubeconfig upload`.

---

### Task 4: ADR + docs + full gate

**Files:** Create `docs/architecture/decisions/0012-kubeconfig-import-scan-and-upload.md` (status
accepted, date 2026-06-18); update `docs/architecture/modules/k8s.md`, `webui.md`. **Don't** edit
`docs/INDEX.md` / `docs/roadmap/current-phase.md`.

- [ ] **Step 1:** Write ADR-0012 (records: import lists are non-nil; ~/.kube full-dir scan as a
  deliberate divergence from kubectl's `$KUBECONFIG`-only precedence; the upload/file-picker path;
  the false-error-toast root cause) + module docs.
- [ ] **Step 2:** Full gate from `outwall/`:
  `make fmt && make vet && go test ./... -race` (file+grep) `&& (cd web && pnpm test && pnpm lint) && make build && make build-desktop` (then restore `internal/daemon/webdist/index.html`). All green.
- [ ] **Step 3:** Commit `docs(k8s): ADR-0012 + module docs for kubeconfig import fixes`.

## Self-Review

- Bug #1 (toast) → Task 1 (non-nil list + null-guard). Bug #2 (1/3 clusters) → Task 2 (scan all
  ~/.kube files). UX #3 (file picker) → Task 3. All three root causes from the investigation are
  addressed; idempotency preserved; no client-go; the non-kubeconfig-file skip avoids importing junk.
