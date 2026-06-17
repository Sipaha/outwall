// Package proxy is the data plane: a localhost reverse proxy that authenticates the
// calling agent, enforces default-deny, injects upstream credentials, and forwards.
package proxy

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/authn"
	"github.com/Sipaha/outwall/internal/grant"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/upstream"
)

// Deps are the data-plane dependencies.
type Deps struct {
	Agents    *agent.Registry
	Upstreams *upstream.Registry
	Grants    *grant.Registry
	Vault     *secret.Vault
	Logger    *slog.Logger
}

type handler struct {
	Deps
}

// New builds the data-plane HTTP handler.
func New(d Deps) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &handler{Deps: d}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Vault.Locked() {
		writeErr(w, http.StatusServiceUnavailable, "vault locked")
		return
	}

	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, "Bearer ") {
		writeErr(w, http.StatusUnauthorized, "missing bearer token")
		return
	}
	token := strings.TrimPrefix(authz, "Bearer ")
	ag, err := h.Agents.Authenticate(token)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid token")
		return
	}

	// Split "/<upstream>/<rest...>".
	trimmed := strings.TrimPrefix(r.URL.Path, "/")
	name, rest, _ := strings.Cut(trimmed, "/")
	if name == "" {
		writeErr(w, http.StatusNotFound, "no upstream in path")
		return
	}
	up, err := h.Upstreams.GetByName(name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "unknown upstream")
		return
	}

	allowed, err := h.Grants.Allowed(ag.ID, up.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "policy error")
		return
	}
	if !allowed {
		writeErr(w, http.StatusForbidden, "access denied")
		return
	}

	base, err := url.Parse(up.BaseURL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "bad upstream url")
		return
	}
	auth, err := authn.For(up.Auth)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "auth config error")
		return
	}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = base.Scheme
			pr.Out.URL.Host = base.Host
			pr.Out.Host = base.Host
			pr.Out.URL.Path = singleJoin(base.Path, rest)
			pr.Out.URL.RawQuery = r.URL.RawQuery
			pr.Out.Header.Del("Authorization") // never forward the agent's token
			if err := auth.Apply(pr.Out); err != nil {
				h.Logger.Error("apply upstream auth", "upstream", up.Name, "err", err)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			h.Logger.Error("proxy upstream", "upstream", up.Name, "err", err)
			writeErr(w, http.StatusBadGateway, "upstream error")
		},
	}
	rp.ServeHTTP(w, r)
}

func singleJoin(a, b string) string {
	a = strings.TrimSuffix(a, "/")
	b = strings.TrimPrefix(b, "/")
	if b == "" {
		if a == "" {
			return "/"
		}
		return a
	}
	return a + "/" + b
}
