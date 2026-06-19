package authn

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"

	"github.com/Sipaha/outwall/internal/upstream"
)

// mtlsTransport builds an http.RoundTripper that presents the upstream's client certificate
// (mutual TLS) and, when a CA bundle is configured, trusts only it for the server cert. Used for
// http upstreams with auth type "mtls"; the credential is the X.509 key pair, so no header is
// injected (the authenticator is noneAuth). The transport is cached per upstream by the Manager
// (keyed on the auth fingerprint), mirroring the k8s client-cert transport seam (ADR-0008).
func mtlsTransport(cfg upstream.AuthConfig) (http.RoundTripper, error) {
	if cfg.ClientCert == "" || cfg.ClientKey == "" {
		return nil, fmt.Errorf("mtls: client_cert and client_key are required")
	}
	cert, err := tls.X509KeyPair([]byte(cfg.ClientCert), []byte(cfg.ClientKey))
	if err != nil {
		return nil, fmt.Errorf("mtls: parse client key pair: %w", err)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	if cfg.CABundle != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(cfg.CABundle)) {
			return nil, fmt.Errorf("mtls: ca_bundle contains no valid certificate")
		}
		tlsCfg.RootCAs = pool
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = tlsCfg
	return tr, nil
}
