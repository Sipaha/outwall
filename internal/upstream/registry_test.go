package upstream

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
)

func setup(t *testing.T) (*store.Store, *Registry) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "u.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	v := secret.NewVault(s)
	require.NoError(t, v.Init("pw"))
	return s, NewRegistry(s, v)
}

func TestCreateEncryptsAuthConfig(t *testing.T) {
	s, reg := setup(t)

	up, err := reg.Create("github", "https://api.github.com", AuthConfig{
		Type: "static", Header: "Authorization", Token: "Bearer ghp_secret",
	})
	require.NoError(t, err)
	require.NotEmpty(t, up.ID)

	// Raw stored blob must NOT contain the plaintext token.
	var blob []byte
	require.NoError(t, s.DB().QueryRow(
		`SELECT auth_config FROM upstreams WHERE id=?`, up.ID).Scan(&blob))
	require.NotContains(t, string(blob), "ghp_secret")

	got, err := reg.GetByName("github")
	require.NoError(t, err)
	require.Equal(t, "Bearer ghp_secret", got.Auth.Token)
	require.Equal(t, "https://api.github.com", got.BaseURL)

	_, err = reg.GetByName("missing")
	require.ErrorIs(t, err, ErrNotFound)

	_ = sql.ErrNoRows // keep import honest if refactored
}

func TestGetOrCreateByHostLazyAndIdempotent(t *testing.T) {
	s, reg := setup(t)

	up, created, err := reg.GetOrCreateByHost("gitlab.example")
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "gitlab.example", up.Name)
	require.Equal(t, "https://gitlab.example", up.BaseURL)
	require.Equal(t, KindHTTP, up.Kind)
	require.Equal(t, "none", up.Auth.Type) // credential-less on lazy create

	// Idempotent: a second call returns the same upstream and created=false.
	again, created2, err := reg.GetOrCreateByHost("gitlab.example")
	require.NoError(t, err)
	require.False(t, created2)
	require.Equal(t, up.ID, again.ID)

	// Only one row was created.
	var n int
	require.NoError(t, s.DB().QueryRow(`SELECT COUNT(*) FROM upstreams WHERE name=?`, "gitlab.example").Scan(&n))
	require.Equal(t, 1, n)

	// Host-approval resolve attaches the operator-entered token: it round-trips decrypted...
	err = reg.SetAuth(up.ID, AuthConfig{Type: "static", Header: "Authorization", Token: "glpat-secret"})
	require.NoError(t, err)
	got, err := reg.GetByName("gitlab.example")
	require.NoError(t, err)
	require.Equal(t, "glpat-secret", got.Auth.Token)
	require.Equal(t, "static", got.AuthType)

	// ...and is masked at rest (no plaintext token in the stored blob).
	var blob []byte
	require.NoError(t, s.DB().QueryRow(`SELECT auth_config FROM upstreams WHERE id=?`, up.ID).Scan(&blob))
	require.NotContains(t, string(blob), "glpat-secret")
}

func TestCreateKindK8sRoundTrips(t *testing.T) {
	_, reg := setup(t)

	up, err := reg.CreateKind("prod-cluster", "https://api.k8s.example:6443", "k8s", AuthConfig{
		Type:     "none",
		K8sAuth:  "token",
		Token:    "sa-token-secret",
		CABundle: "-----BEGIN CERTIFICATE-----\nFAKE\n-----END CERTIFICATE-----\n",
	})
	require.NoError(t, err)
	require.Equal(t, "k8s", up.Kind)

	got, err := reg.GetByName("prod-cluster")
	require.NoError(t, err)
	require.Equal(t, "k8s", got.Kind)
	require.Equal(t, "token", got.Auth.K8sAuth)
	require.Equal(t, "sa-token-secret", got.Auth.Token)
	require.Equal(t, "-----BEGIN CERTIFICATE-----\nFAKE\n-----END CERTIFICATE-----\n", got.Auth.CABundle)

	// Create (http delegate) keeps Kind = http.
	httpUp, err := reg.Create("plain", "https://api.example", AuthConfig{Type: "none"})
	require.NoError(t, err)
	require.Equal(t, "http", httpUp.Kind)
	reloaded, err := reg.GetByName("plain")
	require.NoError(t, err)
	require.Equal(t, "http", reloaded.Kind)

	// List surfaces the kind too.
	all, err := reg.List()
	require.NoError(t, err)
	kinds := map[string]string{}
	for _, u := range all {
		kinds[u.Name] = u.Kind
	}
	require.Equal(t, "k8s", kinds["prod-cluster"])
	require.Equal(t, "http", kinds["plain"])
}
