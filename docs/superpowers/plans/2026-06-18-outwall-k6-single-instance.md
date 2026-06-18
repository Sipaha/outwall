# outwall Desktop — Plan K6 (single-instance + focus-existing) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Ensure only one outwall **desktop** app runs at a time. Launching a second instance must
bring the already-running window to the foreground ("come to focus") and the second instance must
exit cleanly.

**Architecture:** Use Wails v3's built-in single-instance support
(`application.Options.SingleInstance`). On Linux it uses a dbus session-bus lock
(`github.com/godbus/dbus/v5`, already in the module graph via Wails — no new top-level dep). When a
second instance launches, Wails' `application.New` notifies the first instance and calls
`os.Exit(ExitCode)` for the second; the first instance's `OnSecondInstanceLaunch` callback brings its
window to the front. **Critical ordering:** the single-instance gate must run *before* the in-process
daemon binds its loopback ports (8181/8182/8099) — otherwise the second instance fails on
"address already in use" before the gate. So `application.New(...)` must be called **before**
`desktop.Run(...)` in `cmd/outwall-desktop/main.go`.

**Tech Stack:** Go 1.26, Wails v3 (`v3.0.0-alpha2.103`), CGO+GTK4 (desktop build only). No new deps.

## Global Constraints

- Module path `github.com/Sipaha/outwall` exact. **No `citeck`** anywhere.
- The **server** binary stays CGO-free: all changes are under `cmd/outwall-desktop` (`//go:build desktop`).
  Do not add desktop/Wails imports to any non-`desktop`-tagged file.
- **No panics / `log.Fatal`** in library code — `main` may `log.Fatal` (it already does). Return wrapped errors from `run()`.
- No new dependencies; don't bump existing. **No `Co-Authored-By`** / no amend. One commit per task.
  Author `Sipaha <sipahabk@gmail.com>`.
- **Don't** edit `docs/INDEX.md` / `docs/roadmap/current-phase.md`. After `make build-desktop`,
  restore `internal/daemon/webdist/index.html`.

## Reference (read these in the Wails module before coding)

`go list -m -f '{{.Dir}}' github.com/wailsapp/wails/v3` →
- `pkg/application/single_instance.go` — `SingleInstanceOptions{UniqueID, OnSecondInstanceLaunch, ExitCode, ...}`, `SecondInstanceData{Args,WorkingDir,AdditionalData}`.
- `pkg/application/application.go` ~line 181-189 — New() acquires the lock; on `alreadyRunningError` it notifies the first instance and `os.Exit(ExitCode)`.
- `pkg/application/single_instance_linux.go` — confirm `acquire` calls `notify(...)` before returning the already-running error (so the first instance's callback fires).
- `pkg/application/webview_window.go` — `Show() Window`, `UnMinimise()`, `Focus()`, `Restore()`. Check whether these dispatch onto the main thread internally (the `OnSecondInstanceLaunch` callback runs on a listener goroutine — if the window methods are NOT main-thread-safe, dispatch via the app's main-thread invoke helper).

---

### Task 1: `focusWindow` helper + unit test (desktop-tagged)

**Files:** Create `cmd/outwall-desktop/focus.go` (`//go:build desktop`), `cmd/outwall-desktop/focus_test.go` (`//go:build desktop`).

**Interfaces:**
```go
// windowFocuser is the subset of application.Window used to bring a window to the foreground.
type windowFocuser interface {
    Show() application.Window
    UnMinimise()
    Focus()
}
// focusWindow brings an existing window to the foreground: un-hide, un-minimise, focus.
func focusWindow(w windowFocuser)
```
(If the Wails method set differs from the above after reading `webview_window.go`, adjust the interface
to the real signatures — the point is: show + unminimise + focus, all three.)

- [ ] **Step 1:** Failing test `focus_test.go`: a fake `windowFocuser` records calls; `focusWindow(fake)`
  must call `Show`, `UnMinimise`, and `Focus` (assert all three invoked).
- [ ] **Step 2:** `go test -tags desktop ./cmd/outwall-desktop/ -run Focus` → FAIL (undefined).
- [ ] **Step 3:** Implement `focusWindow`.
- [ ] **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit `feat(desktop): focusWindow helper to foreground an existing window`.

---

### Task 2: wire SingleInstance into `application.New` + reorder `run()`

**Files:** Modify `cmd/outwall-desktop/main.go`.

**Behavior — reorder `run()` so the single-instance gate precedes the daemon:**
1. Resolve `dir` (unchanged).
2. Declare `var stopDaemon func()` and `var mainWindow application.Window` up front.
3. `app := application.New(application.Options{ Name, Description, Icon, OnShutdown: func(){ if stopDaemon != nil { stopDaemon() } }, SingleInstance: &application.SingleInstanceOptions{ UniqueID: "com.sipaha.outwall", ExitCode: 0, OnSecondInstanceLaunch: func(application.SecondInstanceData){ if mainWindow != nil { <focus on main thread>(mainWindow) } } } })`.
   - **This is the gate:** if this is the second instance, `New` already `os.Exit(0)`'d here — so the daemon below never runs and the ports are never contended.
   - If `OnSecondInstanceLaunch` runs off the main thread and the window methods aren't main-thread-safe (per the Task-1 reference read), wrap the focus call in the app's main-thread dispatch (e.g. the app/window invoke helper found in the Wails package); otherwise call `focusWindow` directly.
4. Now `h, err := desktop.Run(daemon.Config{...})` (daemon start + port bind) — unchanged config.
5. Assign `stopDaemon = func(){...}` (the existing idempotent body) and `defer stopDaemon()`.
6. `mainWindow = app.Window.NewWithOptions(application.WebviewWindowOptions{... URL: h.UIURL ...})` (capture the returned window).
7. The SIGINT/SIGTERM handler and `app.Run()` — unchanged.

- [ ] **Step 1:** Implement the reorder + SingleInstance options. (No standard unit test — the package
  is CGO+GTK; verification is the build + the integration smoke in Task 3.)
- [ ] **Step 2:** `make build-desktop` compiles (CGO+GTK) → then `git checkout -- internal/daemon/webdist/index.html`.
- [ ] **Step 3:** Commit `feat(desktop): single-instance gate (focus existing, exit second)`.

---

### Task 3: integration smoke (two launches) + ADR + docs + gate

**Files:** Create `cmd/outwall-desktop/singleinstance_smoke_test.sh` (or a Go integration test tagged
`//go:build desktop` that launches the built binary twice) + `docs/architecture/decisions/0013-desktop-single-instance.md` + update `docs/architecture/modules` (the desktop module doc, e.g. `desktop.md`).

**The smoke must prove the user's requirement** (use a throwaway `$HOME`/data dir + `xvfb-run` so it
doesn't touch the real instance or vault):
- launch instance #1 in the background under `xvfb-run` (give it a few seconds to acquire the lock);
- launch instance #2; assert it **exits with code 0** within a short timeout (it did not hang, did not
  crash on a port bind);
- assert instance #1 is **still running** afterward;
- clean up (kill #1).
Pick a temp `HOME` so the dbus UniqueID lock + data dir are isolated; if the CI/headless dbus session
is unavailable, the test must **skip with a clear message** (not fail) — but on this host
(`xvfb-run` + session dbus present) it should run.

- [ ] **Step 1:** Write the smoke (script or tagged Go test). Run it: instance #2 exits 0, instance #1 survives, no port-bind error in #2's output.
- [ ] **Step 2:** Run it → it passes (or skips with a clear reason if no session bus).
- [ ] **Step 3:** Write ADR-0013 (records: Wails native single-instance; the ordering fix — gate before
  daemon port-bind; focus-on-second-launch; Linux dbus lock; ExitCode 0) + the desktop module doc note.
- [ ] **Step 4:** Full gate from `outwall/`:
  `make fmt && make vet && go test ./... -race` (the standard CGO-free gate — must stay green and unaffected)
  `&& go test -tags desktop ./cmd/outwall-desktop/...` (the desktop-tagged unit test)
  `&& make build && make build-desktop` (then restore index.html). All green.
- [ ] **Step 5:** Commit `docs(desktop): ADR-0013 single-instance + integration smoke`.

## Self-Review

- Single-instance + focus + second-exits → Wails `SingleInstanceOptions` (Task 2) + `focusWindow` (Task 1).
- The ordering fix (gate before port bind) is the one non-obvious correctness point — called out in Task 2 and proven by the Task-3 smoke (instance #2 exits 0, no "address already in use").
- Server binary stays CGO-free: all changes are `//go:build desktop`. `go test ./...` unaffected.
- No new top-level dep (godbus already transitive via Wails).
