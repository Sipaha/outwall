package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDataDirUnderSpk(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	require.Equal(t, filepath.Join("/home/tester", ".spk", "outwall"), DataDir())
}
