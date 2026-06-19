package authn

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/Sipaha/outwall/internal/upstream"
)

// Tokens is the persistable result of an authorization-code login / refresh.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
}

// OAuthConfig builds the oauth2.Config for an authorization-code upstream from its auth config.
func OAuthConfig(cfg upstream.AuthConfig) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     oauth2.Endpoint{AuthURL: cfg.AuthURL, TokenURL: cfg.TokenURL},
		RedirectURL:  cfg.RedirectURL,
		Scopes:       splitScopes(cfg.Scope),
	}
}

// splitScopes splits a space- or comma-separated scope string into a slice (empty → nil).
func splitScopes(s string) []string {
	f := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' })
	if len(f) == 0 {
		return nil
	}
	return f
}

// AuthCodeURL returns the IdP authorization URL for a login, binding the CSRF state and a PKCE
// (S256) challenge derived from verifier. The caller stores (state, verifier) until the callback.
func AuthCodeURL(cfg upstream.AuthConfig, state, verifier string) string {
	return OAuthConfig(cfg).AuthCodeURL(state,
		oauth2.AccessTypeOffline, // request a refresh token
		oauth2.S256ChallengeOption(verifier))
}

// ExchangeCode swaps an authorization code for tokens, sending the PKCE verifier.
func ExchangeCode(ctx context.Context, cfg upstream.AuthConfig, code, verifier string) (Tokens, error) {
	tok, err := OAuthConfig(cfg).Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Tokens{}, fmt.Errorf("oidc-ac: exchange code: %w", err)
	}
	return Tokens{AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, Expiry: tok.Expiry}, nil
}

// GenerateVerifier returns a fresh PKCE code verifier.
func GenerateVerifier() string { return oauth2.GenerateVerifier() }

// oidcAuthCode injects the upstream's user access token (obtained via a browser authorization-code
// login) and transparently refreshes it via the stored refresh token. When a refresh produces a new
// token it is handed to persist so the rotated refresh token survives a restart (ADR-0021).
type oidcAuthCode struct {
	mu      sync.Mutex
	src     oauth2.TokenSource
	persist func(Tokens) // nil ⇒ in-memory only
	last    string       // last seen access token, to detect a refresh
}

func newOIDCAuthCode(cfg upstream.AuthConfig, persist func(Tokens)) *oidcAuthCode {
	var expiry time.Time
	if cfg.TokenExpiry != "" {
		expiry, _ = time.Parse(time.RFC3339, cfg.TokenExpiry)
	}
	tok := &oauth2.Token{
		AccessToken:  cfg.AccessToken,
		RefreshToken: cfg.RefreshToken,
		Expiry:       expiry,
	}
	src := OAuthConfig(cfg).TokenSource(context.Background(), tok)
	return &oidcAuthCode{src: oauth2.ReuseTokenSource(tok, src), persist: persist, last: cfg.AccessToken}
}

func (o *oidcAuthCode) Apply(r *http.Request) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	tok, err := o.src.Token()
	if err != nil {
		return fmt.Errorf("oidc-ac: obtain token (re-login may be required): %w", err)
	}
	if tok.AccessToken != o.last && o.persist != nil {
		o.persist(Tokens{AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, Expiry: tok.Expiry})
	}
	o.last = tok.AccessToken
	tok.SetAuthHeader(r)
	return nil
}
