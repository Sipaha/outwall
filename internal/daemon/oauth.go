package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Sipaha/outwall/internal/authn"
	"github.com/Sipaha/outwall/internal/upstream"
)

// callbackServer is the on-demand OIDC callback listener. It binds the fixed callback port only
// while at least one browser login is in flight (ref-counted via acquire/release), so the port is
// free when idle (a login can also be abandoned — release fires on the login TTL too).
type callbackServer struct {
	addr    string
	handler http.Handler
	mu      sync.Mutex
	srv     *http.Server
	active  int
}

func newCallbackServer(addr string, handler http.Handler) *callbackServer {
	return &callbackServer{addr: addr, handler: handler}
}

// acquire starts the listener if it is not already running and registers one in-flight login. The
// returned release (idempotent) deregisters and, when no logins remain, shuts the listener down.
func (c *callbackServer) acquire() (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.srv == nil {
		ln, err := net.Listen("tcp", c.addr)
		if err != nil {
			return nil, err
		}
		srv := &http.Server{Handler: c.handler}
		c.srv = srv
		go func() { _ = srv.Serve(ln) }()
	}
	c.active++
	var once sync.Once
	return func() { once.Do(c.release) }, nil
}

func (c *callbackServer) release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active > 0 {
		c.active--
	}
	if c.active == 0 && c.srv != nil {
		_ = c.srv.Close()
		c.srv = nil
	}
}

// running reports whether the listener is currently bound (a login is in flight).
func (c *callbackServer) running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.srv != nil
}

// shutdown stops the listener regardless of in-flight count (daemon stop).
func (c *callbackServer) shutdown() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.srv != nil {
		_ = c.srv.Close()
		c.srv = nil
		c.active = 0
	}
}

// oauthLoginTTL bounds how long a started browser login may stay pending before its state expires.
const oauthLoginTTL = 10 * time.Minute

// oauthLogin is an in-flight authorization-code login awaiting the IdP redirect.
type oauthLogin struct {
	upstreamID string
	verifier   string
	expiry     time.Time
	// release frees the on-demand callback listener for this login (idempotent). Called when the
	// callback arrives or the login TTL expires.
	release func()
}

// oauthLogins is the in-memory store of pending logins, keyed by the CSRF state value.
type oauthLogins struct {
	mu sync.Mutex
	m  map[string]oauthLogin
}

func newOAuthLogins() *oauthLogins { return &oauthLogins{m: map[string]oauthLogin{}} }

func (s *oauthLogins) put(state string, l oauthLogin) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[state] = l
}

// take returns and removes the pending login for state, dropping it if expired.
func (s *oauthLogins) take(state string, now time.Time) (oauthLogin, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.m[state]
	if !ok {
		return oauthLogin{}, false
	}
	delete(s.m, state)
	if now.After(l.expiry) {
		return oauthLogin{}, false
	}
	return l, true
}

func randToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// defaultRedirectURI is the fixed loopback callback the OIDC flow uses when the operator did not set
// a custom RedirectURL — served by the dedicated callback listener (Serve). This exact value must be
// registered as a redirect URI in the IdP client.
func (d *Daemon) defaultRedirectURI() string {
	return "http://" + d.cfg.CallbackListen + "/callback"
}

// effectiveOAuthCfg returns the upstream auth config with a defaulted RedirectURL (the dedicated
// callback listener's /callback) when the operator did not set one. The same value MUST be used for
// the authorize URL and the token exchange, so both handlers go through here.
func (d *Daemon) effectiveOAuthCfg(cfg upstream.AuthConfig) upstream.AuthConfig {
	if cfg.RedirectURL == "" {
		cfg.RedirectURL = d.defaultRedirectURI()
	}
	return cfg
}

// hOIDCRedirectURI returns the redirect URI the operator must register in their IdP, so the UI can
// show it on the OIDC host form.
func (d *Daemon) hOIDCRedirectURI(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"redirect_uri": d.defaultRedirectURI()})
}

// hOAuthLogin (POST /upstreams/{name}/oauth/login) starts a browser authorization-code login: it
// mints a CSRF state + PKCE verifier, parks them, and returns the IdP authorize URL for the UI to
// open. The token never touches the agent — outwall holds it and injects it on the data plane.
func (d *Daemon) hOAuthLogin(w http.ResponseWriter, r *http.Request) {
	up, err := d.upstreams.GetByName(r.PathValue("name"))
	if errors.Is(err, upstream.ErrNotFound) {
		adminErr(w, http.StatusNotFound, "unknown upstream")
		return
	}
	if err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if up.Auth.Type != "oidc-authorization-code" {
		adminErr(w, http.StatusBadRequest, "upstream is not oidc-authorization-code")
		return
	}
	cfg := d.effectiveOAuthCfg(up.Auth)
	state, err := randToken()
	if err != nil {
		adminErr(w, http.StatusInternalServerError, "state gen")
		return
	}
	// Bring up the callback listener for the duration of this login (released on callback or TTL).
	release, err := d.callback.acquire()
	if err != nil {
		adminErr(w, http.StatusInternalServerError, "start callback listener: "+err.Error())
		return
	}
	time.AfterFunc(oauthLoginTTL, release) // free the listener if the login is abandoned
	verifier := authn.GenerateVerifier()
	d.oauthLogins.put(state, oauthLogin{
		upstreamID: up.ID, verifier: verifier, expiry: time.Now().Add(oauthLoginTTL), release: release,
	})
	authURL := authn.AuthCodeURL(cfg, state, verifier)
	// In the desktop app the embedded webview drops window.open, so open the system browser from
	// here. In browser/headless mode OpenURL is nil and the front-end's window.open does the job.
	opened := false
	if d.cfg.OpenURL != nil {
		if err := d.cfg.OpenURL(authURL); err != nil {
			slog.Error("oauth: open system browser", "err", err)
		} else {
			opened = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": authURL, "opened": opened})
}

// hOAuthCallback (GET /oauth/callback) is the IdP redirect target. It exchanges the code (with the
// stored PKCE verifier) for tokens and persists them encrypted on the upstream, then shows a small
// close-this-tab page. It is served top-level on the UI listener and is CSRF-exempt (a browser
// redirect cannot carry the X-Outwall-CSRF header), which is safe: the random state ties the
// callback to a login this daemon started.
func (d *Daemon) hOAuthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		oauthResultPage(w, http.StatusBadRequest, "Login failed: "+e)
		return
	}
	state, code := q.Get("state"), q.Get("code")
	login, ok := d.oauthLogins.take(state, time.Now())
	if !ok {
		oauthResultPage(w, http.StatusBadRequest, "Login expired or unknown — start again from outwall.")
		return
	}
	if login.release != nil {
		defer login.release() // free the callback listener now the login is resolved
	}
	up, err := d.upstreams.GetByID(login.upstreamID)
	if err != nil {
		oauthResultPage(w, http.StatusBadRequest, "Upstream no longer exists.")
		return
	}
	cfg := d.effectiveOAuthCfg(up.Auth)
	toks, err := authn.ExchangeCode(r.Context(), cfg, code, login.verifier)
	if err != nil {
		oauthResultPage(w, http.StatusBadGateway, "Token exchange failed: "+err.Error())
		return
	}
	d.persistOAuthTokens(up.ID, toks)
	d.publish("upstream.created", map[string]any{"id": up.ID, "name": up.Name, "kind": up.Kind})
	oauthResultPage(w, http.StatusOK, "Login complete — you can close this tab.")
}

// persistOAuthTokens writes refreshed/obtained tokens back onto the upstream's encrypted auth
// config, preserving the rest of the config. Used by both the login callback and the refresh hook.
func (d *Daemon) persistOAuthTokens(upstreamID string, t authn.Tokens) {
	up, err := d.upstreams.GetByID(upstreamID)
	if err != nil {
		slog.Error("oauth persist: load upstream", "id", upstreamID, "err", err)
		return
	}
	cfg := up.Auth
	cfg.AccessToken = t.AccessToken
	if t.RefreshToken != "" {
		cfg.RefreshToken = t.RefreshToken // some IdPs omit it on refresh — keep the old one then
	}
	if !t.Expiry.IsZero() {
		cfg.TokenExpiry = t.Expiry.UTC().Format(time.RFC3339)
	}
	if err := d.upstreams.SetAuth(upstreamID, cfg); err != nil {
		slog.Error("oauth persist: set auth", "id", upstreamID, "err", err)
	}
}

func oauthResultPage(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.WriteHeader(code)
	// msg can include attacker-influenced text (the IdP `error` param echoed on the redirect), so
	// HTML-escape it before reflecting it into the page (reflected-XSS guard).
	_, _ = io.WriteString(w, "<!doctype html><html><body style=\"font-family:sans-serif;background:#1e1f22;color:#ddd;padding:2rem\"><h2>outwall</h2><p>"+html.EscapeString(msg)+"</p></body></html>")
}
