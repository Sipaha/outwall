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
		DBPath:         filepath.Join(dir, "o.db"),
		SocketPath:     filepath.Join(dir, "o.sock"),
		Listen:         "127.0.0.1:0",
		UIListen:       "127.0.0.1:18299", // fixed free-ish port for the test
		CallbackListen: "127.0.0.1:0",     // ephemeral — never the fixed 23312 (a running app may hold it)
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
