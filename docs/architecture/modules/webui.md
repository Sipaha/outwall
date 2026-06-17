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
  `listAudit`/`getAudit`/`pruneAudit`).
- `lib/types.ts` — TS interfaces mirroring the Go admin JSON field names (Agent, Upstream, Rule,
  Approval, AccessRequest, AuditEntry/AuditBody/AuditDetail, VaultStatus, OutwallEvent).
- `lib/events.ts` — Zustand store wrapping one `EventSource('/api/events')`. Tracks `connected`
  and a per-event-type counter (`counters['approval.enqueued']`, …) bumped on each event, so a
  screen `useEffect`-refetches on the relevant event instead of polling. `connect()`/`disconnect()`.
- `lib/toast.ts` — Zustand transient-notification store (auto-dismiss after 5s).
- `index.css` — `@import "tailwindcss"` + the Darcula/Lens `@theme` token block (dark-only in
  6A): `--color-background:#1e1f22`, the `--color-status-*` palette, JetBrains-Mono `--font-mono`.
- `components/` — `Sidebar` (wordmark + nav + live SSE connection dot), `StatusBadge` (status →
  color pill), `Modal`, `Toast` (`ToastContainer`), `DataTable` (compact dense table).
- `pages/` — `Unlock` (init/unlock master-password card; calls `vaultInit`/`vaultUnlock`) and
  `Dashboard` (Agents table + live Approval queue refetched on `agent.registered`,
  `approval.enqueued`, `approval.resolved`, with Approve/Deny → `resolveApproval`).
- `App.tsx` — on mount `getVaultStatus()`: not-initialized → Unlock(init); locked →
  Unlock(unlock); else the shell (Sidebar + routed `<main>`), connecting the SSE store. Only
  Dashboard is wired in 6A; Upstreams/Agents/Rules/Approvals/Audit/Settings are placeholder
  routes filled by Plan 6B.

## Tests

- `lib/api.test.ts` — `vaultUnlock` posts to `/api/vault/unlock` with the CSRF header; throws
  `ApiError` (status + daemon message) on a 401; GET helpers send the header and parse arrays.
- `pages/Unlock.test.tsx` — typing + submit calls `vaultUnlock`/`vaultInit`; bad password shows
  the daemon error and skips `onDone`; init mode requires a matching confirmation.

Run with `pnpm -C web test` / `lint` / `build`.
