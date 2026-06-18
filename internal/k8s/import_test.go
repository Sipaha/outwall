package k8s

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
	"github.com/Sipaha/outwall/internal/upstream"
)

func newReg(t *testing.T) (*store.Store, *secret.Vault, *upstream.Registry) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "imp.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	v := secret.NewVault(s)
	require.NoError(t, v.Init("pw")) // Init leaves the vault unlocked
	return s, v, upstream.NewRegistry(s, v)
}

// writeKubeconfig writes a two-context kubeconfig (token + insecure) to a temp file and returns
// its path.
func writeKubeconfig(t *testing.T) string {
	t.Helper()
	src := `
apiVersion: v1
kind: Config
clusters:
  - name: a-cluster
    cluster:
      server: https://a.example:6443
      certificate-authority-data: ` + b64(fakeCAPEM) + `
  - name: b-cluster
    cluster:
      server: https://b.example:6443
      insecure-skip-tls-verify: true
users:
  - name: a-user
    user:
      token: a-token
  - name: b-user
    user:
      token: b-token
contexts:
  - name: a-ctx
    context:
      cluster: a-cluster
      user: a-user
  - name: b-ctx
    context:
      cluster: b-cluster
      user: b-user
`
	p := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(p, []byte(src), 0o600))
	return p
}

func TestImporterIdempotent(t *testing.T) {
	_, _, reg := newReg(t)
	im := &Importer{Reg: reg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	path := writeKubeconfig(t)

	added, skipped, err := im.Import([]string{path})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"a-ctx", "b-ctx"}, added)
	require.Empty(t, skipped)

	// The clusters are registered as kind=k8s with the right auth.
	a, err := reg.GetByName("a-ctx")
	require.NoError(t, err)
	require.Equal(t, upstream.KindK8s, a.Kind)
	require.Equal(t, "https://a.example:6443", a.BaseURL)
	require.Equal(t, "a-token", a.Auth.Token)
	require.Equal(t, fakeCAPEM, a.Auth.CABundle)

	b, err := reg.GetByName("b-ctx")
	require.NoError(t, err)
	require.True(t, b.Auth.K8sInsecureSkipVerify)

	// Second import: nothing added, both skipped (idempotent).
	added2, skipped2, err := im.Import([]string{path})
	require.NoError(t, err)
	require.Empty(t, added2)
	require.ElementsMatch(t, []string{"a-ctx", "b-ctx"}, skipped2)

	// Still exactly two clusters in the registry.
	ups, err := reg.List()
	require.NoError(t, err)
	require.Len(t, ups, 2)
}

func TestImporterMissingPathSkipped(t *testing.T) {
	_, _, reg := newReg(t)
	im := &Importer{Reg: reg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	// A non-existent path is silently skipped (not every discovered path exists).
	added, skipped, err := im.Import([]string{filepath.Join(t.TempDir(), "does-not-exist")})
	require.NoError(t, err)
	require.Empty(t, added)
	require.Empty(t, skipped)
}

func TestImporterLockedVaultErrors(t *testing.T) {
	_, v, reg := newReg(t)
	v.Lock()
	im := &Importer{Reg: reg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	path := writeKubeconfig(t)

	added, _, err := im.Import([]string{path})
	require.Error(t, err, "a locked vault cannot encrypt new auth → wrapped error")
	require.ErrorIs(t, err, secret.ErrLocked)
	require.Empty(t, added)
}
