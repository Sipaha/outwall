package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LinuxAppID is the GTK application id Wails derives from the app name ("org.wails.<name>").
// GTK4 removed gtk_window_set_icon, so on Linux the window's taskbar icon is resolved from a
// .desktop file matched to this id (Wayland app_id) / the WM_CLASS (X11). It MUST equal
// "org.wails.<Options.Name>" and the .desktop basename / Icon name below use it.
const LinuxAppID = "org.wails.outwall"

// LinuxWMClass is the X11 WM_CLASS set via Options.Linux.ProgramName; StartupWMClass matches it so
// X11 window managers also resolve the icon.
const LinuxWMClass = "outwall"

// InstallLinuxIntegration writes (idempotently) a freedesktop .desktop file and a hicolor icon so
// the running window's taskbar/dock icon resolves by app_id on GTK4 — the embedded window Icon is
// ignored there (see ADR-0007 addendum). dataHome is XDG_DATA_HOME (e.g. ~/.local/share); execPath
// is the running binary; iconPNG is the app icon. Best-effort: callers log a failure, they do not
// fail startup over it.
func InstallLinuxIntegration(dataHome, name, comment, execPath string, iconPNG []byte) error {
	if dataHome == "" {
		return fmt.Errorf("desktop: empty XDG data home")
	}
	iconDir := filepath.Join(dataHome, "icons", "hicolor", "512x512", "apps")
	if err := os.MkdirAll(iconDir, 0o755); err != nil {
		return fmt.Errorf("desktop: mkdir icon dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(iconDir, LinuxAppID+".png"), iconPNG, 0o644); err != nil {
		return fmt.Errorf("desktop: write icon: %w", err)
	}

	appsDir := filepath.Join(dataHome, "applications")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		return fmt.Errorf("desktop: mkdir applications dir: %w", err)
	}
	// Quote the Exec path if it contains spaces (Desktop Entry spec).
	exec := execPath
	if strings.ContainsAny(exec, " \t") {
		exec = `"` + exec + `"`
	}
	entry := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=" + name + "\n" +
		"Comment=" + comment + "\n" +
		"Exec=" + exec + "\n" +
		"Icon=" + LinuxAppID + "\n" +
		"Terminal=false\n" +
		"Categories=Utility;Network;\n" +
		"StartupWMClass=" + LinuxWMClass + "\n"
	if err := os.WriteFile(filepath.Join(appsDir, LinuxAppID+".desktop"), []byte(entry), 0o644); err != nil {
		return fmt.Errorf("desktop: write .desktop: %w", err)
	}
	return nil
}

// LinuxDataHome returns the XDG_DATA_HOME directory ($XDG_DATA_HOME or ~/.local/share).
func LinuxDataHome() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share")
}
