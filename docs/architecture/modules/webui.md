# module: web (+ internal/daemon/webui.go)

The embedded desktop UI: a Vite + React 19 + TypeScript + Tailwind 4 + Zustand + react-router
app under `web/`, built into `internal/daemon/webdist` and served by the daemon's `UIListen`
bind. See ADR-0006 for the rationale (embed, `/api` prefix, SSE CSRF exemption, reused theme).

## Build + embed

- `web/vite.config.ts` sets `build.outDir = ../internal/daemon/webdist` with `emptyOutDir: true`,
  so `pnpm build` writes the bundle straight into the Go embed location. Dev server proxies
  `/api` → `http://localhost:8182` (the daemon's `UIListen`).
- `internal/daemon/webui.go` embeds it: `//go:embed all:webdist`. `staticUI()` serves existing
  files directly and falls back to `index.html` for unknown paths (SPA client-side routing). If
  `fs.Sub` ever fails it returns a 500 handler rather than panicking (no-panic-in-library rule).
- A committed placeholder `webdist/index.html` lets `go build`/`go test` compile before any web
  build; `webdist/assets/` and `web/node_modules/`, `web/dist/` are gitignored.
- `make build` runs `build-web` (`pnpm -C web install && pnpm -C web build`) then the Go build;
  `make build-fast` skips the web rebuild.

## Frontend structure (`web/src`)

- `lib/api.ts` — typed control-API client. `API_BASE='/api'`, one `X-Outwall-CSRF` header on
  every call, `ApiError {status, message}` thrown on non-2xx (parsing the daemon's `{error}`
  body), `fetchWithTimeout`. One helper per endpoint (`getVaultStatus`, `vaultInit/Unlock`,
  `listAgents`, `listUpstreams`/`createUpstream`/`deleteUpstream`/`setUpstreamAuth` (H3 host
  credential set/replace), `listRules`/`createRule`/`deleteRule`/`setRuleVariablePolicy` (H3 value-set
  add/remove/trust-any), `listApprovals`/`resolveApproval` (H3 takes an optional
  `{ auth?, trust_any? }`), `listAccessRequests`/`resolveAccessRequest`, `listAudit`/`getAudit`/
  `pruneAudit`, `vaultLock`, and the K4 cluster helpers `createCluster`/`importClusters`/`getKubeconfig`).
- `lib/types.ts` — TS interfaces mirroring the Go admin JSON field names (Agent, Upstream — with
  the K4 `k8s_auth`/`k8s_insecure` cluster fields, ClusterAuthConfig, ClusterImportResult, Rule +
  ValuePolicy, Approval — H3 adds the `kind`/`host`/`op_*`/`new_values`/`template` control-plane
  fields plus `OpVariable`/`NewValue`/`ResolveOptions`, AccessRequest,
  AuditEntry/AuditBody/AuditDetail, VaultStatus, OutwallEvent).
- `lib/events.ts` — Zustand store wrapping one `EventSource('/api/events')`. Tracks `connected`
  and a per-event-type counter (`counters['approval.enqueued']`, …) bumped on each event, so a
  screen `useEffect`-refetches on the relevant event instead of polling. `connect()`/`disconnect()`.
- `lib/toast.ts` — Zustand transient-notification store (auto-dismiss after 5s).
- `index.css` — `@import "tailwindcss"` + the Darcula/Lens `@theme` token block (dark-only in
  6A): `--color-background:#1e1f22`, the `--color-status-*` palette, JetBrains-Mono `--font-mono`.
  Sets `color-scheme: dark` on `:root`/`body` (+ an `option` background) so WebKitGTK renders
  native form controls — the `<select>` and its option popup — dark instead of light (ADR-0011).
- `components/` — `Sidebar` (wordmark + nav + live SSE connection dot; H3 labels the
  `/upstreams` route **Hosts** and `/rules` **Operations**), `StatusBadge` (status →
  color pill), `Modal`, `Toast` (`ToastContainer`), `DataTable` (compact dense table),
  `FormField` (labelled control wrapper + shared themed `fieldControlClass`), `Select` (themed
  native `<select>`), `JsonView` (pretty-prints captured JSON bodies; metadata for binary/absent).
- `pages/` — the full screen set:
  - `Unlock` — init/unlock master-password card (`vaultInit`/`vaultUnlock`).
  - `Dashboard` — Agents table + live Approval queue (refetched on `agent.registered`,
    `approval.enqueued`, `approval.resolved`; Approve/Deny → `resolveApproval`).
  - `Upstreams` (**Hosts**, H3) — `listUpstreams` (filtered to `kind!=="k8s"` — clusters live on
    Clusters) table with a **credential-status** column (`credential set (type)` vs `no credential`
    from `auth_type`); per-host **Set/Replace credential** (modal → `setUpstreamAuth`) and **Remove**
    (confirm → `deleteUpstream`); "Add host" modal whose shared `AuthFields` block switches
    conditional fields by `<Select>` type (none/static/basic/oidc-client-credentials);
    `createUpstream`. Refetch on `upstream.created`/`.updated`/`.deleted`.
  - `Clusters` (K4/K5) — lists kind=k8s upstreams (name + red **"insecure"** badge when
    `k8s_insecure`, API URL, auth type). "Add cluster" modal (token/client-cert/exec → `createCluster`
    with `kind:"k8s"`); **"Import from kubeconfig"** opens a hidden `<input type="file">` (K5) — on
    select it reads `file.text()` and posts the body via `importKubeconfigContent`, toasting
    `added N / skipped M` (null-guarded `(res.added ?? []).length`, so an HTTP-200 all-skipped
    import never fires a false "Failed to import" toast); per-cluster "Kubeconfig" → pick an agent,
    paste its token, `getKubeconfig` → show/download YAML; delete → `deleteUpstream`. Refetch on
    `upstream.created`/`.deleted`.
  - `Agents` — `listAgents` table; row "Detail" modal shows the agent's rules (filtered
    `listRules`) and access requests (filtered `listAccessRequests`), read-only. Refetch on
    `agent.registered`.
  - `Rules` (**Operations**, H3) — http operation rules render as cards: the segmented template
    (fixed vs `{name:type}` variable spans) + outcome badge + Delete, and per **text** variable an
    inline **value-set editor** (`ValueSetEditor`) — removable value chips, an add-value input, and a
    "trust any value" toggle; each edit recomputes the whole policy and posts
    `setRuleVariablePolicy(ruleID, var, policy)`. Date variables show "auto (any date)". k8s tuple
    rules keep a separate **Cluster (k8s) rules** table. The "Add operation" modal adapts to the
    selected host's `kind` (http: Method + Path-template + var=value lines; k8s: Namespace/Resource/
    Verb tuple). `createRule`; refetch on `rule.created`/`rule.updated`.
  - `Approvals` (H3) — pending approvals render as **cards keyed on `kind`/shape**: a **host** card
    (agent/host/purpose + inline credential form → `resolveApproval(id, true, { auth })`), an
    **operation** card (segmented shape, a concrete **example URL** from `op_values`, per-text-var
    "trust any value" checkbox, a **broad-placeholder warning** when any var is `any`, Approve posts
    `{ trust_any }`), a **new-value** card (template + new `(var,value)`, Approve / Approve+trust-any),
    and the legacy **plain/k8s** card (method+path, or `ns/resource` + verb badge + masked patch body
    for K2 mutations). Plus access-request intents (Grant/Deny/Dismiss → `resolveAccessRequest`).
    Refetch on `approval.enqueued`/`.resolved` and `access.requested`.
  - `Audit` — `listAudit(200)` table (status colored by class) refetched on `audit.recorded`;
    row "View" → `getAudit(id)` detail modal with meta grid, masked-headers table, and request/
    response body panels via `JsonView`.
  - `Settings` — vault status + Lock vault (`vaultLock` then reload → Unlock screen); audit
    prune control (`pruneAudit` older-than-N-days); a localhost-only daemon note.
- `App.tsx` — on mount `getVaultStatus()`: not-initialized → Unlock(init); locked →
  Unlock(unlock); else the shell (Sidebar + routed `<main>`), connecting the SSE store. The
  routes (Upstreams/**Clusters**/Agents/Rules/Approvals/Audit/Settings) are wired.

## Tests

- `lib/api.test.ts` — `vaultUnlock` posts to `/api/vault/unlock` with the CSRF header; throws
  `ApiError` (status + daemon message) on a 401; GET helpers send the header and parse arrays.
- `pages/Unlock.test.tsx` — typing + submit calls `vaultUnlock`/`vaultInit`; bad password shows
  the daemon error and skips `onDone`; init mode requires a matching confirmation.
- `pages/Upstreams.test.tsx` (Hosts) — rows render credential status (set/none); per-host Set
  credential → `setUpstreamAuth(name, auth)`; Remove → confirm → `deleteUpstream(name)`; the add-host
  modal switches conditional auth fields and submits `createUpstream`.
- `pages/Clusters.test.tsx` — kind=k8s rows render (and the insecure cluster shows the "insecure"
  badge) while an http upstream is filtered out; selecting a file on the hidden import input reads
  it and calls `importKubeconfigContent`, toasting added/skipped; a backend `added:null` still
  toasts success (null-guard), never "Failed to import"; the add form reveals exec fields when
  auth=exec. Also asserts the Upstreams screen no longer lists a kind=k8s row.
- `pages/Rules.test.tsx` (Operations) — an operation template renders with its per-variable
  value-set (text chip + date "auto"); adding a value posts the grown set, removing posts the trimmed
  set, the trust-any toggle posts `mode:"any"` (all via `setRuleVariablePolicy`); the add-operation
  modal submits `createRule`; a `kind:"k8s"` upstream shows the tuple fields and k8s rules stay in
  their own section.
- `pages/Approvals.test.tsx` — the plain Approve calls `resolveApproval(id, true)`; the k8s fixture
  renders the tuple + patch body; the **host** card approves with `{ auth }`; the **operation** card
  shows the example URL, the per-text-var trust-any checkbox, and the broad-placeholder warning on
  toggle, and approves posting `{ trust_any }`; the **new-value** card approves+trust-any with
  `{ trust_any }`.
- `pages/Audit.test.tsx` — the journal loads; row "View" calls `getAudit(id)` and renders the
  masked header + pretty-printed JSON body.
- `test/setup.ts` polyfills `HTMLDialogElement.showModal/close` (jsdom lacks them) for the
  modal-driven page tests.

Run with `pnpm -C web test` / `lint` / `build`.
