# ADR-0013: Desktop single-instance + focus-existing (flock + socket hand-off)

- **Status:** accepted
- **Date:** 2026-06-18

## Context

outwall ships as a single-user **desktop** app (ADR-0007): one Wails window with the daemon
running in-process, binding fixed loopback ports (UI `127.0.0.1:8182`, data plane, MCP) and a
unix admin socket. If the operator launches it twice — double-clicking the icon, a second
tray/shortcut — the second process would try to bind the same ports and fail with "address
already in use", or (worse) two daemons would fight over the same SQLite vault.

We want the proven behavior: **only one instance runs; a second launch raises the already-running
window ("come to focus") and exits cleanly.** Two forces shape the design:

1. The gate must run **before the daemon binds any port or the socket**, so a second launch never
   hits a bind conflict.
2. The lock + the focus hand-off should be **CGO-free** so they are covered by the standard
   `go test ./...` gate (the daemon and `internal/desktop` are CGO-free; only the GTK window-raise
   needs CGO). The server binary must stay CGO-free (ADR-0007).

## Decision

Mirror citeck-launcher's proven pattern (a flock lock file + a focus hand-off over the app's own
unix socket), **not** Wails v3's native `SingleInstanceOptions`.

- **flock lock file** (`$HOME/.spk/outwall/desktop.lock`, `LOCK_EX|LOCK_NB`) is the primary-instance
  gate. flock is released automatically when the process dies, so it is crash-safe with no
  stale-lock bookkeeping. `internal/desktop.AcquireInstanceLock(lockPath, socketPath)`
  (`instance_unix.go`, `//go:build !windows`).
- **Focus hand-off over the existing admin socket.** On a lock conflict, `AcquireInstanceLock`
  calls `NotifyExistingInstance(socketPath)` (`focus.go`) — a unix `DialContext` HTTP client with a
  2 s total timeout that does `POST /desktop/focus`. If it answers 2xx, `AcquireInstanceLock`
  returns the sentinel `ErrFocusedExisting`; the caller (`cmd/outwall-desktop/main.go`) does
  `errors.Is(err, ErrFocusedExisting)` → `os.Exit(0)`. If the socket does **not** answer (lock held
  but no live daemon — a stale lock) it returns a wrapped error instead of silently exiting.
  **`os.Exit` is the caller's job**, which keeps the lock/notify logic unit-testable.
- **The running instance serves the focus.** `daemon.Config.OnFocusRequest func()` plus a
  `POST /desktop/focus` route registered in `apiMux()` (so it is reachable over the CSRF-free unix
  socket; the second launcher sends no CSRF header). Nil `OnFocusRequest` (a headless `serve`)
  answers `503` — "no window to focus" — which the second launcher reads as a stale lock. The
  desktop wrapper sets `OnFocusRequest: func(){ application.InvokeAsync(raiseToFront) }`.
- **Ordering: lock gate FIRST.** `run()` acquires the lock *before* `desktop.Run` binds any port
  or the socket. Sequence: resolve `dir` → `AcquireInstanceLock` (→ `os.Exit(0)` on
  `ErrFocusedExisting`, else `defer lock.Release()`) → `desktop.Run(daemon.Config{…,
  OnFocusRequest})` → `application.New` → capture `mainWindow` from `NewWithOptions` → `app.Run()`.
- **GTK focus-stealing workaround + UI-thread rule** (`cmd/outwall-desktop/focus.go`,
  `//go:build desktop`). On Linux a naive `Show()+Focus()` (`gtk_window_present`) is denied by the
  WM's focus-stealing prevention — it blinks the taskbar instead of raising. `raiseWindow` calls
  `Show()`, then on Linux pins `SetAlwaysOnTop(true)+Focus()` and drops the pin ~700 ms later via
  `application.InvokeAsync` (dropping it on the next UI tick races GTK's async map+restack and
  leaves the window behind). **Every Wails window call runs on the UI thread via
  `application.InvokeAsync` — calling them off-thread deadlocks GTK** — so `OnFocusRequest`
  marshals through `InvokeAsync`.

## Alternatives considered

- **Wails v3 native `SingleInstanceOptions`** — rejected. Wails v3 is pre-stable (alpha2.103); its
  native single-instance + GTK focus path is unreliable (the launcher team hit this and built the
  custom path deliberately). The flock + socket approach reuses outwall's existing admin socket,
  is crash-safe via flock, and — critically — keeps the lock/notify/route CGO-free so they get real
  unit tests in the standard gate. Only the GTK raise is desktop-tagged.
- **Bind a second lock TCP port instead of flock** — rejected. A port bind is not crash-safe in the
  same clean way (TIME_WAIT, and it conflates the gate with the daemon's own binds) and adds nothing
  over a file lock for a local single-user app.
- **`os.Exit` inside `AcquireInstanceLock`** (as the launcher does) — rejected for outwall. Exiting
  inside the library makes it untestable; we return `ErrFocusedExisting` and exit in `main`.

## Consequences

- A second launch raises the running window and exits 0; no port-bind conflict, no vault contention.
  Proven by `cmd/outwall-desktop/smoke_single_instance.sh` under xvfb (instance #2 exits 0, instance
  #1 survives, no "address already in use").
- The lock, the socket-notify, and the `/desktop/focus` route are CGO-free and unit-tested in the
  normal `go test ./...` gate (`internal/desktop`, `internal/daemon`). Only `raiseWindow` is
  desktop-tagged, so the server binary and test gate stay CGO-free (ADR-0007).
- A **stale lock** (lock file held by a wrapper whose daemon is gone, or a leftover from `kill -9`)
  surfaces as an error rather than a silent exit — the operator sees it instead of a window that
  never appears. In practice flock releases on death so this is rare.
- No new dependencies — the hand-off rides outwall's existing unix admin socket (no dbus).
- `instance_unix.go` is `//go:build !windows`. Windows single-instance is out of scope for now
  (outwall targets a Linux/macOS desktop); a future Windows port adds an `instance_windows.go`.
