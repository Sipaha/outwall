package daemon

import (
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
	h := d.UIHandler() // the CSRF-wrapped TCP mux
	// no CSRF header → 403
	r1 := httptest.NewRequest("GET", "/vault/status", nil)
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, r1)
	require.Equal(t, http.StatusForbidden, w1.Code)
	// with CSRF header → passes through (200)
	r2 := httptest.NewRequest("GET", "/vault/status", nil)
	r2.Header.Set("X-Outwall-CSRF", "1")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)
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
