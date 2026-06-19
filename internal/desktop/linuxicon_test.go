package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstallLinuxIntegration(t *testing.T) {
	dataHome := t.TempDir()
	icon := []byte("\x89PNG\r\n\x1a\n-fake")
	require.NoError(t, InstallLinuxIntegration(dataHome, "outwall", "egress gateway",
		"/opt/my apps/outwall-desktop", icon))

	// Icon written under hicolor apps, named by the app id.
	iconPath := filepath.Join(dataHome, "icons", "hicolor", "512x512", "apps", LinuxAppID+".png")
	got, err := os.ReadFile(iconPath)
	require.NoError(t, err)
	require.Equal(t, icon, got)

	// .desktop written, basename = app id, with matching Icon + StartupWMClass and a quoted Exec.
	deskPath := filepath.Join(dataHome, "applications", LinuxAppID+".desktop")
	entry, err := os.ReadFile(deskPath)
	require.NoError(t, err)
	s := string(entry)
	require.Contains(t, s, "Icon="+LinuxAppID+"\n")
	require.Contains(t, s, "StartupWMClass="+LinuxWMClass+"\n")
	require.Contains(t, s, `Exec="/opt/my apps/outwall-desktop"`) // spaces → quoted
	require.Contains(t, s, "Name=outwall\n")

	// Idempotent: a second call overwrites without error.
	require.NoError(t, InstallLinuxIntegration(dataHome, "outwall", "egress gateway",
		"/opt/outwall", icon))

	require.Error(t, InstallLinuxIntegration("", "outwall", "x", "/o", icon))
}
