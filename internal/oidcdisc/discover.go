// Package oidcdisc fetches an OpenID Connect provider's discovery document
// (`{issuer}/.well-known/openid-configuration`) so the operator can auto-fill the OIDC endpoints
// instead of typing them. It is a generic OIDC client — no provider-specific assumptions.
package oidcdisc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// wellKnownPath is the standard discovery sub-path appended to an issuer URL (OpenID Connect
// Discovery 1.0, §4).
const wellKnownPath = "/.well-known/openid-configuration"

// maxBody caps the discovery document read (these documents are a few KB; a huge response is junk).
const maxBody = 1 << 20

// Config is the subset of the discovery document outwall uses to populate the host form.
type Config struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	EndSessionEndpoint    string   `json:"end_session_endpoint,omitempty"`
	ScopesSupported       []string `json:"scopes_supported,omitempty"`
}

// DiscoveryURL normalizes an operator-entered URL to the discovery-document URL: a URL that already
// points at the well-known document is used as-is; otherwise it is treated as the issuer and the
// well-known path is appended (trailing slash trimmed). The input must be an absolute http(s) URL.
func DiscoveryURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("issuer/discovery URL is required")
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return "", fmt.Errorf("URL must start with http:// or https://")
	}
	if strings.Contains(raw, wellKnownPath) {
		return raw, nil
	}
	return strings.TrimRight(raw, "/") + wellKnownPath, nil
}

// Discover fetches and parses the discovery document at the issuer/discovery URL using hc, returning
// the endpoints outwall needs. It errors when the document cannot be fetched/parsed or lacks the
// authorization/token endpoints.
func Discover(ctx context.Context, hc *http.Client, raw string) (Config, error) {
	url, err := DiscoveryURL(raw)
	if err != nil {
		return Config{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Config{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return Config{}, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Config{}, fmt.Errorf("discovery at %s returned HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return Config{}, fmt.Errorf("read discovery document: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(body, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse discovery document: %w", err)
	}
	if cfg.AuthorizationEndpoint == "" || cfg.TokenEndpoint == "" {
		return Config{}, fmt.Errorf("discovery document missing authorization_endpoint or token_endpoint")
	}
	return cfg, nil
}
