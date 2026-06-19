package authn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/upstream"
)

func TestAuthCodeURLIncludesPKCEAndState(t *testing.T) {
	cfg := upstream.AuthConfig{
		Type: "oidc-authorization-code", ClientID: "cid", AuthURL: "https://idp.test/authorize",
		TokenURL: "https://idp.test/token", RedirectURL: "http://127.0.0.1:8182/oauth/callback",
		Scope: "openid profile",
	}
	raw := AuthCodeURL(cfg, "state123", GenerateVerifier())
	u, err := url.Parse(raw)
	require.NoError(t, err)
	q := u.Query()
	require.Equal(t, "code", q.Get("response_type"))
	require.Equal(t, "cid", q.Get("client_id"))
	require.Equal(t, "state123", q.Get("state"))
	require.Equal(t, "http://127.0.0.1:8182/oauth/callback", q.Get("redirect_uri"))
	require.Equal(t, "S256", q.Get("code_challenge_method"))
	require.NotEmpty(t, q.Get("code_challenge"))
	require.Equal(t, "openid profile", q.Get("scope"))
}

// fakeIdP serves the token endpoint for code exchange and refresh.
func fakeIdP(t *testing.T, access1, access2 string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		access := access1
		if r.Form.Get("grant_type") == "refresh_token" {
			access = access2
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"` + access + `","refresh_token":"rt2","token_type":"Bearer","expires_in":3600}`))
	}))
}

func TestExchangeCode(t *testing.T) {
	idp := fakeIdP(t, "at1", "at2")
	defer idp.Close()
	cfg := upstream.AuthConfig{Type: "oidc-authorization-code", ClientID: "cid", TokenURL: idp.URL + "/token"}
	toks, err := ExchangeCode(context.Background(), cfg, "thecode", GenerateVerifier())
	require.NoError(t, err)
	require.Equal(t, "at1", toks.AccessToken)
	require.Equal(t, "rt2", toks.RefreshToken)
	require.False(t, toks.Expiry.IsZero())
}

func TestOIDCAuthCodeRefreshesAndPersists(t *testing.T) {
	idp := fakeIdP(t, "at1", "refreshed")
	defer idp.Close()
	cfg := upstream.AuthConfig{
		Type: "oidc-authorization-code", ClientID: "cid", TokenURL: idp.URL + "/token",
		AccessToken: "expired", RefreshToken: "rt1",
		TokenExpiry: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), // already expired → refresh
	}
	var persisted Tokens
	a := newOIDCAuthCode(cfg, func(tk Tokens) { persisted = tk })

	req := httptest.NewRequest("GET", "https://api.test/x", nil)
	require.NoError(t, a.Apply(req))
	require.Equal(t, "Bearer refreshed", req.Header.Get("Authorization"))
	require.Equal(t, "refreshed", persisted.AccessToken, "a refreshed token must be persisted")
	require.Equal(t, "rt2", persisted.RefreshToken)
}

func TestManagerBuildsAuthCodeWithPersister(t *testing.T) {
	idp := fakeIdP(t, "at1", "refreshed")
	defer idp.Close()
	mgr := NewManager(nil)
	var gotID string
	mgr.SetOAuthPersister(func(id string, _ Tokens) { gotID = id })
	up := &upstream.Upstream{ID: "u-oauth", Kind: upstream.KindHTTP, Auth: upstream.AuthConfig{
		Type: "oidc-authorization-code", ClientID: "cid", TokenURL: idp.URL + "/token",
		AccessToken: "expired", RefreshToken: "rt1",
		TokenExpiry: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	}}
	a, err := mgr.Authenticator(up)
	require.NoError(t, err)
	req := httptest.NewRequest("GET", "https://api.test/x", nil)
	require.NoError(t, a.Apply(req))
	require.Equal(t, "Bearer refreshed", req.Header.Get("Authorization"))
	require.Equal(t, "u-oauth", gotID, "the persister is wired with the upstream ID")

	// Not logged in → build error.
	_, err = mgr.Authenticator(&upstream.Upstream{ID: "u2", Kind: upstream.KindHTTP, Auth: upstream.AuthConfig{
		Type: "oidc-authorization-code", ClientID: "cid", TokenURL: idp.URL + "/token"}})
	require.Error(t, err)
}
