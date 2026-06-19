# ADR-0007: Desktop wrapper (Wails v3, in-process daemon)

- **Status:** accepted
- **Date:** 2026-06-17

## Context

outwall ships as a local **desktop** app (see AGENTS.md): a native window that renders the
embedded React UI and runs the egress-gateway daemon so the operator can unlock the vault,
approve requests, and inspect the audit log. Plans 1–6 built the daemon (`cmd/outwall`,
`CGO_ENABLED=0`, pure-Go SQLite) and the embedded web UI it serves over `UIListen`
(`127.0.0.1:8182`, ADR-0005/0006). This plan adds the GUI shell.

Two hard constraints shape the design:

1. **The server binary must stay CGO-free.** A Wails/GTK/WebKit webview needs `CGO_ENABLED=1`
   plus GTK3 + WebKit2GTK at build time. That toolchain cannot leak into the server binary or
   the test gate, both of which are `CGO_ENABLED=0` and must build/run on any host.
2. **Wails v3 is pre-stable** (the resolved version is `v3.0.0-alpha2.103`), so its API is a
   moving target and is the one place we accept churn.

The reference implementation is citeck-launcher's `cmd/citeck-desktop`, which supervises its
daemon as a **child process** — it selects a daemon binary, spawns it, talks to it over a unix
control socket, and respawns it for zero-downtime auto-updates. outwall has none of those
forces: no Docker, no zero-downtime-upgrade contract, no binary-selection/rollback machinery.

## Decision

**Run the daemon in-process.** The Wails shell and the daemon share one OS process.

- `internal/desktop` is a **CGO-free, Wails-free, build-tag-free** seam:
  `desktop.Run(cfg daemon.Config) (*Handle, error)` calls `daemon.New(cfg)`, starts
  `Serve(ctx)` in a goroutine, polls `http://<UIListen>/` until it answers `200` (≤10 s, with a
  fast-fail if `Serve` returns a bind error first), and returns a `Handle{UIURL}` with
  `Stop(ctx) error` (cancel the ctx, wait for `Serve` to return, `Close` the store). Because it
  has no build tag and no CGO/Wails import, it is covered by the normal `go test ./...` gate and
  is fully unit-tested (start daemon → curl UI 200 → stop).
- `cmd/outwall-desktop/main.go` carries **`//go:build desktop`**. The default no-tag gate
  (`go build ./...`, `go vet ./...`, `go test ./...`) skips it, so the server build and the test
  gate stay CGO-free and green. The desktop build is `CGO_ENABLED=1 go build -tags desktop`
  (`make build-desktop`).
- **The webview loads `UIListen` directly.** The resolved Wails v3 `WebviewWindowOptions` has a
  `URL string` field, so the window is pointed straight at `http://127.0.0.1:8182/` — no asset
  handler / reverse proxy is needed. (If a future Wails version drops that field, the fallback is
  an `application.AssetOptions{Handler: <httputil.ReverseProxy to UIListen>}`, mirroring the
  launcher's `loadingHandler`. We did not need it.)
- **Lifecycle:** `application.Options.OnShutdown` and a `SIGINT/SIGTERM → app.Quit()` handler
  both route through Wails' clean shutdown, which fires `OnShutdown → Handle.Stop` (idempotent).
  Routing signals through `app.Quit()` rather than a bare ctx-cancel avoids leaving the Wails
  event loop running and hanging the terminal on Ctrl-C.

## Alternatives considered

- **Child-process daemon supervisor (the launcher's model)** — rejected. It exists to enable
  zero-downtime upgrades and Docker orchestration, neither of which outwall has. The control
  socket / binary selection / respawn machinery would be pure cost with no benefit, and would add
  a second process to manage and a second failure mode (orphaned child).
- **No build tag; guard CGO some other way** — rejected. The build tag is the standard, robust
  way to keep the Wails/GTK import out of the CGO-free server and the test gate. Without it,
  `go build ./...` / `go vet ./...` would try to compile the Wails import and fail (or force CGO
  on the whole tree).
- **Reverse-proxy asset handler instead of the URL field** — not needed, since the resolved API
  has a `URL` field; kept on record as the documented fallback.
- **Pin Wails to the launcher's alpha.98** — rejected per the use-latest-deps rule; we take
  `@latest` and adapt to the resolved API.

## Consequences

- The server binary and the test gate are unaffected by the desktop target — the `desktop`-tagged
  file is invisible to them, and `internal/desktop` has the only daemon-runner logic, fully
  tested CGO-free.
- One process means a daemon crash takes the GUI with it (and vice-versa) — acceptable for a
  single-user local desktop app; there is nothing to keep alive without the window.
- `daemon.Serve` already tolerates `127.0.0.1:0` on the data-plane and MCP binds (Go's
  `ListenAndServe` assigns an ephemeral port), so no change to `Serve` was needed; the runner
  polls only the fixed `UIListen`.
- Wails churn is contained to `cmd/outwall-desktop/main.go`. A future Wails upgrade re-confirms
  the API there (`go doc github.com/wailsapp/wails/v3/pkg/application`) and touches nothing else.
- Resolved dependency: `github.com/wailsapp/wails/v3 v3.0.0-alpha2.103`.

## Addendum (2026-06-19): system tray + minimise-to-close + app icon

The desktop app now lives in the **system tray** and treats the window's close button as
**minimise-to-tray**, not quit — outwall keeps running (data plane, MCP, UI) in the background so
agents stay served while the window is hidden. Pattern mirrors citeck-launcher (the working Wails v3
reference):

- A `WindowClosing` **hook** (`mainWindow.RegisterHook(events.Common.WindowClosing, …)`) calls
  `Hide()` then `e.Cancel()`. Hooks run before listeners in `HandleWindowEvent`, and a cancel stops
  dispatch — so the built-in destroy listener (registered in `NewWithOptions`) is skipped and the
  window only hides. `app.Quit()` is unaffected: `app.cleanup()` nils the window list and calls
  `postQuit()` regardless of the per-window cancel, so **Exit** still quits cleanly (firing
  `OnShutdown` → daemon stop).
- A `SystemTray` (`app.SystemTray.New()`) with the app icon, tooltip, **left-click → raise window**
  (`raiseToFront`, which `Show()`s a hidden window then does the ADR-0013 keep-above raise), and a
  right-click **menu**: *Open* (raise) + *Exit* (`app.Quit()`). Tray callbacks are marshalled onto
  the UI thread via `InvokeAsync` (off-thread Wails calls deadlock GTK). `SetTemplateIcon` on macOS,
  `SetIcon` elsewhere.
- **App icon**: a generated flat mark at `cmd/outwall-desktop/logo.png` (embedded as the window
  `Icon` and the tray icon) — a white shield with a cut-out outward arrow on a teal tile (security +
  egress). Produced with PIL (supersampled vector-style render), legible down to ~22 px.
- **Taskbar/dock icon (Linux/GTK4)**: GTK4 removed `gtk_window_set_icon`, so the embedded window
  `Icon` does NOT drive the taskbar icon — GTK4 resolves it from a `.desktop` file matched to the
  window's app_id (Wayland) / WM_CLASS (X11). On launch the wrapper idempotently installs
  `~/.local/share/icons/hicolor/512x512/apps/org.wails.outwall.png` and
  `~/.local/share/applications/org.wails.outwall.desktop` (`Icon=org.wails.outwall`,
  `StartupWMClass=outwall`) via the CGO-free `desktop.InstallLinuxIntegration`, and sets
  `Options.Linux.ProgramName = "outwall"` so WM_CLASS matches. The tray icon, by contrast, takes the
  PNG directly — that path was already fine.
- **OS notifications on access requests**: the desktop wraps the Wails `notifications` service
  (`application.NewService(notifications.New())`) and subscribes **in-process** to the daemon event
  bus (`daemon.Subscribe` → `desktop.Handle.Subscribe`, returning `events.Bus.Subscribe`). On
  `approval.enqueued` (an agent requested access → operator decision pending) it raises an OS
  notification (`SendNotification`); `OnNotificationResponse` raises the window on click — so the
  operator is prompted even with the window hidden in the tray. The `approval.enqueued` payload
  gained a `host` field for the message. In-process subscription (not the SSE HTTP stream) keeps it
  independent of the webview lifecycle.
