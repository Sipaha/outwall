package tlsca

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadOrCreateCAIdempotent(t *testing.T) {
	dir := t.TempDir()

	ca1, err := LoadOrCreateCA(dir)
	require.NoError(t, err)
	require.NotEmpty(t, ca1.CAPEM())

	// Files exist with 0600 perms.
	for _, f := range []string{"ca.crt", "ca.key"} {
		info, err := os.Stat(filepath.Join(dir, f))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}

	ca2, err := LoadOrCreateCA(dir)
	require.NoError(t, err)
	require.Equal(t, string(ca1.CAPEM()), string(ca2.CAPEM()), "second load must reuse the same CA")
}

func TestServerCertVerifiesAgainstCA(t *testing.T) {
	dir := t.TempDir()
	ca, err := LoadOrCreateCA(dir)
	require.NoError(t, err)

	cert, err := ca.ServerCert("127.0.0.1", "localhost")
	require.NoError(t, err)
	require.NotEmpty(t, cert.Certificate)

	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(ca.CAPEM()))

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	require.NoError(t, err)
	_, err = leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "localhost"})
	require.NoError(t, err)

	_, err = leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "127.0.0.1"})
	require.NoError(t, err)
}
