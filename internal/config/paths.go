// Package config resolves outwall's on-disk locations.
package config

import (
	"os"
	"path/filepath"
)

// DataDir returns outwall's data directory: $HOME/.spk/outwall. If the home
// directory cannot be resolved it falls back to ./.spk/outwall.
func DataDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".spk", "outwall")
	}
	return filepath.Join(home, ".spk", "outwall")
}

// CACertPath returns the path to the local CA certificate (DataDir/ca.crt). An HTTP client or
// browser talking to the data plane must trust this cert (it is not in the system trust store).
func CACertPath() string {
	return filepath.Join(DataDir(), "ca.crt")
}
