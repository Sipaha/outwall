package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/upstream"
)

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

	// (1) start the login → get the authorize URL and pull the state out of it.
	w := req(t, d.AdminHandler(), "POST", "/upstreams/api.test/oauth/login", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
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

	// (3) a callback with an unknown/replayed state is rejected.
	cb2 := httptest.NewRecorder()
	d.hOAuthCallback(cb2, httptest.NewRequest("GET", "/oauth/callback?code=x&state="+state, nil))
	require.Equal(t, http.StatusBadRequest, cb2.Code)
}

func TestOAuthLoginRejectsNonOIDCUpstream(t *testing.T) {
	d := newDaemon(t)
	require.NoError(t, d.vault.Init("pw"))
	_, err := d.upstreams.Create("plain", "https://plain.test", upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)
	w := req(t, d.AdminHandler(), "POST", "/upstreams/plain/oauth/login", "")
	require.Equal(t, http.StatusBadRequest, w.Code)
}
