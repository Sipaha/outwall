package authn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
}

// NewManager constructs a Manager; nil hc uses http.DefaultClient.
func NewManager(hc *http.Client) *Manager {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Manager{hc: hc, m: map[string]managed{}}
}

func fingerprint(c upstream.AuthConfig) string {
	return strings.Join([]string{c.Type, c.Header, c.Token, c.Username, c.Password,
		c.TokenURL, c.ClientID, c.ClientSecret, c.Scope}, "\x00")
}

// Authenticator returns a cached authenticator for the upstream, rebuilding it if the auth
// config changed since last time.
func (mgr *Manager) Authenticator(up *upstream.Upstream) (Authenticator, error) {
	fp := fingerprint(up.Auth)
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if cur, ok := mgr.m[up.ID]; ok && cur.fingerprint == fp {
		return cur.auth, nil
	}
	a, err := mgr.build(up.Auth)
	if err != nil {
		return nil, err
	}
	mgr.m[up.ID] = managed{fingerprint: fp, auth: a}
	return a, nil
}

func (mgr *Manager) build(cfg upstream.AuthConfig) (Authenticator, error) {
	if cfg.Type == "oidc-client-credentials" {
		if cfg.TokenURL == "" || cfg.ClientID == "" {
			return nil, fmt.Errorf("oidc-client-credentials: token_url and client_id required")
		}
		return &oidcClientCreds{hc: mgr.hc, tokenURL: cfg.TokenURL, clientID: cfg.ClientID,
			secret: cfg.ClientSecret, scope: cfg.Scope}, nil
	}
	return For(cfg) // stateless types
}
