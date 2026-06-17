# outwall — Plan 7: Wails 3 Desktop Wrapper

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development or superpowers:executing-plans.

**Goal:** Ship the desktop app: a Wails v3 thin-wrapper (`cmd/outwall-desktop`) that runs the
outwall daemon **in-process** and renders the embedded UI in a native webview pointed at the
`UIListen` localhost bind. The Unlock screen appears at launch (vault locked); everything else is
the same UI verified in Plans 6A/6B. The server binary (`cmd/outwall`) stays CGO-free; only this
new target is CGO + GTK.

**Architecture:** Unlike citeck-launcher (which supervises the daemon as a *child process* for
zero-downtime upgrades), outwall runs the daemon in the same process — no Docker, no upgrade
contract, so a child process buys nothing. A small CGO-free helper `internal/desktop` starts
`daemon.New(...).Serve(ctx)` in a goroutine, waits for `UIListen` to answer, and returns the URL +
a stop func — this part is unit-testable. The GUI shell `cmd/outwall-desktop/main.go`
(`//go:build desktop`) creates the Wails app, calls the helper, opens a webview window at the URL,
and tears the daemon down on shutdown.

**Tech Stack:** Plan 1–6 stack + `github.com/wailsapp/wails/v3` (**latest** release — `go get
github.com/wailsapp/wails/v3@latest`; the launcher pins alpha.98 but per the use-latest-deps rule
take the newest, and **confirm the `application` API with `go doc` since v3 is pre-stable**). Linux
build needs CGO + GTK3 + WebKit2GTK 4.1 (all present on this host) + gcc.

## Global Constraints

(All prior Go constraints apply.) Plus:
- `cmd/outwall-desktop/main.go` carries `//go:build desktop` so the default `go build ./...` /
  `go vet ./...` / `go test ./...` (no tags) skip it — the CGO-free server build + the test gate
  stay green. The desktop build is `CGO_ENABLED=1 go build -tags desktop`.
- `internal/desktop` (the daemon-runner helper) is **plain Go, no build tag, no CGO, no Wails
  import** — so it is covered by the normal test gate.
- **Use the latest Wails v3** (`@latest`); confirm the real API (`application.New`,
  `app.Window.New*`, webview URL/external-URL loading, `OnShutdown`, `app.Run`/`app.Quit`) via
  `go doc github.com/wailsapp/wails/v3/pkg/application` — the snippets below are from the launcher's
  alpha.98 usage and may differ. Reference: `~/.spk/spk-editor/solutions/citeck-launcher/citeck-launcher/cmd/citeck-desktop/main.go`.
- No citeck strings/branding.

## File Structure

```
Create:  internal/desktop/runner.go        # CGO-free: start in-process daemon + readiness wait
         internal/desktop/runner_test.go
         cmd/outwall-desktop/main.go         # //go:build desktop — Wails GUI shell
         cmd/outwall-desktop/logo.png        # window/app icon (simple generated PNG ok)
Modify:  Makefile                            # build-desktop target
         go.mod / go.sum                      # + wails/v3 (under the desktop build)
         .gitignore                           # dist/bin/outwall-desktop already covered by /dist/
```

---

### Task 1: in-process daemon runner (CGO-free, tested)

**Files:** Create `internal/desktop/runner.go`, `internal/desktop/runner_test.go`.

**Interfaces:**
- `desktop.Run(cfg daemon.Config) (*desktop.Handle, error)` — builds `daemon.New(cfg)`, starts
  `Serve(ctx)` in a goroutine, polls `http://<cfg.UIListen>/` until it answers 200 (timeout ~10s),
  returns a `Handle` (or an error if the daemon never came up).
- `type Handle struct { UIURL string; ... }` with `(*Handle).Stop(ctx context.Context) error`
  (cancels the daemon ctx, waits for Serve to return, closes the store) and `(*Handle).UIURL`
  = `http://<cfg.UIListen>/`.

- [ ] **Step 1: failing test** — `internal/desktop/runner_test.go`:
```go
package desktop

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/daemon"
)

func TestRunStartsAndStops(t *testing.T) {
	dir := t.TempDir()
	h, err := Run(daemon.Config{
		DBPath:     filepath.Join(dir, "o.db"),
		SocketPath: filepath.Join(dir, "o.sock"),
		Listen:     "127.0.0.1:0",
		UIListen:   "127.0.0.1:18299", // fixed free-ish port for the test
		MCPListen:  "127.0.0.1:0",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = h.Stop(context.Background()) })

	require.Contains(t, h.UIURL, "18299")
	resp, err := http.Get(h.UIURL)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode) // UI served

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, h.Stop(ctx))
}
```
  (If `daemon.Config` uses `Listen:"127.0.0.1:0"` the data-plane port is OS-assigned; confirm the
  daemon's `Serve` tolerates `:0` on the data-plane + MCP binds. If it doesn't, the runner can use
  fixed high ports — but prefer `:0` and fix `Serve` if needed, noting it in the REPORT.)

- [ ] **Step 2** run → FAIL. **Step 3** implement `runner.go` (goroutine `Serve`, a readiness
  poll loop with a short client timeout, ctx cancel + `daemon.Close` on Stop). **Step 4** run
  `go test ./internal/desktop/` → PASS; also `go vet ./...` + `go test ./...` stay green (no tag).
  **Step 5** commit `feat(desktop): in-process daemon runner + readiness wait`.

---

### Task 2: Wails GUI shell + build-desktop

**Files:** Create `cmd/outwall-desktop/main.go` (`//go:build desktop`), `cmd/outwall-desktop/logo.png`; modify `Makefile`, `go.mod`.

**Behavior (confirm Wails v3 API with `go doc` first):**
- Add the dep: `go get github.com/wailsapp/wails/v3@latest` (record the resolved version).
- `main.go` (`//go:build desktop`):
  - Choose an `outwall` data dir (`os.UserConfigDir()/outwall`) and a fixed `UIListen` (e.g.
    `127.0.0.1:8182`), data-plane + MCP binds (`127.0.0.1:8099` / `127.0.0.1:8181`).
  - `h, err := desktop.Run(cfg)` — start the in-process daemon; on error, log + exit non-zero.
  - Create the Wails app and a webview window pointed at `h.UIURL`:
    ```go
    app := application.New(application.Options{
        Name:        "outwall",
        Description: "Authenticating egress gateway for AI agents",
        Icon:        logoPNG, // //go:embed logo.png
        OnShutdown: func() {
            ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
            _ = h.Stop(ctx); cancel()
        },
    })
    app.Window.NewWithOptions(application.WebviewWindowOptions{
        Name: "main", Title: "outwall", Width: 1200, Height: 800, MinWidth: 480, MinHeight: 480,
        URL: h.UIURL, // load the local daemon UI (confirm the field name in the resolved API)
        DevToolsEnabled: true,
        KeyBindings: map[string]func(application.Window){
            "F12": func(w application.Window) { w.OpenDevTools() },
        },
    })
    // SIGINT/SIGTERM → app.Quit() (fires OnShutdown → daemon stop)
    ...
    if err := app.Run(); err != nil { log.Fatal(err) }
    ```
  - **If the resolved Wails v3 has no external-`URL` webview field**, fall back to an
    `application.AssetOptions{Handler: <reverse-proxy to h.UIURL>}` (a `httputil.ReverseProxy` to
    the loopback UIListen) — mirror the launcher's `loadingHandler` approach. Pick whichever the
    real API supports; document the choice.
- `Makefile`:
  ```makefile
  DESKTOP_TAGS ?= desktop
  build-desktop: build-web
  	@mkdir -p dist/bin
  	CGO_ENABLED=1 go build -tags "$(DESKTOP_TAGS)" -o dist/bin/outwall-desktop ./cmd/outwall-desktop
  ```
- `cmd/outwall-desktop/logo.png` — a simple icon (a solid-square PNG or a minimal generated mark;
  not load-bearing).

- [ ] **Step 1** add the dep + write `main.go` against the **real** Wails v3 API (`go doc` first).
- [ ] **Step 2** `make build-desktop` → produces `dist/bin/outwall-desktop` (CGO + gtk). It must
  COMPILE; a full GUI launch needs a display (the supervisor does a best-effort launch smoke).
- [ ] **Step 3** confirm the no-tag gate is unaffected: `go build ./...` (CGO-free server) +
  `go vet ./...` + `go test ./...` still green (the `desktop`-tagged file is skipped).
- [ ] **Step 4** commit `feat(desktop): Wails v3 GUI shell + build-desktop`.

---

## Verification (supervisor)

1. Gate: `gofmt -l .`, `go vet ./...`, `go test ./...` (no-tag, must be green — proves the
   server build + tests are unaffected), and `make build` (CGO-free server still builds).
2. Desktop build: `make build-desktop` → `dist/bin/outwall-desktop` exists.
3. Best-effort GUI launch smoke: if a display / `xvfb-run` is available, launch
   `outwall-desktop` briefly and confirm it doesn't crash + the in-process daemon answers on
   `UIListen` (curl 200). If no display, document that GUI launch is manual-only and rely on the
   `internal/desktop` runner test + the clean build as the automated evidence.

## Self-Review

- **Spec coverage:** desktop app via Wails v3 ✓; renders the embedded UI ✓; unlock at launch
  (the UI's own Unlock screen) ✓; daemon lifecycle owned by the wrapper (in-process, stopped on
  shutdown) ✓; server binary stays CGO-free (tag isolation) ✓.
- **Divergence from launcher (documented):** in-process daemon, not a child-process supervisor —
  outwall has no Docker/zero-downtime-upgrade need, so the child-process control socket / binary
  selection / respawn machinery is omitted. ADR-0007 records this.
- **Risk:** Wails v3 is pre-stable; the GUI shell is the only place to adapt to the resolved API,
  and the testable logic lives in the CGO-free `internal/desktop` runner.

## ADR + docs (finalize)

ADR-0007 (implementer writes it): the desktop wrapper design — in-process daemon (vs launcher's
child-process model, with the rationale), the `desktop`-build-tag isolation keeping the server
CGO-free, webview-points-at-UIListen (or reverse-proxy asset handler) approach, and the resolved
Wails v3 version. Module doc `desktop.md` (new); update `daemon.md` if `Serve` changed to tolerate
`:0` binds. This completes Phase 1.
