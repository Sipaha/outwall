// Package authn injects upstream credentials into proxied requests.
// Authenticator is the pluggable seam for future schemes (OIDC, mTLS, SigV4, HMAC).
package authn

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/Sipaha/outwall/internal/upstream"
)

// ErrUnsupported is returned for an unknown auth type.
var ErrUnsupported = errors.New("unsupported auth type")

// Authenticator mutates an outgoing request to add upstream credentials.
type Authenticator interface {
	Apply(req *http.Request) error
}

// For builds an Authenticator from an upstream's auth config.
func For(cfg upstream.AuthConfig) (Authenticator, error) {
	switch cfg.Type {
	case "none", "":
		return noneAuth{}, nil
	case "static":
		if cfg.Header == "" {
			return nil, fmt.Errorf("static auth: empty header")
		}
		return staticAuth{header: cfg.Header, token: cfg.Token}, nil
	case "basic":
		return basicAuth{user: cfg.Username, pass: cfg.Password}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupported, cfg.Type)
	}
}

type noneAuth struct{}

func (noneAuth) Apply(*http.Request) error { return nil }

type staticAuth struct{ header, token string }

func (s staticAuth) Apply(r *http.Request) error {
	r.Header.Set(s.header, s.token)
	return nil
}

type basicAuth struct{ user, pass string }

func (b basicAuth) Apply(r *http.Request) error {
	r.SetBasicAuth(b.user, b.pass)
	return nil
}
