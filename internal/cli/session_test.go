package cli

import (
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/client"
)

func TestIsSessionRequired(t *testing.T) {
	require.False(t, isSessionRequired(nil))
	require.False(t, isSessionRequired(errors.New("daemon: something else")))
	// The daemon gate surfaces through client.Do as "daemon: operator session required".
	require.True(t, isSessionRequired(errors.New("daemon: operator session required")))
}

// TestDoPrivilegedOpensSessionAndRetries stands up a unix-socket daemon stub that rejects the first
// privileged call with 403 "operator session required", accepts /operator/session/open, then accepts
// the retry — proving doPrivileged prompts (stubbed), opens the session, and retries exactly once.
func TestDoPrivilegedOpensSessionAndRetries(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "d.sock")
	var opened, calls int32

	mux := http.NewServeMux()
	mux.HandleFunc("POST /operator/session/open", func(w http.ResponseWriter, _ *http.Request) {
		atomic.StoreInt32(&opened, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"open":true,"idle_remaining_seconds":3600}`))
	})
	mux.HandleFunc("POST /rules", func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 && atomic.LoadInt32(&opened) == 0 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"operator session required"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"r1"}`))
	})

	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	// Stub the TTY prompt so the test needs no terminal.
	restore := promptPasswordFn
	promptPasswordFn = func(string) (string, error) { return "pw", nil }
	t.Cleanup(func() { promptPasswordFn = restore })

	gf := &globalFlags{socket: sock}
	var out map[string]string
	require.NoError(t, doPrivileged(gf, "POST", "/rules", map[string]string{"x": "y"}, &out))
	require.Equal(t, "r1", out["id"])
	require.Equal(t, int32(1), atomic.LoadInt32(&opened), "session must have been opened")
	require.Equal(t, int32(2), atomic.LoadInt32(&calls), "the privileged call must run twice (initial 403 + retry)")

	// Sanity: a plain client with no session gets the raw gate error.
	require.True(t, isSessionRequired(client.New(sock).Do("POST", "/rules-does-not-exist", nil, nil)) == false)
	_ = os.Remove(sock)
}
