# outwall — Plan 6A: Web UI Foundation (dark console) + Unlock + Dashboard

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development or superpowers:executing-plans.

**Goal:** Stand up the embedded React UI in the citeck-launcher dark "developer console" style:
the app shell (sidebar + dark Darcula/Lens theme), the typed API client (`X-Outwall-CSRF`,
`/api` base), the SSE event store, the daemon static-serve + `go:embed` wiring, and the first two
screens — **Unlock** (master password) and **Dashboard** (agents table + live approval queue via
SSE). Remaining screens are Plan 6B.

**Architecture:** `web/` is a Vite + React 19 + TypeScript + Tailwind 4 + Zustand app that builds
straight into `internal/daemon/webdist` (`//go:embed all:webdist`). The daemon's `UIListen` TCP
bind serves the static UI at `/` and the control API under `/api/**` (CSRF-gated, via
`StripPrefix`), with SSE at `/api/events`. Mirror the launcher's web conventions
(`~/.spk/spk-editor/solutions/citeck-launcher/citeck-launcher/web`) for component/style idiom —
**but no citeck strings/branding** (it's `X-Outwall-CSRF`, title "outwall", etc.).

**Tech Stack:** React 19, Vite, TypeScript, Tailwind CSS 4 (`@tailwindcss/vite`), Zustand,
lucide-react, react-router. Vitest + Testing Library for unit tests. **Add npm deps at their
latest versions** (user directive) — `pnpm add` resolves latest by default; don't pin to old.

## Global Constraints

(All prior Go constraints apply to the daemon changes.) Plus:
- The daemon serves: `/api/**` → CSRF middleware → `StripPrefix("/api")` → API mux (admin routes
  + `/events`); `/**` → embedded static (SPA: unknown paths fall back to `index.html`). Static
  assets are NOT CSRF-gated; only `/api/**` is.
- Reuse the launcher's Darcula/Lens `@theme` token palette **verbatim** (values below). No citeck.
- `internal/daemon/webdist/` must contain a committed `index.html` placeholder so `go build`/`go
  test` compile before any `pnpm build`. The real bundle (written by `pnpm build`, `emptyOutDir`)
  overwrites it locally; built `assets/` are gitignored.
- Dark theme is the only theme in 6A (no light toggle yet — can come later).

## File Structure

```
Create:  web/package.json, web/vite.config.ts, web/tsconfig*.json, web/eslint.config.js,
         web/index.html, web/src/main.tsx, web/src/index.css (theme tokens),
         web/src/App.tsx (shell: sidebar + routes), web/src/lib/api.ts, web/src/lib/types.ts,
         web/src/lib/events.ts (Zustand SSE store), web/src/lib/api.test.ts,
         web/src/components/{Sidebar,StatusBadge,Modal,Toast,DataTable}.tsx,
         web/src/pages/{Unlock,Dashboard}.tsx,
         internal/daemon/webui.go (embed + static handler),
         internal/daemon/webdist/index.html (placeholder)
Modify:  internal/daemon/admin.go (factor apiMux(); UIHandler serves static + /api), 
         internal/daemon/admin_test.go (routes now under /api), 
         Makefile (build-web target; `make build` runs it first),
         .gitignore (web/node_modules, internal/daemon/webdist/assets/)
```

---

### Task 1: daemon static-serve + `/api` wiring + embed

**Files:** Create `internal/daemon/webui.go`, `internal/daemon/webdist/index.html`; modify `internal/daemon/admin.go`, `internal/daemon/admin_test.go`, `.gitignore`.

**Behavior:**
- `internal/daemon/webdist/index.html` placeholder:
  `<!doctype html><meta charset=utf-8><title>outwall</title><body>outwall UI not built — run <code>make build</code>.</body>`
- `webui.go`:
```go
package daemon

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:webdist
var webdistFS embed.FS

// staticUI serves the embedded SPA: existing files are served directly, unknown
// paths fall back to index.html (client-side routing).
func staticUI() http.Handler {
	sub, err := fs.Sub(webdistFS, "webdist")
	if err != nil {
		// webdist is a compile-time embed; fs.Sub on a constant valid path cannot fail.
		// Serve a 500 rather than panic, to honor the no-panic-in-library rule.
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "ui assets unavailable", http.StatusInternalServerError)
		})
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/" {
			files.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(sub, p[1:]); err != nil {
			http.ServeFileFS(w, r, sub, "index.html") // SPA fallback
			return
		}
		files.ServeHTTP(w, r)
	})
}
```
- In `admin.go`: extract the route registration into `apiMux()` (admin routes **plus**
  `mux.HandleFunc("GET /events", sseHandler(d.bus))`). `AdminHandler()` (unix socket) returns
  `apiMux()` unchanged at root. `UIHandler()` becomes:
```go
func (d *Daemon) UIHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", csrfMiddleware(d.apiMux())))
	mux.Handle("/", staticUI())
	return mux
}
```
- Update `admin_test.go`: the `TestUICSRFGate` (and any UI-mux test) now hits `/api/vault/status`.
  Add a test that `GET /` returns 200 and contains "outwall".

- [ ] **Step 1** write the failing/updated tests. **Step 2** run `go test ./internal/daemon/` →
  FAIL. **Step 3** implement. **Step 4** run → PASS; also `make build` (Go only is fine here).
  **Step 5** commit `feat(daemon): embed + serve web UI, mount API under /api`.

---

### Task 2: web scaffold + theme + api/SSE client

**Files:** all `web/*` config + `src/{main.tsx,index.css,lib/*}`; `Makefile`; `.gitignore`.

**Key files:**
- `web/package.json` — deps (latest): `react`, `react-dom`, `react-router`, `zustand`,
  `lucide-react`, `clsx`, `tailwindcss`, `@tailwindcss/vite`; dev: `vite`, `@vitejs/plugin-react`,
  `typescript`, `vitest`, `jsdom`, `@testing-library/react`, `@testing-library/jest-dom`,
  `eslint`, `@eslint/js`, `typescript-eslint`, `eslint-plugin-react-hooks`. Scripts: `dev`,
  `build` (`tsc -b && vite build`), `test` (`vitest run`), `lint` (`eslint .`).
- `web/vite.config.ts` — like the launcher's: `plugins:[react(), tailwindcss()]`,
  `server.proxy['/api']='http://localhost:8182'` (dev → daemon UIListen),
  `build.outDir:'../internal/daemon/webdist'`, `emptyOutDir:true`, vitest jsdom config.
- `web/index.html` — anti-FOUC dark background script (`document.documentElement.style
  .backgroundColor='#1e1f22'`), title "outwall", `<div id="root">`, `src/main.tsx`.
- `web/src/index.css` — `@import "tailwindcss";` + the **launcher's `@theme` block verbatim**
  (the dark Darcula/Lens palette): `--color-background:#1e1f22; --color-foreground:#dfe1e5;
  --color-muted:#2b2d30; --color-muted-foreground:#9da0a8; --color-card:#2b2d30;
  --color-border:#43454a; --color-primary:#4d9cf6; --color-primary-foreground:#ffffff;
  --color-destructive:#f75464; --color-success:#5fad65; --color-warning:#e8c44d;
  --color-accent:#393b40;` plus status colors (`--color-status-running:#4eb45e;
  --color-status-transient:#e0b53a; --color-status-stalled:#e08740; --color-status-stopped:#7c7f87;`)
  and the JetBrains-Mono `--font-mono` stack. `body{background:var(--color-background);
  color:var(--color-foreground);font:13px/1.4 -apple-system,...}`.
- `web/src/lib/api.ts` — mirror the launcher's `api.ts`: `API_BASE='/api'`, `const CSRF_HEADER =
  { 'X-Outwall-CSRF':'1' }`, `class ApiError extends Error {status; code}`, `fetchWithTimeout`,
  and typed helpers: `getVaultStatus()`, `vaultInit(pw)`, `vaultUnlock(pw)`, `listAgents()`,
  `listUpstreams()`, `createUpstream(...)`, `listRules()`, `createRule(...)`, `deleteRule(id)`,
  `listApprovals()`, `resolveApproval(id, approve)`, `listAccessRequests()`,
  `resolveAccessRequest(id, status)`, `listAudit(limit)`, `getAudit(id)`, `pruneAudit(olderThan)`.
  Each does `fetch(API_BASE+path, {headers:{...CSRF_HEADER,'Content-Type':'application/json'}})`
  and throws `ApiError` on non-2xx (parse `{error}` body).
- `web/src/lib/types.ts` — TS interfaces matching the Go DTOs (Agent, Upstream, Rule, Approval,
  AccessRequest, AuditEntry, AuditBody, VaultStatus, Event).
- `web/src/lib/events.ts` — Zustand store: connects `new EventSource('/api/events')` (CSRF is not
  settable on EventSource, but the daemon's CSRF gate accepts the header OR — since EventSource
  can't send it — **the daemon must exempt `GET /api/events` from CSRF**; adjust `csrfMiddleware`
  to skip the events path, documented as safe: SSE is read-only, same-origin, no state change).
  The store exposes `connected`, `lastEvent`, and a `subscribe(type, handler)` registry; on each
  event it bumps a per-type counter so screens can `useEffect` re-fetch.

  > **Daemon tweak (fold into Task 1 or here):** `csrfMiddleware` skips the CSRF check for
  > `GET …/events` (EventSource cannot set headers). Note it in ADR-0005's consequences.

- `web/src/main.tsx` — React root + `BrowserRouter`.
- `Makefile` — add `build-web: ; pnpm -C web install --frozen-lockfile=false && pnpm -C web build`
  and make `build:` depend on it (Go build embeds the produced `webdist`). Keep a `build-fast`
  that skips the web rebuild.
- `.gitignore` — add `web/node_modules/`, `internal/daemon/webdist/assets/`, `web/dist/`.

- [ ] **Step 1** scaffold + `pnpm -C web install`. **Step 2** `web/src/lib/api.test.ts` — a Vitest
  test that `vaultUnlock` posts to `/api/vault/unlock` with the CSRF header and throws `ApiError`
  on a 401 (mock `fetch`). Run `pnpm -C web test` → it should pass once api.ts exists.
  **Step 3** `pnpm -C web build` → confirm it writes `internal/daemon/webdist/{index.html,assets}`.
  **Step 4** `go test ./internal/daemon/` still green (now embeds the real bundle). **Step 5**
  commit `feat(web): scaffold UI, dark theme, api + SSE client`.

---

### Task 3: app shell + Unlock + Dashboard

**Files:** `web/src/App.tsx`, `web/src/components/{Sidebar,StatusBadge,Modal,Toast,DataTable}.tsx`, `web/src/pages/{Unlock,Dashboard}.tsx`.

**Behavior:**
- `App.tsx` — on mount, `getVaultStatus()`. If `!initialized` → Unlock (init mode); if `locked` →
  Unlock (unlock mode); else render the shell: a left **Sidebar** (outwall wordmark; nav items
  Dashboard / Upstreams / Agents / Rules / Approvals / Audit / Settings with lucide icons — only
  Dashboard is wired in 6A, the rest are routes Plan 6B fills) + a `<main>` with the routed page.
  Connect the SSE store once mounted.
- `Sidebar.tsx` — dark vertical nav, active item highlighted with `--color-primary`, mono
  wordmark, a small connection dot (green when SSE `connected`).
- `StatusBadge.tsx` — pill using the status colors (e.g. agent status `new` → transient amber;
  approval pending → amber; granted/allow → green; denied → destructive).
- `Modal.tsx`, `Toast.tsx` — reusable primitives (mirror launcher style: tinted shadow, card bg).
- `DataTable.tsx` — a compact dense table (mono numerics, hover row, `--color-border` dividers).
- `Unlock.tsx` — centered card; password field; init mode shows "Set master password" + confirm,
  unlock mode shows "Unlock". Calls `vaultInit`/`vaultUnlock`; on success App re-checks status.
  Error → inline destructive text.
- `Dashboard.tsx` — two panels: **Agents** (DataTable: name, status badge, id-short, created) and
  a live **Approval queue** (pending approvals from `listApprovals()`, each with agent/upstream/
  method/path + **Approve**/**Deny** buttons calling `resolveApproval`). Re-fetch both on the SSE
  events `agent.registered`, `approval.enqueued`, `approval.resolved` (subscribe via the events
  store). Empty states: "No agents yet" / "No pending approvals".

- [ ] **Step 1** build the components + pages. **Step 2** a Vitest test: render `<Unlock>` with a
  mocked `vaultUnlock`, type a password, click Unlock, assert the API was called. **Step 3**
  `pnpm -C web test` + `pnpm -C web lint` + `pnpm -C web build` → all green. **Step 4** `go build`
  embeds it; `make build` produces the binary. **Step 5** commit `feat(web): app shell + Unlock + Dashboard`.

---

## Verification (supervisor does the visual smoke)

After the sub-agent finishes, the supervisor: `make build`, run `dist/bin/outwall serve` with the
vault initialized (over the admin socket), then drive a browser (Playwright) to
`http://127.0.0.1:8182/`, screenshot the Unlock screen and the Dashboard (after unlocking +
registering a couple of agents and enqueuing an approval), and assess the dark-console aesthetic
against the launcher reference. Iterate if it looks off.

## Self-Review

- **Spec coverage (6A slice):** embedded dark-console UI served by the daemon ✓; Unlock
  (master-password) screen ✓; Dashboard with agents + **live** approval queue over SSE ✓; typed
  API client with CSRF ✓; SSE store ✓. **Deferred to 6B:** Upstreams (CRUD + secrets), Rules
  editor, Approvals page, Audit (journal + body viewer), Agent detail, Settings.
- **No citeck:** `X-Outwall-CSRF`, title "outwall", own wordmark — verify `grep -rin citeck web/`.
- **Type consistency:** `api.ts` helpers ↔ `types.ts` DTOs ↔ Go admin JSON; `/api` prefix ↔
  daemon `UIHandler` StripPrefix; `/api/events` CSRF-exempt.

## ADR + docs (finalize)

ADR-0006 (implementer writes it): the web UI foundation — Vite→`webdist` `go:embed`, the `/api`
prefix + static SPA serving on `UIListen`, CSRF exemption for SSE, the reused dark theme tokens,
Zustand SSE-driven refresh. Update ADR-0005's CSRF note (events exemption). Module/notes: a short
`docs/architecture/modules/webui.md` describing the web app structure. Don't touch INDEX/current-phase.
