# ADR-0006: Web UI foundation (embedded dark console)

- **Status:** accepted
- **Date:** 2026-06-17

## Context

outwall is a local desktop product (Wails 3 wrapper around the Go daemon, Plan 7). The operator
needs a UI to unlock the vault, watch agents register, and resolve approvals live. ADR-0005 gave
the daemon a TCP control surface (`UIListen`, default `127.0.0.1:8182`) carrying the admin API
plus an SSE event stream, gated by the `X-Outwall-CSRF` header. This ADR pins down the frontend
and how the daemon serves it.

Constraints: the daemon ships as a single static binary with no runtime asset dependency (it
must run offline and self-contained — see ADR-0001's CGO-free posture); a browser/webview is the
host. We want the developer-console aesthetic of citeck-launcher's web UI (used purely as a
pattern source — **no citeck strings or branding** may appear in outwall) without re-deriving it.

## Decision

**Vite → `webdist` `go:embed`.** `web/` is a Vite + React 19 + TypeScript + Tailwind 4 + Zustand
+ react-router app. Its `build.outDir` is `../internal/daemon/webdist` with `emptyOutDir: true`,
so `pnpm build` writes the production bundle straight into the Go embed location;
`internal/daemon/webui.go` embeds it with `//go:embed all:webdist`. A committed placeholder
`webdist/index.html` (the only tracked file there; `webdist/assets/` is gitignored) lets
`go build`/`go test` compile before any web build. `make build` runs `build-web` (`pnpm -C web
install && pnpm -C web build`) first, then the Go build; `make build-fast` skips the web rebuild.

**`/api` prefix + SPA static serve on `UIListen`.** `UIHandler()` is now a small mux:
`/api/**` → `StripPrefix("/api")` → `csrfMiddleware` → the shared `apiMux()` (admin routes +
`GET /events`); `/**` → `staticUI()`, the embedded SPA. `staticUI` serves existing files
directly and falls back to `index.html` for unknown paths (client-side routing). The unix-socket
`AdminHandler()` keeps serving `apiMux()` at root (CSRF-free, for the CLI). `staticUI` returns a
500 handler rather than panicking if `fs.Sub` ever fails, honoring the no-panic-in-library rule.

**SSE CSRF exemption.** `csrfMiddleware` skips the CSRF check for `GET /events`. `EventSource`
cannot set custom request headers, so the events stream could never carry `X-Outwall-CSRF`. This
is safe: SSE is read-only (changes no state), same-origin, and served only over the loopback
`UIListen` bind. (Noted as a consequence in ADR-0005.)

**Reused dark theme tokens.** `web/src/index.css` reuses the launcher's Darcula/Lens `@theme`
token values verbatim (`--color-background:#1e1f22`, etc.) plus the `--color-status-*` palette
and the JetBrains-Mono `--font-mono` stack. Dark is the only theme in 6A (no toggle yet).

**Typed API client + Zustand SSE-driven refresh.** `lib/api.ts` mirrors the launcher idiom:
`API_BASE='/api'`, a single `X-Outwall-CSRF` header, an `ApiError {status, message}` thrown on
non-2xx (parsing the daemon's `{error}` body), `fetchWithTimeout`, and one typed helper per
endpoint. `lib/types.ts` mirrors the Go admin JSON field names. `lib/events.ts` is a Zustand
store that opens one `EventSource('/api/events')`, tracks `connected`, and bumps a per-event-type
counter so screens `useEffect`-refetch on the relevant event rather than polling.

## Alternatives considered

- **Serve the UI from disk / a separate static server** — breaks the single-binary, offline,
  self-contained model. Rejected; `go:embed` keeps everything in one artifact.
- **Hash-router to avoid SPA fallback** — uglier URLs; the `index.html` fallback in `staticUI`
  is trivial and keeps clean paths. Rejected.
- **Require CSRF on SSE via a query-param token** — `EventSource` can carry a query param, but it
  leaks into logs/history and adds a token-issuance dance for no gain given the loopback +
  read-only model. Rejected in favor of the documented path exemption.
- **A heavier data/query layer (TanStack Query)** — overkill for the 6A slice; the Zustand
  counter + `useEffect` refetch is enough and matches the launcher. Can revisit later.

## Consequences

- One artifact: `make build` produces a binary with the UI baked in; there is no separate deploy
  step or asset-path configuration.
- The `/api` prefix cleanly separates the control API from the SPA; adding screens in Plan 6B is
  routing + new `api.ts`/`types.ts` entries, no daemon change.
- The placeholder `webdist/index.html` must stay committed; a clean checkout that runs only
  `go build` (no `pnpm build`) still compiles and serves a "UI not built" page rather than
  failing the embed.
- The SSE CSRF exemption is path-specific (`GET /events`); if the events endpoint ever moves or
  gains state-changing behavior, the exemption must be revisited (it is read-only by design).
- Dark-only theming is a deliberate 6A scope cut; a light toggle would reuse the launcher's
  `[data-theme]` overrides if added later.
- Plan 6B fills the remaining screens (Upstreams, Agents, Rules, Approvals, Audit, Agent detail,
  Settings) against the same client + theme + SSE store laid down here.
