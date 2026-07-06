package daemon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServeAgentSocket(t *testing.T) {
	dir := t.TempDir()
	d, err := New(Config{
		DBPath:        filepath.Join(dir, "outwall.db"),
		SocketPath:    filepath.Join(dir, "outwall.sock"),
		Listen:        "127.0.0.1:0",
		UIListen:      "127.0.0.1:0",
		PruneInterval: -1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = d.Serve(ctx) }()

	sock := filepath.Join(dir, "agent.sock")
	require.Eventually(t, func() bool {
		_, err := net.Dial("unix", sock)
		return err == nil
	}, 3*time.Second, 20*time.Millisecond)

	c := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
	resp, err := c.Post("http://unix/register", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var reg struct{ ID, Token string }
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&reg))
	require.NotEmpty(t, reg.Token)
}
