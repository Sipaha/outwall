package client_test

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/client"
)

func TestDoAuthSetsBearer(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "t.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /whoami", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"authz": r.Header.Get("Authorization")})
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	c := client.New(sock)

	// DoAuth sets the bearer header.
	var withTok struct{ Authz string }
	require.NoError(t, c.DoAuth("owa_abc", "GET", "/whoami", nil, &withTok))
	require.Equal(t, "Bearer owa_abc", withTok.Authz)

	// Do sets no Authorization header (operator CLI behavior preserved).
	var noTok struct{ Authz string }
	require.NoError(t, c.Do("GET", "/whoami", nil, &noTok))
	require.Equal(t, "", noTok.Authz)

	_ = os.Remove(sock)
}
