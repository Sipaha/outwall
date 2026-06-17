package authn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/upstream"
)

// genCA returns a self-signed CA (cert PEM, *x509.Certificate, signer).
func genCA(t *testing.T) (caPEM []byte, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return caPEM, cert, key
}

// genLeaf signs a server leaf cert for cn under the given CA.
func genLeaf(t *testing.T, cn string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{cn},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert
}

func TestTransportK8sTrustsCA(t *testing.T) {
	caPEM, caCert, caKey := genCA(t)
	leaf := genLeaf(t, "api.k8s.test", caCert, caKey)

	mgr := NewManager(nil)
	up := &upstream.Upstream{
		ID:   "c1",
		Kind: upstream.KindK8s,
		Auth: upstream.AuthConfig{K8sAuth: "token", Token: "tok", CABundle: string(caPEM)},
	}
	rt, err := mgr.Transport(up)
	require.NoError(t, err)
	require.NotNil(t, rt)

	tr, ok := rt.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, tr.TLSClientConfig)
	require.NotNil(t, tr.TLSClientConfig.RootCAs)

	// A cert signed by our CA verifies against the transport's RootCAs.
	opts := x509.VerifyOptions{Roots: tr.TLSClientConfig.RootCAs, DNSName: "api.k8s.test"}
	_, err = leaf.Verify(opts)
	require.NoError(t, err, "leaf signed by the configured CA must verify")

	// An unrelated CA's leaf must NOT verify.
	_, otherCACert, otherCAKey := genCA(t)
	otherLeaf := genLeaf(t, "api.k8s.test", otherCACert, otherCAKey)
	_, err = otherLeaf.Verify(opts)
	require.Error(t, err, "leaf from an unrelated CA must be rejected")
}

func TestTransportHTTPIsNil(t *testing.T) {
	mgr := NewManager(nil)
	up := &upstream.Upstream{ID: "h1", Kind: upstream.KindHTTP, Auth: upstream.AuthConfig{Type: "none"}}
	rt, err := mgr.Transport(up)
	require.NoError(t, err)
	require.Nil(t, rt, "http upstreams use the default transport")
}

// genClientCertPEM returns (clientCertPEM, clientKeyPEM, caPEM) for client-cert auth tests.
func genClientCertPEM(t *testing.T) (string, string, string) {
	t.Helper()
	caPEM, caCert, caKey := genCA(t)
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "admin"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &clientKey.PublicKey, caKey)
	require.NoError(t, err)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(clientKey)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return string(certPEM), string(keyPEM), string(caPEM)
}

func TestTransportK8sClientCert(t *testing.T) {
	certPEM, keyPEM, caPEM := genClientCertPEM(t)
	mgr := NewManager(nil)
	up := &upstream.Upstream{
		ID:   "c2",
		Kind: upstream.KindK8s,
		Auth: upstream.AuthConfig{
			K8sAuth: "client-cert", CABundle: caPEM,
			ClientCert: certPEM, ClientKey: keyPEM,
		},
	}
	rt, err := mgr.Transport(up)
	require.NoError(t, err)
	tr := rt.(*http.Transport)
	require.Len(t, tr.TLSClientConfig.Certificates, 1)
}

func TestK8sTokenAuthInjectsBearer(t *testing.T) {
	mgr := NewManager(nil)

	tokUp := &upstream.Upstream{
		ID: "c3", Kind: upstream.KindK8s,
		Auth: upstream.AuthConfig{K8sAuth: "token", Token: "sa-secret"},
	}
	a, err := mgr.Authenticator(tokUp)
	require.NoError(t, err)
	req, _ := http.NewRequest("GET", "https://api/", nil)
	require.NoError(t, a.Apply(req))
	require.Equal(t, "Bearer sa-secret", req.Header.Get("Authorization"))

	certPEM, keyPEM, caPEM := genClientCertPEM(t)
	certUp := &upstream.Upstream{
		ID: "c4", Kind: upstream.KindK8s,
		Auth: upstream.AuthConfig{
			K8sAuth: "client-cert", CABundle: caPEM,
			ClientCert: certPEM, ClientKey: keyPEM,
		},
	}
	a2, err := mgr.Authenticator(certUp)
	require.NoError(t, err)
	req2, _ := http.NewRequest("GET", "https://api/", nil)
	require.NoError(t, a2.Apply(req2))
	require.Empty(t, req2.Header.Get("Authorization"), "client-cert auth must not set an Authorization header")
}

// silence unused tls import when test build flags vary.
var _ = tls.VersionTLS13
