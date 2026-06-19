package authn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sipaha/outwall/internal/upstream"
)

type oidcClientCreds struct {
	hc       *http.Client
	tokenURL string
	clientID string
	secret   string
	scope    string

	mu      sync.Mutex
	token   string
	expires time.Time
}

// earlyRefresh refreshes a bit before actual expiry to avoid races at the boundary.
const earlyRefresh = 30 * time.Second

func (o *oidcClientCreds) Apply(r *http.Request) error {
	tok, err := o.fetch(r.Context())
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

func (o *oidcClientCreds) fetch(ctx context.Context) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.token != "" && time.Now().Before(o.expires.Add(-earlyRefresh)) {
		return o.token, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {o.clientID},
		"client_secret": {o.secret},
	}
	if o.scope != "" {
		form.Set("scope", o.scope)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("oidc token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("oidc token fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oidc token endpoint: status %d", resp.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("oidc token decode: %w", err)
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("oidc token endpoint returned empty access_token")
	}
	o.token = body.AccessToken
	ttl := time.Duration(body.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = time.Minute // conservative default when expires_in absent
	}
	o.expires = time.Now().Add(ttl)
	return o.token, nil
}

// Manager caches one Authenticator per upstream so OIDC tokens persist across requests.
type Manager struct {
	hc *http.Client
	mu sync.Mutex
	m  map[string]managed
}

type managed struct {
	fingerprint string
	auth        Authenticator
	transport   http.RoundTripper // nil for http upstreams
}

// NewManager constructs a Manager; nil hc uses http.DefaultClient.
func NewManager(hc *http.Client) *Manager {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Manager{hc: hc, m: map[string]managed{}}
}

func fingerprint(kind string, c upstream.AuthConfig) string {
	return strings.Join([]string{kind, c.Type, c.Header, c.Token, c.Username, c.Password,
		c.TokenURL, c.ClientID, c.ClientSecret, c.Scope,
		c.AWSAccessKeyID, c.AWSSecretAccessKey, c.AWSRegion, c.AWSService,
		c.HMACSecret, c.HMACHeader, c.HMACAlgo,
		c.CABundle, c.K8sAuth, c.ClientCert, c.ClientKey, c.ExecCommand,
		strings.Join(c.ExecArgs, "\x01"), execEnvFingerprint(c.ExecEnv)}, "\x00")
}

func execEnvFingerprint(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(env[k])
		b.WriteString("\x01")
	}
	return b.String()
}

// managedFor returns (rebuilding if needed) the cached entry for the upstream.
func (mgr *Manager) managedFor(up *upstream.Upstream) (managed, error) {
	fp := fingerprint(up.Kind, up.Auth)
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if cur, ok := mgr.m[up.ID]; ok && cur.fingerprint == fp {
		return cur, nil
	}
	a, tr, err := mgr.build(up.Kind, up.Auth)
	if err != nil {
		return managed{}, err
	}
	entry := managed{fingerprint: fp, auth: a, transport: tr}
	mgr.m[up.ID] = entry
	return entry, nil
}

// Authenticator returns a cached authenticator for the upstream, rebuilding it if the auth
// config changed since last time.
func (mgr *Manager) Authenticator(up *upstream.Upstream) (Authenticator, error) {
	entry, err := mgr.managedFor(up)
	if err != nil {
		return nil, err
	}
	return entry.auth, nil
}

// Transport returns the RoundTripper to reach this target's real backend. For Kind=="k8s"
// it is a transport whose tls.Config trusts AuthConfig.CABundle and (for client-cert auth)
// presents the client cert. For http upstreams it returns nil (the proxy uses the default
// transport). Cached per target alongside the Authenticator, keyed on the config fingerprint.
func (mgr *Manager) Transport(up *upstream.Upstream) (http.RoundTripper, error) {
	entry, err := mgr.managedFor(up)
	if err != nil {
		return nil, err
	}
	return entry.transport, nil
}

func (mgr *Manager) build(kind string, cfg upstream.AuthConfig) (Authenticator, http.RoundTripper, error) {
	if kind == upstream.KindK8s {
		a, err := buildK8sAuth(cfg)
		if err != nil {
			return nil, nil, err
		}
		tr, err := k8sTransport(cfg)
		if err != nil {
			return nil, nil, err
		}
		return a, tr, nil
	}
	if cfg.Type == "oidc-client-credentials" {
		if cfg.TokenURL == "" || cfg.ClientID == "" {
			return nil, nil, fmt.Errorf("oidc-client-credentials: token_url and client_id required")
		}
		return &oidcClientCreds{hc: mgr.hc, tokenURL: cfg.TokenURL, clientID: cfg.ClientID,
			secret: cfg.ClientSecret, scope: cfg.Scope}, nil, nil
	}
	if cfg.Type == "mtls" {
		// mTLS needs a per-upstream TLS transport (client cert); the authenticator is a no-op.
		tr, err := mtlsTransport(cfg)
		if err != nil {
			return nil, nil, err
		}
		return noneAuth{}, tr, nil
	}
	a, err := For(cfg) // stateless types (incl. sigv4/hmac, which need no transport)
	return a, nil, err
}
