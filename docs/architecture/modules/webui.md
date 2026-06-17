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
  `listAgents`, `listUpstreams`/`createUpstream`, `listRules`/`createRule`/`deleteRule`,
  `listApprovals`/`resolveApproval`, `listAccessRequests`/`resolveAccessRequest`,
  `listAudit`/`getAudit`/`pruneAudit`, `vaultLock`).
- `lib/types.ts` — TS interfaces mirroring the Go admin JSON field names (Agent, Upstream, Rule,
  Approval, AccessRequest, AuditEntry/AuditBody/AuditDetail, VaultStatus, OutwallEvent).
- `lib/events.ts` — Zustand store wrapping one `EventSource('/api/events')`. Tracks `connected`
  and a per-event-type counter (`counters['approval.enqueued']`, …) bumped on each event, so a
  screen `useEffect`-refetches on the relevant event instead of polling. `connect()`/`disconnect()`.
- `lib/toast.ts` — Zustand transient-notification store (auto-dismiss after 5s).
- `index.css` — `@import "tailwindcss"` + the Darcula/Lens `@theme` token block (dark-only in
  6A): `--color-background:#1e1f22`, the `--color-status-*` palette, JetBrains-Mono `--font-mono`.
- `components/` — `Sidebar` (wordmark + nav + live SSE connection dot), `StatusBadge` (status →
  color pill), `Modal`, `Toast` (`ToastContainer`), `DataTable` (compact dense table),
  `FormField` (labelled control wrapper + shared themed `fieldControlClass`), `Select` (themed
  native `<select>`), `JsonView` (pretty-prints captured JSON bodies; metadata for binary/absent).
- `pages/` — the full screen set:
  - `Unlock` — init/unlock master-password card (`vaultInit`/`vaultUnlock`).
  - `Dashboard` — Agents table + live Approval queue (refetched on `agent.registered`,
    `approval.enqueued`, `approval.resolved`; Approve/Deny → `resolveApproval`).
  - `Upstreams` — `listUpstreams` table + "Add upstream" modal whose auth form switches
    conditional fields by `<Select>` type (none/static/basic/oidc-client-credentials);
    `createUpstream`. Refetch on `upstream.created`.
  - `Agents` — `listAgents` table; row "Detail" modal shows the agent's rules (filtered
    `listRules`) and access requests (filtered `listAccessRequests`), read-only. Refetch on
    `agent.registered`.
  - `Rules` — `listRules` joined with `listUpstreams`/`listAgents` to render names; columns
    Subject/Upstream/**Match**/Outcome/Rate + Delete (confirm modal → `deleteRule`). The "Add
    rule" modal adapts to the selected upstream's `kind`: an **http** upstream shows
    Method + Path-glob; a **k8s** cluster shows Namespace + Resource + Verb (`<select>` over the
    RBAC verbs) and sends the tuple instead. The Match column likewise renders `ns/resource verb`
    for k8s rules and `method path` for http rules. `createRule`; refetch on `rule.created` (K2).
  - `Approvals` — pending approvals (Approve/Deny → `resolveApproval`) + access-request intents
    (Grant/Deny/Dismiss → `resolveAccessRequest`). The pending **Target** column renders the k8s
    tuple (`namespace / resource` + verb badge) and the masked patch body (`<pre>`) for k8s
    mutating approvals (K2), else `method path`. Refetch on `approval.enqueued`/`.resolved`
    and `access.requested`.
  - `Audit` — `listAudit(200)` table (status colored by class) refetched on `audit.recorded`;
    row "View" → `getAudit(id)` detail modal with meta grid, masked-headers table, and request/
    response body panels via `JsonView`.
  - `Settings` — vault status + Lock vault (`vaultLock` then reload → Unlock screen); audit
    prune control (`pruneAudit` older-than-N-days); a localhost-only daemon note.
- `App.tsx` — on mount `getVaultStatus()`: not-initialized → Unlock(init); locked →
  Unlock(unlock); else the shell (Sidebar + routed `<main>`), connecting the SSE store. All six
  routes (Upstreams/Agents/Rules/Approvals/Audit/Settings) are wired.

## Tests

- `lib/api.test.ts` — `vaultUnlock` posts to `/api/vault/unlock` with the CSRF header; throws
  `ApiError` (status + daemon message) on a 401; GET helpers send the header and parse arrays.
- `pages/Unlock.test.tsx` — typing + submit calls `vaultUnlock`/`vaultInit`; bad password shows
  the daemon error and skips `onDone`; init mode requires a matching confirmation.
- `pages/Upstreams.test.tsx` — rows render from `listUpstreams`; switching the auth `<Select>`
  reveals the conditional fields; submit calls `createUpstream` with the built auth config.
- `pages/Rules.test.tsx` — rows resolve agent/upstream names; the add-rule modal submits
  `createRule` with the default draft; for a `kind:"k8s"` upstream the modal shows
  Namespace/Resource/Verb (and hides Path glob) and submits the tuple.
- `pages/Approvals.test.tsx` — clicking Approve calls `resolveApproval(id, true)`; a k8s
  approval fixture renders the `ns/resource/verb` tuple and the patch body.
- `pages/Audit.test.tsx` — the journal loads; row "View" calls `getAudit(id)` and renders the
  masked header + pretty-printed JSON body.
- `test/setup.ts` polyfills `HTMLDialogElement.showModal/close` (jsdom lacks them) for the
  modal-driven page tests.

Run with `pnpm -C web test` / `lint` / `build`.
