package authn

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/upstream"
)

func TestOIDCClientCredentialsFetchesAndCaches(t *testing.T) {
	var tokenCalls int32
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenCalls, 1)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "client_credentials", r.PostForm.Get("grant_type"))
		require.Equal(t, "cid", r.PostForm.Get("client_id"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "AT123", "token_type": "Bearer", "expires_in": 3600})
	}))
	defer idp.Close()

	mgr := NewManager(idp.Client())
	up := &upstream.Upstream{ID: "u1", Auth: upstream.AuthConfig{
		Type: "oidc-client-credentials", TokenURL: idp.URL, ClientID: "cid", ClientSecret: "sec", Scope: "api",
	}}
	a, err := mgr.Authenticator(up)
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest(http.MethodGet, "https://api", nil)
		require.NoError(t, a.Apply(req))
		require.Equal(t, "Bearer AT123", req.Header.Get("Authorization"))
	}
	require.Equal(t, int32(1), atomic.LoadInt32(&tokenCalls)) // cached, not re-fetched
}
