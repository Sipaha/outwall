package authn

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"

	"github.com/Sipaha/outwall/internal/upstream"
)

// k8sTransport builds the per-cluster http.RoundTripper that reaches the real API server:
// its tls.Config trusts AuthConfig.CABundle and, for client-cert auth, presents the client
// certificate (mTLS). Returns an *http.Transport so the proxy can attach it per request.
func k8sTransport(cfg upstream.AuthConfig) (*http.Transport, error) {
	tlsConf := &tls.Config{MinVersion: tls.VersionTLS12}
	switch {
	case cfg.CABundle != "":
		// Trust the cluster's CA (the normal path). The CA always wins over the insecure flag.
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(cfg.CABundle)) {
			return nil, fmt.Errorf("k8s ca_bundle: no valid PEM certificates")
		}
		tlsConf.RootCAs = pool
	case cfg.K8sInsecureSkipVerify:
		// SECURITY: verification is disabled ONLY because the operator's own kubeconfig carried
		// an explicit insecure-skip-tls-verify:true for this cluster — outwall mirrors the trust
		// decision they already made (exactly as kubectl does), never as a default and never to
		// paper over a CA error. The Clusters UI marks such a cluster with a red "insecure" badge.
		tlsConf.InsecureSkipVerify = true
	}
	if cfg.K8sAuth == "client-cert" {
		if cfg.ClientCert == "" || cfg.ClientKey == "" {
			return nil, fmt.Errorf("k8s client-cert auth: client_cert and client_key required")
		}
		cert, err := tls.X509KeyPair([]byte(cfg.ClientCert), []byte(cfg.ClientKey))
		if err != nil {
			return nil, fmt.Errorf("k8s client cert/key: %w", err)
		}
		tlsConf.Certificates = []tls.Certificate{cert}
	}
	return &http.Transport{TLSClientConfig: tlsConf}, nil
}

// staticBearer injects a fixed bearer token (used for k8s token auth).
type staticBearer struct{ token string }

func (s staticBearer) Apply(r *http.Request) error {
	r.Header.Set("Authorization", "Bearer "+s.token)
	return nil
}

// execBearer injects the exec-plugin's (cached) token as a bearer.
type execBearer struct{ src *execTokenSource }

func (e execBearer) Apply(r *http.Request) error {
	tok, err := e.src.Token(r.Context())
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

// buildK8sAuth builds the header-injecting Authenticator for a k8s cluster target.
//   - token       → static bearer
//   - exec        → exec-plugin bearer (cached)
//   - client-cert → no header (identity is carried by the transport's client cert)
func buildK8sAuth(cfg upstream.AuthConfig) (Authenticator, error) {
	switch cfg.K8sAuth {
	case "token":
		if cfg.Token == "" {
			return nil, fmt.Errorf("k8s token auth: token required")
		}
		return staticBearer{token: cfg.Token}, nil
	case "exec":
		if cfg.ExecCommand == "" {
			return nil, fmt.Errorf("k8s exec auth: exec_command required")
		}
		return execBearer{src: newExecTokenSource(cfg.ExecCommand, cfg.ExecArgs, cfg.ExecEnv)}, nil
	case "client-cert":
		return noneAuth{}, nil
	default:
		return nil, fmt.Errorf("%w: k8s_auth %q", ErrUnsupported, cfg.K8sAuth)
	}
}
