package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func newDaemon(t *testing.T) *Daemon {
	t.Helper()
	d, err := New(Config{
		DBPath:     filepath.Join(t.TempDir(), "d.db"),
		SocketPath: filepath.Join(t.TempDir(), "d.sock"),
		Listen:     "127.0.0.1:0",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func req(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAdminVaultAndUpstreamFlow(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()

	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	// Wrong password on unlock → 401.
	d.vault.Lock()
	require.Equal(t, http.StatusUnauthorized, req(t, h, "POST", "/vault/unlock", `{"password":"no"}`).Code)
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/unlock", `{"password":"pw"}`).Code)

	// Create upstream.
	w := req(t, h, "POST", "/upstreams",
		`{"name":"gh","base_url":"https://api.github.com","auth":{"type":"static","header":"Authorization","token":"Bearer x"}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// List upstreams must not leak the secret token.
	wl := req(t, h, "GET", "/upstreams", "")
	require.Equal(t, http.StatusOK, wl.Code)
	require.NotContains(t, wl.Body.String(), "Bearer x")

	// Register agent returns a token.
	wa := req(t, h, "POST", "/agents/register", `{"name":"claude"}`)
	require.Equal(t, http.StatusOK, wa.Code)
	require.Contains(t, wa.Body.String(), "owa_")
}

func TestAdminVaultLock(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()

	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	// After init the vault is unlocked.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/lock", "").Code)

	// Status now reports locked:true.
	ws := req(t, h, "GET", "/vault/status", "")
	require.Equal(t, http.StatusOK, ws.Code)
	var st map[string]bool
	require.NoError(t, json.Unmarshal(ws.Body.Bytes(), &st))
	require.True(t, st["locked"])
}

func TestAdminRulesAndApprovals(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	// create an upstream + a rule.
	wu := req(t, h, "POST", "/upstreams", `{"name":"gh","base_url":"https://api.github.com","auth":{"type":"none"}}`)
	require.Equal(t, http.StatusOK, wu.Code)
	var up map[string]string
	require.NoError(t, json.Unmarshal(wu.Body.Bytes(), &up))

	wr := req(t, h, "POST", "/rules", `{"upstream_id":"`+up["id"]+`","method":"*","path_glob":"/**","outcome":"allow"}`)
	require.Equal(t, http.StatusOK, wr.Code, wr.Body.String())

	wl := req(t, h, "GET", "/rules", "")
	require.Equal(t, http.StatusOK, wl.Code)
	require.Contains(t, wl.Body.String(), up["id"])

	// resolving an unknown approval → 404.
	require.Equal(t, http.StatusNotFound, req(t, h, "POST", "/approvals/nope/resolve", `{"approve":true}`).Code)
}

func TestAdminAuditEmptyOK(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	require.Equal(t, http.StatusOK, req(t, h, "GET", "/audit", "").Code)
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/audit/prune", `{"older_than_rfc3339":"2020-01-01T00:00:00Z"}`).Code)
	require.Equal(t, http.StatusNotFound, req(t, h, "GET", "/audit/nope", "").Code)
}

func TestUICSRFGate(t *testing.T) {
	d := newDaemon(t)
	h := d.UIHandler() // the static + /api TCP mux
	// no CSRF header → 403 (API is mounted under /api)
	r1 := httptest.NewRequest("GET", "/api/vault/status", nil)
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, r1)
	require.Equal(t, http.StatusForbidden, w1.Code)
	// with CSRF header → passes through (200)
	r2 := httptest.NewRequest("GET", "/api/vault/status", nil)
	r2.Header.Set("X-Outwall-CSRF", "1")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)
}

func TestUIServesStaticIndex(t *testing.T) {
	d := newDaemon(t)
	h := d.UIHandler()
	// GET / serves the embedded SPA index (no CSRF needed for static assets).
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "outwall")
	// Unknown client-route path falls back to index.html (SPA routing).
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, httptest.NewRequest("GET", "/dashboard", nil))
	require.Equal(t, http.StatusOK, w2.Code)
	require.Contains(t, w2.Body.String(), "outwall")
}

func TestUISSEExemptFromCSRF(t *testing.T) {
	d := newDaemon(t)
	// Use a real server + client: the SSE handler streams on its own goroutine, so reading
	// a shared httptest.ResponseRecorder from the test goroutine would race. The HTTP client
	// returns once response headers arrive (read-synchronized), and canceling the request
	// context unblocks the streaming handler.
	srv := httptest.NewServer(d.UIHandler())
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// No X-Outwall-CSRF header — EventSource cannot set one, so the gate must exempt /api/events.
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode) // 200, not 403 from the CSRF gate
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
}

func TestAdminAccessRequests(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	// resolving an unknown request → 404
	require.Equal(t, http.StatusNotFound, req(t, h, "POST", "/access-requests/nope/resolve", `{"status":"granted"}`).Code)
	// list is empty-but-OK initially
	require.Equal(t, http.StatusOK, req(t, h, "GET", "/access-requests", "").Code)
}
