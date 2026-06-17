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
