package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/upstream"
)

// TestDefaultCallbackListen: an empty CallbackListen defaults to the fixed port (no listener bound —
// the default is only realized when a login starts).
func TestDefaultCallbackListen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "none"))
	dir := t.TempDir()
	d, err := New(Config{DBPath: filepath.Join(dir, "d.db"), SocketPath: filepath.Join(dir, "d.sock")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	require.Equal(t, DefaultCallbackListen, d.cfg.CallbackListen)
	require.False(t, d.callback.running(), "no listener bound until a login starts")
}

func TestOAuthLoginAndCallbackStoresTokens(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at1","refresh_token":"rt1","token_type":"Bearer","expires_in":3600}`))
	}))
	defer idp.Close()

	d := newDaemon(t)
	require.NoError(t, d.vault.Init("pw"))

	_, err := d.upstreams.Create("api.test", "https://api.test", upstream.AuthConfig{
		Type: "oidc-authorization-code", ClientID: "cid",
		AuthURL: idp.URL + "/authorize", TokenURL: idp.URL + "/token",
	})
	require.NoError(t, err)

	// The callback listener is on-demand: down while idle.
	require.False(t, d.callback.running())

	// (1) start the login → get the authorize URL and pull the state out of it.
	w := req(t, d.AdminHandler(), "POST", "/upstreams/api.test/oauth/login", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.True(t, d.callback.running(), "listener comes up for the login")
	var lr struct {
		URL string `json:"url"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &lr))
	u, err := url.Parse(lr.URL)
	require.NoError(t, err)
	state := u.Query().Get("state")
	require.NotEmpty(t, state)

	// (2) the IdP redirects back to the callback with code + state → tokens get stored.
	cb := httptest.NewRecorder()
	d.hOAuthCallback(cb, httptest.NewRequest("GET", "/oauth/callback?code=thecode&state="+state, nil))
	require.Equal(t, http.StatusOK, cb.Code, cb.Body.String())
	require.Contains(t, cb.Body.String(), "Login complete")

	up, err := d.upstreams.GetByName("api.test")
	require.NoError(t, err)
	require.Equal(t, "at1", up.Auth.AccessToken)
	require.Equal(t, "rt1", up.Auth.RefreshToken)
	require.False(t, d.callback.running(), "listener is released after the callback resolves")

	// The host list now reports the OIDC host as logged in (tokens held), not merely configured.
	require.Contains(t, req(t, d.AdminHandler(), "GET", "/upstreams", "").Body.String(), `"logged_in":true`)

	// (3) a callback with an unknown/replayed state is rejected.
	cb2 := httptest.NewRecorder()
	d.hOAuthCallback(cb2, httptest.NewRequest("GET", "/oauth/callback?code=x&state="+state, nil))
	require.Equal(t, http.StatusBadRequest, cb2.Code)
}

func TestOAuthRedirectURIFixedCallback(t *testing.T) {
	d := newDaemon(t) // ephemeral callback bind in tests
	require.NoError(t, d.vault.Init("pw"))
	// The redirect URI is derived from the configured callback bind and used in the authorize URL.
	want := "http://" + d.cfg.CallbackListen + "/callback"

	// The endpoint reports the fixed redirect URI to register in the IdP.
	w := req(t, d.AdminHandler(), "GET", "/oidc/redirect-uri", "")
	require.Equal(t, http.StatusOK, w.Code)
	var rr struct {
		RedirectURI string `json:"redirect_uri"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rr))
	require.Equal(t, want, rr.RedirectURI)

	// The authorize URL carries that exact redirect_uri.
	_, err := d.upstreams.Create("api.test", "https://api.test", upstream.AuthConfig{
		Type: "oidc-authorization-code", ClientID: "cid",
		AuthURL: "https://idp/authorize", TokenURL: "https://idp/token",
	})
	require.NoError(t, err)
	w2 := req(t, d.AdminHandler(), "POST", "/upstreams/api.test/oauth/login", "")
	require.Equal(t, http.StatusOK, w2.Code, w2.Body.String())
	var lr struct {
		URL string `json:"url"`
	}
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &lr))
	u, err := url.Parse(lr.URL)
	require.NoError(t, err)
	require.Equal(t, want, u.Query().Get("redirect_uri"))
}

func TestOAuthCallbackEscapesReflectedError(t *testing.T) {
	d := newDaemon(t)
	cb := httptest.NewRecorder()
	d.hOAuthCallback(cb, httptest.NewRequest("GET", "/oauth/callback?error=<script>alert(1)</script>", nil))
	require.Equal(t, http.StatusBadRequest, cb.Code)
	body := cb.Body.String()
	require.NotContains(t, body, "<script>", "reflected error must be HTML-escaped")
	require.Contains(t, body, "&lt;script&gt;")
}

func TestOAuthLoginOpensSystemBrowserInDesktopMode(t *testing.T) {
	d := newDaemon(t)
	require.NoError(t, d.vault.Init("pw"))
	_, err := d.upstreams.Create("api.test", "https://api.test", upstream.AuthConfig{
		Type: "oidc-authorization-code", ClientID: "cid",
		AuthURL: "https://idp.test/authorize", TokenURL: "https://idp.test/token",
	})
	require.NoError(t, err)

	var openedURL string
	d.cfg.OpenURL = func(u string) error { openedURL = u; return nil }

	w := req(t, d.AdminHandler(), "POST", "/upstreams/api.test/oauth/login", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var lr struct {
		URL    string `json:"url"`
		Opened bool   `json:"opened"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &lr))
	require.True(t, lr.Opened, "desktop mode must report the browser was opened server-side")
	require.Equal(t, lr.URL, openedURL, "the OpenURL hook receives the authorize URL")
}

func TestOAuthLoginRejectsNonOIDCUpstream(t *testing.T) {
	d := newDaemon(t)
	require.NoError(t, d.vault.Init("pw"))
	_, err := d.upstreams.Create("plain", "https://plain.test", upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)
	w := req(t, d.AdminHandler(), "POST", "/upstreams/plain/oauth/login", "")
	require.Equal(t, http.StatusBadRequest, w.Code)
}
