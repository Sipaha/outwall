//go:build desktop

// Command outwall-desktop is the Wails v3 GUI shell for outwall.
//
// It runs the outwall daemon IN-PROCESS (via internal/desktop.Run) and renders
// the embedded UI in a native WebKitGTK webview pointed at the daemon's UIListen
// loopback bind. outwall has no Docker or upgrade contract, so supervising the
// daemon as a separate child process buys nothing — the daemon shares this
// process and is torn down on shutdown. See ADR-0007.
//
// This file carries `//go:build desktop` so the default no-tag gate
// (`go build ./...`, `go vet ./...`, `go test ./...`) skips it and the server
// binary stays CGO-free. The desktop build is `CGO_ENABLED=1 go build -tags
// desktop ./cmd/outwall-desktop`.
package main

import (
	"context"
	_ "embed"
	"errors"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/Sipaha/outwall/internal/browser"
	"github.com/Sipaha/outwall/internal/config"
	"github.com/Sipaha/outwall/internal/daemon"
	"github.com/Sipaha/outwall/internal/desktop"

	// Bundle server-profile plugins (self-register via init()). The core never imports these; the
	// binary entrypoint opts each one in. Add a line here for each new platform plugin.
	_ "github.com/Sipaha/outwall/internal/serverprofile/citeck"
)

//go:embed logo.png
var logoPNG []byte

// Loopback binds for the in-process daemon. The webview loads UIListen; the
// data plane and MCP control plane are bound for agents running on the host.
const (
	uiListen   = "127.0.0.1:8182"
	dataListen = "127.0.0.1:8099"
	mcpListen  = "127.0.0.1:8181"
	// cbListen is the fixed OIDC browser-login callback bind; its /callback is the redirect URI
	// registered in the IdP.
	cbListen = "127.0.0.1:23312"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run wires the in-process daemon and the Wails window, then blocks in app.Run
// until the GUI exits. Keeping the body here (rather than in main) lets deferred
// cleanups fire on an early error return instead of being skipped by log.Fatal.
func run() error {
	dir, err := dataDir()
	if err != nil {
		return err
	}

	// On Linux/GTK4 the window's taskbar icon is resolved from a .desktop file by app_id (GTK4
	// dropped gtk_window_set_icon), so install one + a hicolor icon. Best-effort — a failure must
	// not block launch. macOS/Windows take the icon from Options.Icon directly.
	if runtime.GOOS == "linux" {
		exe, _ := os.Executable()
		if ierr := desktop.InstallLinuxIntegration(desktop.LinuxDataHome(), "outwall",
			"Authenticating egress gateway for AI agents", exe, logoPNG); ierr != nil {
			slog.Warn("install desktop integration (taskbar icon)", "err", ierr)
		}
	}

	socketPath := filepath.Join(dir, "outwall.sock")

	// Single-instance gate (ADR-0013): flock FIRST, before the daemon binds any port or the
	// unix socket. A second launch finds the lock held, posts POST /desktop/focus to the
	// running instance over its admin socket, and — if answered — exits 0 (the running window
	// was raised). A stale lock with no live daemon surfaces as a hard error instead.
	lock, err := desktop.AcquireInstanceLock(filepath.Join(dir, "desktop.lock"), socketPath)
	if errors.Is(err, desktop.ErrFocusedExisting) {
		os.Exit(0)
	}
	if err != nil {
		return err
	}
	defer lock.Release()

	// mainWindow is captured below from NewWithOptions; raiseToFront (focus.go) brings it to
	// the foreground. The OnFocusRequest callback marshals the call onto the Wails UI thread
	// via application.InvokeAsync — calling Wails window methods off-thread deadlocks GTK.
	var mainWindow application.Window
	raiseToFront := func() {
		if mainWindow != nil {
			raiseWindow(mainWindow)
		}
	}

	// Start the in-process daemon and wait for the UI bind to answer.
	h, err := desktop.Run(daemon.Config{
		DBPath:         filepath.Join(dir, "outwall.db"),
		SocketPath:     socketPath,
		Listen:         dataListen,
		UIListen:       uiListen,
		MCPListen:      mcpListen,
		CallbackListen: cbListen,
		OnFocusRequest: func() { application.InvokeAsync(raiseToFront) },
		// The embedded webview drops window.open, so OIDC browser-login URLs are opened in the
		// operator's real system browser from the Go side (ADR-0021).
		OpenURL: browser.Open,
	})
	if err != nil {
		return err
	}
	// stopDaemon is idempotent (Handle.Stop tolerates repeat calls); it runs
	// from both OnShutdown and the deferred guard below in case app.Run never
	// reaches the OnShutdown path (e.g. a startup error).
	var stopped bool
	stopDaemon := func() {
		if stopped {
			return
		}
		stopped = true
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if stopErr := h.Stop(ctx); stopErr != nil {
			slog.Error("daemon stop", "err", stopErr)
		}
	}
	defer stopDaemon()

	// Desktop OS notifications: raised when an agent requests access (approval.enqueued). The
	// service must be registered with the app; clicking a notification raises the window.
	notifs := notifications.New()

	app := application.New(application.Options{
		Name:        "outwall",
		Description: "Authenticating egress gateway for AI agents",
		Icon:        logoPNG,
		OnShutdown:  stopDaemon,
		Services: []application.Service{
			application.NewService(notifs),
		},
		// Sets the X11 WM_CLASS so the .desktop StartupWMClass matches and the taskbar icon
		// resolves (the GApplication app_id used on Wayland is org.wails.outwall — see linuxicon.go).
		Linux: application.LinuxOptions{ProgramName: desktop.LinuxWMClass},
	})

	// The resolved Wails v3 (alpha2.103) WebviewWindowOptions has a `URL` field,
	// so the webview loads the local daemon UI directly over loopback — no
	// reverse-proxy asset handler is needed (see ADR-0007).
	mainWindow = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:            "main",
		Title:           "outwall",
		URL:             h.UIURL,
		Width:           1200,
		Height:          800,
		MinWidth:        480,
		MinHeight:       480,
		DevToolsEnabled: true,
		KeyBindings: map[string]func(w application.Window){
			"F12": func(w application.Window) { w.OpenDevTools() },
		},
	})

	// Minimise-to-tray: closing the window (the X button) HIDES it instead of quitting — outwall
	// keeps running in the background so agents and the data plane stay up. The WindowClosing hook
	// runs before the built-in destroy listener and cancels it (HandleWindowEvent stops dispatching
	// once a hook cancels), mirroring citeck-launcher. The only real quit is the tray's Exit item.
	mainWindow.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		mainWindow.Hide()
		e.Cancel()
	})

	// System tray: keeps outwall reachable while the window is hidden. Left-click raises the window;
	// right-click opens the menu (Open / Exit). Tray callbacks may fire off the UI thread, so window
	// calls are marshalled onto it via InvokeAsync (off-thread Wails calls deadlock GTK).
	tray := app.SystemTray.New()
	tray.SetLabel("outwall")
	tray.SetTooltip("outwall — egress gateway")
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(logoPNG)
	} else {
		tray.SetIcon(logoPNG)
	}
	trayMenu := app.NewMenu()
	trayMenu.Add("Open").OnClick(func(_ *application.Context) { application.InvokeAsync(raiseToFront) })
	trayMenu.AddSeparator()
	trayMenu.Add("Exit").OnClick(func(_ *application.Context) { app.Quit() })
	tray.SetMenu(trayMenu)
	tray.OnClick(func() { application.InvokeAsync(raiseToFront) })

	// Clicking an OS notification raises the outwall window (even when minimised to the tray).
	notifs.OnNotificationResponse(func(notifications.NotificationResult) {
		application.InvokeAsync(raiseToFront)
	})

	// Raise an OS notification whenever an agent requests access (an approval is enqueued), so the
	// operator is prompted even with the window hidden in the tray. Subscribes to the in-process
	// daemon event bus; the goroutine ends when the process exits.
	go func() {
		evCh, cancelSub := h.Subscribe()
		defer cancelSub()
		for ev := range evCh {
			if ev.Type != "approval.enqueued" {
				continue
			}
			data, _ := ev.Data.(map[string]any)
			title, body := approvalNotice(data)
			if err := notifs.SendNotification(notifications.NotificationOptions{
				ID: notifID(data), Title: title, Body: body,
			}); err != nil {
				slog.Warn("send access-request notification", "err", err)
			}
		}
	}()

	// SIGINT/SIGTERM → app.Quit() (Wails' clean shutdown: tears down the window
	// and the event loop, then fires OnShutdown which stops the daemon). A bare
	// ctx cancel would stop the daemon but leave the Wails loop running, hanging
	// the terminal on Ctrl-C.
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("signal received, requesting graceful shutdown")
		app.Quit()
		// Second-signal escape hatch if shutdown hangs.
		go func() {
			<-sigCh
			slog.Warn("second signal received, forcing exit")
			os.Exit(1)
		}()
	}()

	if runErr := app.Run(); runErr != nil {
		return runErr
	}
	return nil
}

// approvalNotice builds the OS-notification title/body for an approval.enqueued event. It uses the
// human-readable host when present, else the HTTP method+path or the k8s resource, plus the purpose.
func approvalNotice(data map[string]any) (title, body string) {
	title = "outwall — access request"
	subject := str(data["host"])
	if subject == "" {
		subject = strings.TrimSpace(str(data["method"]) + " " + str(data["path"]))
	}
	if subject == "" {
		subject = str(data["resource"])
	}
	if subject == "" {
		subject = "an external resource"
	}
	body = "An agent requested access to " + subject
	if p := str(data["purpose"]); p != "" {
		body += " — " + p
	}
	return title, body
}

// notifID returns a stable notification id from the approval id, falling back to a constant.
func notifID(data map[string]any) string {
	if id := str(data["id"]); id != "" {
		return "approval-" + id
	}
	return "approval"
}

// str returns v as a string, or "" if it is nil/not a string.
func str(v any) string {
	s, _ := v.(string)
	return s
}

// dataDir resolves (and creates) the outwall data directory ($HOME/.spk/outwall).
func dataDir() (string, error) {
	dir := config.DataDir()
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return "", mkErr
	}
	return dir, nil
}
