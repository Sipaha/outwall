package authn

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/upstream"
)

func TestMTLSTransportPresentsClientCert(t *testing.T) {
	certPEM, keyPEM, caPEM := genClientCertPEM(t)
	mgr := NewManager(nil)
	up := &upstream.Upstream{
		ID:   "m1",
		Kind: upstream.KindHTTP,
		Auth: upstream.AuthConfig{Type: "mtls", ClientCert: certPEM, ClientKey: keyPEM, CABundle: caPEM},
	}
	rt, err := mgr.Transport(up)
	require.NoError(t, err)
	tr := rt.(*http.Transport)
	require.Len(t, tr.TLSClientConfig.Certificates, 1)
	require.NotNil(t, tr.TLSClientConfig.RootCAs)

	// The header authenticator for mtls is a no-op.
	a, err := mgr.Authenticator(up)
	require.NoError(t, err)
	req := httptest.NewRequest("GET", "https://example.com/x", nil)
	require.NoError(t, a.Apply(req))
	require.Empty(t, req.Header.Get("Authorization"))

	// Missing key → error.
	_, err = mtlsTransport(upstream.AuthConfig{Type: "mtls", ClientCert: certPEM})
	require.Error(t, err)
}

func TestSigV4SignsAndPreservesBody(t *testing.T) {
	a, err := newSigV4Auth(upstream.AuthConfig{
		Type: "sigv4", AWSAccessKeyID: "AKID", AWSSecretAccessKey: "SECRET",
		AWSRegion: "us-east-1", AWSService: "execute-api",
	})
	require.NoError(t, err)

	// Body-less GET signs with the empty payload hash.
	get := httptest.NewRequest("GET", "https://api.example.com/v1/ping", nil)
	require.NoError(t, a.Apply(get))
	require.True(t, strings.HasPrefix(get.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential="),
		"got %q", get.Header.Get("Authorization"))
	require.NotEmpty(t, get.Header.Get("X-Amz-Date"))

	// POST body is preserved after signing.
	post := httptest.NewRequest("POST", "https://api.example.com/v1/items", strings.NewReader(`{"a":1}`))
	require.NoError(t, a.Apply(post))
	require.NotEmpty(t, post.Header.Get("Authorization"))
	body, _ := io.ReadAll(post.Body)
	require.Equal(t, `{"a":1}`, string(body))

	// Missing region → error.
	_, err = newSigV4Auth(upstream.AuthConfig{Type: "sigv4", AWSAccessKeyID: "x", AWSSecretAccessKey: "y"})
	require.Error(t, err)
}

func TestHMACDeterministicSignature(t *testing.T) {
	a, err := newHMACAuth(upstream.AuthConfig{Type: "hmac", HMACSecret: "topsecret", HMACHeader: "X-Signature"})
	require.NoError(t, err)
	a.now = func() time.Time { return time.Unix(1_700_000_000, 0) }

	r1 := httptest.NewRequest("GET", "https://api.example.com/v1/x?q=1", nil)
	require.NoError(t, a.Apply(r1))
	sig1 := r1.Header.Get("X-Signature")
	require.NotEmpty(t, sig1)
	require.Equal(t, "1700000000", r1.Header.Get("X-Timestamp"))

	// Same inputs → same signature (determinism with a pinned clock).
	r2 := httptest.NewRequest("GET", "https://api.example.com/v1/x?q=1", nil)
	require.NoError(t, a.Apply(r2))
	require.Equal(t, sig1, r2.Header.Get("X-Signature"))

	// A different secret → a different signature.
	a2, err := newHMACAuth(upstream.AuthConfig{Type: "hmac", HMACSecret: "other", HMACHeader: "X-Signature"})
	require.NoError(t, err)
	a2.now = a.now
	r3 := httptest.NewRequest("GET", "https://api.example.com/v1/x?q=1", nil)
	require.NoError(t, a2.Apply(r3))
	require.NotEqual(t, sig1, r3.Header.Get("X-Signature"))

	// sha512 is selectable; empty secret/header → error.
	_, err = newHMACAuth(upstream.AuthConfig{Type: "hmac", HMACSecret: "s", HMACHeader: "H", HMACAlgo: "sha512"})
	require.NoError(t, err)
	_, err = newHMACAuth(upstream.AuthConfig{Type: "hmac", HMACHeader: "H"})
	require.Error(t, err)
}

func TestFingerprintIncludesNewFields(t *testing.T) {
	base := upstream.AuthConfig{Type: "sigv4", AWSAccessKeyID: "A", AWSSecretAccessKey: "B", AWSRegion: "r", AWSService: "s"}
	other := base
	other.AWSRegion = "eu-west-1"
	require.NotEqual(t, fingerprint("http", base), fingerprint("http", other))

	h1 := upstream.AuthConfig{Type: "hmac", HMACSecret: "x", HMACHeader: "H"}
	h2 := h1
	h2.HMACAlgo = "sha512"
	require.NotEqual(t, fingerprint("http", h1), fingerprint("http", h2))
}
