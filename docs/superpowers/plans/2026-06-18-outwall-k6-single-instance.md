# outwall Desktop — Plan K6 (single-instance + focus-existing) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Only one outwall **desktop** app runs at a time. Launching a second instance brings the
already-running window to the foreground ("come to focus") and the second instance exits cleanly.

**Architecture (mirrors citeck-launcher's proven pattern, NOT Wails' native SingleInstance):**
- A **flock lock file** (`$HOME/.spk/outwall/desktop.lock`, `LOCK_EX|LOCK_NB`) is the primary-instance
  gate. flock auto-releases on process death (crash-safe — no stale-lock bookkeeping).
- On a lock conflict the second instance **notifies the running instance over outwall's existing unix
  admin socket** (`POST /desktop/focus` on `outwall.sock`) and, if the running instance answers 2xx,
  `os.Exit(0)`. If the socket does not answer (lock held but no live daemon) it surfaces an error
  instead of silently exiting.
- The running instance serves `POST /desktop/focus` from its admin mux; the route calls a registered
  `Config.OnFocusRequest func()` which the desktop wrapper sets to raise + focus the Wails window.
- **Linux/GTK focus-stealing workaround (the key gotcha):** `gtk_window_present` (Wails `Focus()`) is
  denied by the WM's focus-stealing prevention, so a naive `Show()+Focus()` blinks the taskbar instead
  of raising. We `Show()`, then on Linux pin `SetAlwaysOnTop(true)` + `Focus()`, and drop the pin after
  ~700 ms via `application.InvokeAsync`. **All Wails window calls must run on the UI thread via
  `application.InvokeAsync` — calling them off-thread deadlocks GTK.**

**Why this over Wails native `SingleInstanceOptions`:** the launcher team built this custom path
deliberately (Wails v3 alpha's native single-instance + GTK focus were unreliable). It reuses outwall's
existing socket, is crash-safe via flock, and — critically — the lock / notify / route are all
**CGO-free**, so they get real unit tests in the standard `go test ./...` gate (only the GTK raise is
desktop-tagged). Reference (read it): `citeck-launcher/internal/desktop/{instance_unix.go,focus.go}`
and `citeck-launcher/cmd/citeck-desktop/main.go` (the `raiseToFront` closure ~line 255). **Do NOT copy
any `citeck` string, import, or branding** — port the pattern only.

**Tech Stack:** Go 1.26 (`syscall.Flock`, `net/http` over a unix socket — CGO-free); Wails v3
(`v3.0.0-alpha2.103`) only in the desktop-tagged wrapper. No new dependencies.

## Global Constraints

- Module path `github.com/Sipaha/outwall` exact. **No `citeck`** strings/imports/branding anywhere
  (this plan reads launcher code for the pattern — porting must be clean-room of branding).
- **Server binary stays CGO-free:** the lock, the socket-notify, and the `/desktop/focus` route live in
  CGO-free packages (`internal/desktop`, `internal/daemon`). Only the GTK window-raise is under
  `cmd/outwall-desktop` (`//go:build desktop`). `go build ./...` / `go test ./...` (no tag) must stay green.
- **No panics / `log.Fatal`** in library code — return wrapped errors. `main` may `log.Fatal`.
- No new deps; don't bump existing. **No `Co-Authored-By`** / no amend. One commit per task. Author
  `Sipaha <sipahabk@gmail.com>`.
- No flaky tests (`t.TempDir()`, OS-assigned socket paths, condition polling not sleeps).
- **Don't** edit `docs/INDEX.md` / `docs/roadmap/current-phase.md`. After `make build-desktop`, restore
  `internal/daemon/webdist/index.html`.

---

### Task 1: `/desktop/focus` route + `Config.OnFocusRequest` (daemon, CGO-free)

**Files:** Modify `internal/daemon/daemon.go` (add `OnFocusRequest func()` to `Config`),
`internal/daemon/admin.go` (register `POST /desktop/focus` + `hDesktopFocus`). Test:
`internal/daemon/admin_test.go`.

**Behavior:** `POST /desktop/focus` → if `cfg.OnFocusRequest != nil`, call it and return `204` (or
`200`); else return `503`/`404` (no window to focus, e.g. headless `serve`). The route is registered in
`apiMux()` so it is reachable on the CSRF-free unix socket (the second instance posts there with no CSRF
header).

- [ ] **Step 1:** Failing test `TestDesktopFocusRoute`: a daemon built with `OnFocusRequest` set to a
  flag-setting stub; `POST /desktop/focus` over the admin handler returns 2xx and the stub ran; a daemon
  with a nil `OnFocusRequest` returns a non-2xx (no panic).
- [ ] **Step 2:** Run `./internal/daemon/ -run DesktopFocus -v` → FAIL.
- [ ] **Step 3:** Add the field + route + handler (nil-safe).
- [ ] **Step 4:** Run (incl. existing daemon tests) under `-race` → PASS.
- [ ] **Step 5:** Commit `feat(daemon): POST /desktop/focus route + Config.OnFocusRequest`.

---

### Task 2: instance lock + focus-notify (internal/desktop, CGO-free)

**Files:** Create `internal/desktop/instance_unix.go` (`//go:build !windows`), `internal/desktop/focus.go`,
`internal/desktop/instance_test.go`, `internal/desktop/focus_test.go`.

**Interfaces:**
```go
package desktop
// ErrFocusedExisting signals the caller (main) to exit 0: another instance was running and was told
// to raise its window. Distinct from a hard error (lock held but nobody answered the focus socket).
var ErrFocusedExisting = errors.New("another instance is running; focused it")
// InstanceLock is a flock-based single-instance lock; Release on exit.
type InstanceLock struct{ /* *os.File */ }
// AcquireInstanceLock flocks lockPath. On success → (lock, nil). On conflict it posts to the running
// instance's focus socket: if that answers → (nil, ErrFocusedExisting) [caller exits 0]; if it does
// not answer (stale) → (nil, wrapped error). os.Exit is the CALLER's job (keeps this testable).
func AcquireInstanceLock(lockPath, socketPath string) (*InstanceLock, error)
func (l *InstanceLock) Release()
// NotifyExistingInstance dials socketPath (unix) and POSTs /desktop/focus; nil on 2xx, error otherwise.
// A short total timeout (~2s) so the second-launch path never hangs.
func NotifyExistingInstance(socketPath string) error
```

- [ ] **Step 1:** Failing tests:
  (a) `TestAcquireInstanceLockExclusive` — first `AcquireInstanceLock(t.TempDir()/lock, <dead socket>)`
  succeeds; a second call on the same lock path, with a socket that **does** answer `/desktop/focus`
  (an `httptest`-style unix listener serving 204), returns `ErrFocusedExisting`; with a socket that does
  **not** answer, returns a non-nil error that is NOT `ErrFocusedExisting`. `Release()` then lets a
  fresh acquire succeed.
  (b) `TestNotifyExistingInstance` — against a unix-socket HTTP server that serves `POST /desktop/focus`
  → nil; against a non-existent socket path → error; against a 500 → error.
- [ ] **Step 2:** Run `./internal/desktop/ -v` → FAIL.
- [ ] **Step 3:** Implement flock acquire (`syscall.Flock`, `LOCK_EX|LOCK_NB`, write pid), the notify
  client (unix `DialContext` + 2 s timeout, like the launcher's `focus.go`), and the conflict→notify→
  `ErrFocusedExisting`/error decision. No `os.Exit` inside.
- [ ] **Step 4:** Run `./internal/desktop/... -race` → PASS.
- [ ] **Step 5:** Commit `feat(desktop): flock single-instance lock + socket focus-notify`.

---

### Task 3: wire the wrapper — gate before daemon, GTK raise on focus (cmd/outwall-desktop, CGO)

**Files:** Modify `cmd/outwall-desktop/main.go`; optionally `cmd/outwall-desktop/focus.go` (`//go:build desktop`)
for the `raiseToFront` helper.

**Behavior — new `run()` order:**
1. resolve `dir`; `lockPath = filepath.Join(dir, "desktop.lock")`, `socketPath = filepath.Join(dir, "outwall.sock")`.
2. **`lock, err := desktop.AcquireInstanceLock(lockPath, socketPath)`** — FIRST, before any port/socket bind.
   - `errors.Is(err, desktop.ErrFocusedExisting)` → `os.Exit(0)` (the running instance was raised).
   - other error → return it (`main` `log.Fatal`s). On success `defer lock.Release()`.
3. `var mainWindow application.Window`; `raiseToFront := func(){ mainWindow.Show(); if linux { mainWindow.SetAlwaysOnTop(true); mainWindow.Focus(); time.AfterFunc(700ms, func(){ application.InvokeAsync(func(){ mainWindow.SetAlwaysOnTop(false) }) }) } else { mainWindow.Focus() } }` (guard `mainWindow != nil`).
4. `h, err := desktop.Run(daemon.Config{ ...existing..., OnFocusRequest: func(){ application.InvokeAsync(raiseToFront) } })` — the daemon now binds the socket and serves `/desktop/focus`.
5. `app := application.New(...)` (no Wails `SingleInstance` option — our flock is the gate); `OnShutdown` stops the daemon as today.
6. `mainWindow = app.Window.NewWithOptions(... URL: h.UIURL ...)` (capture the returned window).
7. SIGINT/SIGTERM + `app.Run()` as today.
   (Note: `application.New` may be created before or after `desktop.Run`; the focus callback only fires
   while `app.Run()` is live, by which point `mainWindow` is set. Ensure `application.InvokeAsync` is
   only invoked once the app exists — the callback path guarantees this.)

- [ ] **Step 1:** Implement the reorder + lock gate + `raiseToFront` + `OnFocusRequest` wiring.
- [ ] **Step 2:** `make build-desktop` compiles (CGO+GTK); then `git checkout -- internal/daemon/webdist/index.html`.
- [ ] **Step 3:** Commit `feat(desktop): single-instance gate + GTK-safe window raise on second launch`.

---

### Task 4: integration smoke + ADR + docs + gate

**Files:** Create an integration check (a `//go:build desktop` Go test or a shell script under
`cmd/outwall-desktop/`) + `docs/architecture/decisions/0013-desktop-single-instance.md` +
`docs/architecture/modules/desktop.md` update.

**Smoke (throwaway `$HOME`/data dir + `xvfb-run`, must not touch the real instance/vault):**
- launch instance #1 in the background under `xvfb-run`; wait (poll) until its `outwall.sock` answers;
- launch instance #2; assert it **exits 0** within a few seconds and its stderr/stdout shows the
  focus-handoff path (no "address already in use" / port-bind error);
- assert instance #1 is **still running**; clean up.
- If no headless session/dbus/X is available, **skip with a clear message** (don't fail).

- [ ] **Step 1:** Write + run the smoke (instance #2 exits 0, instance #1 survives, no port-bind error).
- [ ] **Step 2:** Write ADR-0013 (records: launcher-pattern flock + socket focus-notify chosen over Wails
  native SingleInstance and why; the ordering — lock gate before the daemon binds ports; the GTK
  focus-stealing workaround + InvokeAsync UI-thread rule; ExitCode 0; stale-lock → error).
- [ ] **Step 3:** Full gate from `outwall/`:
  `make fmt && make vet && go test ./... -race` (file+grep — must stay green; the lock/notify/route tests run here)
  `&& make build && make build-desktop` (then restore index.html). All green. (Run the Task-4 smoke; if it skips for env reasons, note it.)
- [ ] **Step 4:** Commit `docs(desktop): ADR-0013 single-instance (launcher pattern) + smoke`.

## Self-Review

- Single-instance + focus + second-exits → flock gate (Task 2) + `/desktop/focus` route (Task 1) +
  wrapper wiring (Task 3). The lock/notify/route are CGO-free and unit-tested in the standard gate.
- Ordering correctness (gate before port bind) is in Task 3 and proven by the Task-4 smoke (#2 exits 0,
  no "address already in use").
- GTK focus-stealing workaround + the `InvokeAsync` UI-thread rule are ported from the launcher and
  recorded in ADR-0013 (the naive `Show()+Focus()` does NOT reliably raise on GTK).
- Server binary stays CGO-free; no new dep; no `citeck` branding copied.
