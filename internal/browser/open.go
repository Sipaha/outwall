// Package browser opens a URL in the operator's default system browser. It is CGO-free and used by
// the desktop wrapper for the OIDC browser-login flow: the embedded WebKitGTK webview drops
// window.open to a new window (Wails v3 connects no "create"/"decide-policy" signal on Linux), so
// the login URL must be handed to the real browser from the Go side instead.
package browser

import (
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
)

// Open launches the system browser at rawURL. It accepts only http/https URLs (the value is passed
// as a separate process argument, never a shell string, so there is no shell injection, but the
// scheme check rejects file://, javascript:, etc.). It returns once the opener has been spawned.
func Open(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("browser: parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("browser: refusing to open non-http(s) url %q", u.Scheme)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default: // linux and other unixes
		cmd = exec.Command("xdg-open", rawURL)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("browser: launch opener: %w", err)
	}
	// Reap the child so it does not linger as a zombie; the opener exits quickly after handing the
	// URL to the browser.
	go func() { _ = cmd.Wait() }()
	return nil
}
