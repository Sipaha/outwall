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
