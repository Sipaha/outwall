# ADR-0012: Kubeconfig import — non-nil lists, full ~/.kube scan, and file-picker upload (Plan K5)

- **Status:** accepted
- **Date:** 2026-06-18

## Context

K4 (ADR-0011) shipped the Clusters UI and a kubeconfig auto-importer. Real-world use surfaced
three concrete problems, each already root-caused before this plan:

1. **False-error toast.** `POST /clusters/import` returned `{"added":null,...}` whenever nothing
   was added — a Go nil slice encodes to JSON `null`. The UI did `res.added.length`, which throws
   on `null`; the throw was caught and turned into a generic **"Failed to import clusters"** toast,
   even though the import had succeeded with HTTP 200. The operator saw a red error after a
   successful import.

2. **Only 1 of 3 clusters imported.** `DiscoverKubeconfigPaths()` returned `$KUBECONFIG` else only
   `<home>/.kube/config`. The operator's other clusters lived in sibling files in `~/.kube/`
   (each its own kubeconfig with its own context). Tools like Lens aggregate every file in
   `~/.kube/`; outwall read just one, so the other clusters never appeared.

3. **Import button re-scanned a fixed path.** "Import from kubeconfig" silently ran the auto-scan
   with no way for the operator to pick a specific kubeconfig file to ingest.

## Decision

**Import lists are always non-nil.** `Importer.Import` and the new `ImportContent` initialise
`added, skipped = []string{}, []string{}` before returning, so the handler always emits
`{"added":[],...}` not `{"added":null,...}`. The UI additionally null-guards with
`(res.added ?? []).length` — defence in depth so any future null can never resurrect the false
toast. (Root cause is the backend; the UI guard is belt-and-braces.)

**`~/.kube` is scanned in full — a deliberate divergence from kubectl.** `DiscoverKubeconfigPaths`
now returns the `$KUBECONFIG` entries (when set) **plus every regular file directly under
`<home>/.kube/`**, de-duplicated, with subdirectories (`cache`, `http-cache`, …) skipped. kubectl
uses **only** `$KUBECONFIG` when it is set; outwall additionally scans the directory so the
operator's clusters spread across sibling files all import. This is a UX choice ("pull everything
from `~/.kube`", like Lens), recorded here as a deliberate deviation, not an oversight. The seam
`discoverKubeconfigPathsIn(kubeDir, kubeconfigEnv)` makes it testable without touching `$HOME`.
The importer **skips a discovered file that is not a kubeconfig** (a YAML/schema parse error → a
`slog.Warn`, never fatal), so junk like `notes.txt` in `~/.kube` is harmless.

**A file-picker upload path.** `Importer.ImportContent(data, baseDir)` parses one operator-uploaded
kubeconfig document and registers its contexts idempotently, sharing the skip-existing core
(`register`) with `Import`. `POST /clusters/import` reads the request body (capped at 1 MiB): a
**non-empty** body is imported via `ImportContent(body, "")`; an empty body falls back to the
auto-discover scan. Unlike the auto-scan, an explicitly-uploaded non-kubeconfig is a **real 400
error** — the operator chose that file deliberately. The Clusters "Import from kubeconfig" button
now opens a hidden `<input type="file">`; on select the UI reads `file.text()`, posts it via
`importKubeconfigContent`, and toasts `added N / skipped M`.

The kubeconfig parsing (`yaml.v3`, **no `k8s.io/client-go`**), the idempotent skip-existing keyed
on context name, and the insecure-TLS handling from ADR-0011 are all unchanged.

## Alternatives considered

- **Fix only the UI null-guard, leave the backend returning `null`.** Rejected: the root cause is
  the backend's nil slice; the API contract should be a list, not nullable. We fix both (backend
  is the real fix; the UI guard is defence in depth).
- **Mirror kubectl exactly (`$KUBECONFIG`-only when set).** Rejected: it reproduces the "only one
  cluster" bug for operators whose clusters live in sibling `~/.kube` files. The whole point is to
  aggregate them; the divergence is intentional and documented.
- **A new dedicated upload endpoint (e.g. `POST /clusters/import-file`).** Rejected: overlapping
  with `POST /clusters/import`. Branching on body-present keeps one route and one UI button.
- **Recurse into `~/.kube` subdirectories.** Rejected: `cache`/`http-cache` hold non-kubeconfig
  data; scanning only the top level matches how the files are actually laid out and avoids reading
  large cache trees.

## Consequences

- All of the operator's `~/.kube` clusters import on unlock / on demand; a successful import never
  shows a false error toast; the operator can also upload an arbitrary kubeconfig from disk.
- **Test isolation:** because discovery now reads the whole `<home>/.kube`, daemon tests set
  `HOME` to a temp dir (not just `KUBECONFIG`) to keep auto-import a deterministic no-op.
- A non-kubeconfig file in `~/.kube` is silently skipped on auto-scan but rejected (400) on
  explicit upload — the asymmetry is intentional (implicit discovery vs. an explicit operator
  choice).
- The 1 MiB upload cap is far above any real kubeconfig; a larger document is rejected.
- A future revisit (recursive scan, update-on-reimport, or pruning clusters whose context
  disappeared) would touch `internal/k8s.Importer` / `discoverKubeconfigPathsIn` and the unlock
  hook only.
