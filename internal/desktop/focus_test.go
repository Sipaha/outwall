package desktop

import (
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// serveUnix starts an HTTP server on a unix socket at socketPath serving h, and registers
// cleanup. It returns once the listener is accepting.
func serveUnix(t *testing.T, socketPath string, h http.Handler) {
	t.Helper()
	ln, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

func TestNotifyExistingInstance(t *testing.T) {
	t.Run("answers 2xx", func(t *testing.T) {
		sock := filepath.Join(t.TempDir(), "ok.sock")
		got := make(chan string, 1)
		serveUnix(t, sock, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got <- r.Method + " " + r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		}))
		require.NoError(t, NotifyExistingInstance(sock))
		require.Equal(t, "POST /desktop/focus", <-got)
	})

	t.Run("missing socket", func(t *testing.T) {
		require.Error(t, NotifyExistingInstance(filepath.Join(t.TempDir(), "nope.sock")))
	})

	t.Run("500 is an error", func(t *testing.T) {
		sock := filepath.Join(t.TempDir(), "err.sock")
		serveUnix(t, sock, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		require.Error(t, NotifyExistingInstance(sock))
	})
}
