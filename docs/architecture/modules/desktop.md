# module: internal/desktop

The CGO-free, Wails-free seam between the Wails GUI shell (`cmd/outwall-desktop`) and the
daemon. It runs the outwall daemon **in-process** (not as a child process — see ADR-0007) and
exposes a tiny lifecycle handle the GUI drives. Because this package has **no build tag and no
CGO/Wails import**, it is covered by the normal `go test ./...` gate and is unit-tested directly.

## Public API

- `Run(cfg daemon.Config) (*Handle, error)` — builds `daemon.New(cfg)`, starts `Serve(ctx)` in a
  goroutine, and polls `http://<cfg.UIListen>/` until it answers `200` (timeout 10 s, poll every
  50 ms). If `Serve` returns early (e.g. a bind failure), that error is surfaced immediately
  rather than waiting out the full timeout. On any failure `Run` tears the half-started daemon
  back down before returning, so it never leaks the `Serve` goroutine or the open store. An empty
  `cfg.UIListen` defaults to `daemon.DefaultUIListen`.
- `type Handle struct { UIURL string; ... }` — `UIURL` is `http://<cfg.UIListen>/`, the address
  the webview loads.
- `(*Handle).Stop(ctx context.Context) error` — cancels the daemon ctx, waits (bounded by `ctx`)
  for `Serve` to return, then `Close`s the store. Idempotent and nil-safe, so it can run from
  both `OnShutdown` and a deferred guard.

### Single-instance gate (ADR-0013)

- `AcquireInstanceLock(lockPath, socketPath string) (*InstanceLock, error)` (`instance_unix.go`,
  `//go:build !windows`) — flocks `lockPath` (`LOCK_EX|LOCK_NB`; flock auto-releases on process
  death, so it is crash-safe with no stale-lock bookkeeping). On a lock conflict it posts to the
  running instance's focus socket: if answered it returns `ErrFocusedExisting` (the caller exits
  0 — the running window was raised); if unanswered (stale lock, no live daemon) it returns a
  wrapped error. **`os.Exit` is the caller's job** so this stays unit-testable.
- `ErrFocusedExisting` — sentinel; the wrapper does `errors.Is(err, ErrFocusedExisting)` →
  `os.Exit(0)`.
- `(*InstanceLock).Release()` — unlocks and removes the lock file; nil-safe.
- `NotifyExistingInstance(socketPath string) error` (`focus.go`) — dials the unix admin socket
  (2 s total timeout) and `POST /desktop/focus`; nil on 2xx, error otherwise.
- The daemon side is `daemon.Config.OnFocusRequest func()` + the `POST /desktop/focus` admin
  route (registered in `apiMux()`, so it is reachable CSRF-free over the unix socket). The
  wrapper sets `OnFocusRequest: func(){ application.InvokeAsync(raiseToFront) }`.
- **Ordering:** the wrapper's `run()` calls `AcquireInstanceLock` **first**, before
  `desktop.Run` binds any port or the unix socket — so a second launch hands focus off and exits
  0 with no "address already in use". `cmd/outwall-desktop/smoke_single_instance.sh` proves this
  under xvfb (instance #2 exits 0, instance #1 survives, no port-bind error).

## Notes

- `daemon.Serve` already tolerates `127.0.0.1:0` on the data-plane and MCP binds, so the runner
  can let those ports be OS-assigned and poll only the fixed `UIListen`. No change to `Serve` was
  needed.
- The GUI shell (`cmd/outwall-desktop`, `//go:build desktop`) is the only CGO + GTK/WebKit
  target; this package stays out of that toolchain so the server binary and the test gate remain
  CGO-free. See ADR-0007.
- The GTK window-raise (`cmd/outwall-desktop/focus.go`, `//go:build desktop`) is the one piece
  that must be desktop-tagged: on Linux a naive `Show()+Focus()` (gtk_window_present) is denied
  by the WM's focus-stealing prevention, so `raiseWindow` pins `SetAlwaysOnTop(true)+Focus()` and
  drops the pin ~700 ms later via `application.InvokeAsync`. **All Wails window calls run on the
  UI thread via `application.InvokeAsync` — calling them off-thread deadlocks GTK.** See ADR-0013.
