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

func TestServerCertForCachesPerSNI(t *testing.T) {
	ca, err := LoadOrCreateCA(t.TempDir())
	require.NoError(t, err)

	c1, err := ca.ServerCertFor("be.outwall.localhost")
	require.NoError(t, err)
	require.Contains(t, c1.Leaf.DNSNames, "be.outwall.localhost")

	// A dotted name (an upstream host) is covered exactly (a one-label wildcard could not).
	c2, err := ca.ServerCertFor("enterprise.ecos24.ru.outwall.localhost")
	require.NoError(t, err)
	require.Contains(t, c2.Leaf.DNSNames, "enterprise.ecos24.ru.outwall.localhost")

	// Same SNI returns the cached identical cert (same leaf pointer / serial).
	c1b, err := ca.ServerCertFor("be.outwall.localhost")
	require.NoError(t, err)
	require.Equal(t, c1.Leaf.SerialNumber, c1b.Leaf.SerialNumber)
}
