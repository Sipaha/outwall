package oidcdisc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiscoveryURL(t *testing.T) {
	got, err := DiscoveryURL("https://idp.example/realms/x")
	require.NoError(t, err)
	require.Equal(t, "https://idp.example/realms/x/.well-known/openid-configuration", got)

	// Trailing slash is trimmed.
	got, err = DiscoveryURL("https://idp.example/realms/x/")
	require.NoError(t, err)
	require.Equal(t, "https://idp.example/realms/x/.well-known/openid-configuration", got)

	// A full discovery URL is used as-is.
	full := "https://idp.example/realms/x/.well-known/openid-configuration"
	got, err = DiscoveryURL(full)
	require.NoError(t, err)
	require.Equal(t, full, got)

	_, err = DiscoveryURL("idp.example") // not absolute
	require.Error(t, err)
	_, err = DiscoveryURL("")
	require.Error(t, err)
}

func TestDiscover(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{
			"issuer": "https://idp.example/realms/x",
			"authorization_endpoint": "https://idp.example/realms/x/protocol/openid-connect/auth",
			"token_endpoint": "https://idp.example/realms/x/protocol/openid-connect/token",
			"end_session_endpoint": "https://idp.example/realms/x/protocol/openid-connect/logout",
			"scopes_supported": ["openid", "profile", "email"]
		}`))
	}))
	defer srv.Close()

	cfg, err := Discover(context.Background(), srv.Client(), srv.URL+"/realms/x")
	require.NoError(t, err)
	require.Equal(t, "/realms/x/.well-known/openid-configuration", gotPath)
	require.Equal(t, "https://idp.example/realms/x/protocol/openid-connect/auth", cfg.AuthorizationEndpoint)
	require.Equal(t, "https://idp.example/realms/x/protocol/openid-connect/token", cfg.TokenEndpoint)
	require.Equal(t, "https://idp.example/realms/x/protocol/openid-connect/logout", cfg.EndSessionEndpoint)
	require.Equal(t, []string{"openid", "profile", "email"}, cfg.ScopesSupported)
}

func TestDiscoverErrors(t *testing.T) {
	// Missing endpoints → error.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"issuer":"https://idp.example"}`))
	}))
	defer bad.Close()
	_, err := Discover(context.Background(), bad.Client(), bad.URL)
	require.Error(t, err)

	// Non-200 → error.
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer down.Close()
	_, err = Discover(context.Background(), down.Client(), down.URL)
	require.Error(t, err)
}
