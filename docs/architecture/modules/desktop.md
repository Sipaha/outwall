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

## Notes

- `daemon.Serve` already tolerates `127.0.0.1:0` on the data-plane and MCP binds, so the runner
  can let those ports be OS-assigned and poll only the fixed `UIListen`. No change to `Serve` was
  needed.
- The GUI shell (`cmd/outwall-desktop`, `//go:build desktop`) is the only CGO + GTK/WebKit
  target; this package stays out of that toolchain so the server binary and the test gate remain
  CGO-free. See ADR-0007.
