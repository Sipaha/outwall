// Package daemon wires the store, vault, registries, and data-plane proxy together
// and serves the data plane (TCP localhost) plus an admin API (unix socket).
package daemon

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Sipaha/outwall/internal/access"
	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/approval"
	"github.com/Sipaha/outwall/internal/audit"
	"github.com/Sipaha/outwall/internal/authn"
	"github.com/Sipaha/outwall/internal/events"
	"github.com/Sipaha/outwall/internal/k8s"
	owmcp "github.com/Sipaha/outwall/internal/mcp"
	"github.com/Sipaha/outwall/internal/mcpsvc"
	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/proxy"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
	"github.com/Sipaha/outwall/internal/tlsca"
	"github.com/Sipaha/outwall/internal/upstream"
)

// DefaultListen is the default localhost address for the data-plane listener.
const DefaultListen = "127.0.0.1:8080"

// DefaultMCPListen is the default localhost address for the MCP control-plane listener.
const DefaultMCPListen = "127.0.0.1:8181"

// DefaultUIListen is the default localhost address for the desktop-UI control API + SSE listener.
const DefaultUIListen = "127.0.0.1:8182"

// DefaultCallbackListen is the default localhost address for the OIDC browser-login callback
// listener. It is a fixed, dedicated loopback port so the redirect URI
// (http://127.0.0.1:23312/callback) is stable and can be registered once in the IdP, independent of
// the (possibly customized) UI port.
const DefaultCallbackListen = "127.0.0.1:23312"

// DefaultBrowseDomain is the default base domain for per-upstream browser origins.
const DefaultBrowseDomain = "outwall.localhost"

// DefaultPruneInterval is how often the background audit pruner enforces the retention setting.
const DefaultPruneInterval = time.Hour

// Config holds daemon paths/addresses.
type Config struct {
	DBPath     string
	SocketPath string
	Listen     string // data-plane TCP listen address, e.g. 127.0.0.1:8080
	MCPListen  string // MCP control-plane TCP listen address, e.g. 127.0.0.1:8181
	UIListen   string // desktop-UI control API + SSE TCP listen address, e.g. 127.0.0.1:8182
	// CallbackListen is the dedicated loopback bind for the OIDC browser-login callback
	// (default DefaultCallbackListen). Its /callback path is the redirect URI registered in the IdP.
	CallbackListen string
	CADir          string // local-CA dir; defaults to the DB's directory when empty

	// BrowseDomain is the base domain for per-upstream browser origins: an http upstream <name> is
	// reachable at https://<name>.<BrowseDomain>:<port>/ (Host-routed). Default "outwall.localhost"
	// (Chromium resolves *.localhost to loopback). The path-prefix model on 127.0.0.1 is unchanged.
	BrowseDomain string

	// PruneInterval is how often the background audit pruner enforces the stored retention. Zero
	// uses DefaultPruneInterval; a negative value disables the pruner (tests / keep-all).
	PruneInterval time.Duration

	// OnFocusRequest, when non-nil, is invoked by the POST /desktop/focus admin route to
	// raise the desktop window. The desktop wrapper sets it; a headless serve leaves it nil
	// (the route then answers non-2xx — there is no window to focus). Single-instance gate
	// (ADR-0013): a second launch posts here over the unix socket to focus the running app.
	OnFocusRequest func()

	// OpenURL, when non-nil, opens a URL in the operator's system browser. The desktop wrapper
	// sets it (to internal/browser.Open) because the embedded webview drops window.open; the OIDC
	// login handler uses it so "Log in" works in the desktop app. Nil in browser/headless mode —
	// there the front-end's window.open reaches the real browser (ADR-0021).
	OpenURL func(string) error
}

// Daemon owns the running gateway.
type Daemon struct {
	cfg         Config
	store       *store.Store
	vault       *secret.Vault
	agents      *agent.Registry
	upstreams   *upstream.Registry
	policy      *policy.Registry
	access      *access.Registry
	approvals   *approval.Queue
	audit       *audit.Recorder
	bus         *events.Bus
	ca          *tlsca.CA
	importer    *k8s.Importer
	authManager *authn.Manager
	oauthLogins *oauthLogins
	callback    *callbackServer // on-demand OIDC callback listener (up only during a login)
	dataPlane   http.Handler
	mcp         http.Handler
}

// publish emits a domain event onto the daemon bus (nil-safe).
func (d *Daemon) publish(eventType string, data any) {
	if d.bus != nil {
		d.bus.Publish(eventType, data)
	}
}

// New constructs a Daemon (does not start listeners).
func New(cfg Config) (*Daemon, error) {
	if cfg.MCPListen == "" {
		cfg.MCPListen = DefaultMCPListen
	}
	if cfg.UIListen == "" {
		cfg.UIListen = DefaultUIListen
	}
	if cfg.CallbackListen == "" {
		cfg.CallbackListen = DefaultCallbackListen
	}
	if cfg.Listen == "" {
		cfg.Listen = DefaultListen
	}
	if cfg.BrowseDomain == "" {
		cfg.BrowseDomain = DefaultBrowseDomain
	}
	if cfg.CADir == "" {
		cfg.CADir = filepath.Dir(cfg.DBPath)
	}
	ca, err := tlsca.LoadOrCreateCA(cfg.CADir)
	if err != nil {
		return nil, fmt.Errorf("load local CA: %w", err)
	}
	s, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	v := secret.NewVault(s)
	ag := agent.NewRegistry(s)
	up := upstream.NewRegistry(s, v)
	pol := policy.NewRegistry(s)
	acc := access.NewRegistry(s)
	appr := approval.NewQueue()
	aud := audit.NewRecorder(s)
	bus := events.NewBus()
	appr.SetPublisher(bus)
	aud.SetPublisher(bus)
	svc := mcpsvc.New(ag, up, pol, acc)
	svc.SetPublisher(bus)
	svc.SetEvents(bus.Subscribe)
	svc.SetApprovals(appr)
	svc.SetKubeconfigParams("https://"+cfg.Listen, string(ca.CAPEM()))
	mcpHandler, err := owmcp.NewHandler(owmcp.Deps{
		Svc: svc, Agents: ag, Locked: v.Locked,
	})
	if err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("build mcp handler: %w", err)
	}
	authMgr := authn.NewManager(nil)
	d := &Daemon{
		cfg: cfg, store: s, vault: v, agents: ag, upstreams: up, policy: pol, access: acc,
		approvals: appr, audit: aud, bus: bus, ca: ca,
		importer:    &k8s.Importer{Reg: up, Log: slog.Default()},
		authManager: authMgr,
		oauthLogins: newOAuthLogins(),
		dataPlane: proxy.New(proxy.Deps{
			Agents: ag, Upstreams: up, Policy: pol, Limiter: policy.NewLimiter(),
			Approvals: appr, AuthManager: authMgr, Vault: v, Audit: aud,
		}),
		mcp: mcpHandler,
	}
	// On-demand OIDC callback listener: started by hOAuthLogin, stopped after the callback (or the
	// login TTL) — so the fixed port (cfg.CallbackListen) is only bound while a login is in flight.
	cbMux := http.NewServeMux()
	cbMux.HandleFunc("/callback", d.hOAuthCallback)
	d.callback = newCallbackServer(cfg.CallbackListen, cbMux)
	// Persist refreshed oidc-authorization-code tokens back to the vault so a rotated refresh
	// token survives a restart (the agent never sees any of this).
	authMgr.SetOAuthPersister(d.persistOAuthTokens)
	return d, nil
}

// Close releases resources.
func (d *Daemon) Close() error { return d.store.Close() }

// Subscribe returns a channel of domain events plus a cancel func (see events.Bus.Subscribe). The
// desktop wrapper uses it in-process to raise OS notifications (e.g. on approval.enqueued) without
// going through the SSE HTTP stream.
func (d *Daemon) Subscribe() (<-chan events.Event, func()) { return d.bus.Subscribe() }

// CAPEM returns the local CA certificate (PEM) the data plane is served with.
func (d *Daemon) CAPEM() []byte { return d.ca.CAPEM() }

// DataPlaneURL returns the https base URL of the data plane (no trailing slash).
func (d *Daemon) DataPlaneURL() string { return "https://" + d.cfg.Listen }

// Serve starts the data-plane and admin listeners until ctx is canceled.
func (d *Daemon) Serve(ctx context.Context) error {
	_ = os.Remove(d.cfg.SocketPath)
	ln, err := net.Listen("unix", d.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen unix: %w", err)
	}
	if err := os.Chmod(d.cfg.SocketPath, 0o600); err != nil {
		return fmt.Errorf("chmod socket: %w", err)
	}
	// The data plane is served over TLS using the local-CA-issued server cert so kubectl
	// validates the proxy honestly (the agent kubeconfig embeds the same CA). HTTP upstreams
	// reach the same TLS endpoint at https://127.0.0.1:PORT/<upstream>/...
	serverCert, err := d.ca.ServerCert("127.0.0.1", "localhost", "::1")
	if err != nil {
		return fmt.Errorf("issue data-plane server cert: %w", err)
	}
	dataTLS := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   tls.VersionTLS12,
	}

	adminSrv := &http.Server{Handler: d.AdminHandler()}
	dataSrv := &http.Server{Addr: d.cfg.Listen, Handler: d.dataPlane, TLSConfig: dataTLS}
	mcpSrv := &http.Server{Addr: d.cfg.MCPListen, Handler: d.mcp}
	uiSrv := &http.Server{Addr: d.cfg.UIListen, Handler: d.UIHandler()}
	// The OIDC callback listener (d.callback) is started on demand by hOAuthLogin and stopped after
	// the callback / login TTL — it is NOT bound here, so the fixed port stays free while idle.

	// Background audit pruner: enforces the stored retention setting until ctx is canceled.
	pruneInterval := d.cfg.PruneInterval
	if pruneInterval == 0 {
		pruneInterval = DefaultPruneInterval
	}
	if pruneInterval > 0 {
		go d.audit.RunPruner(ctx, pruneInterval)
	}

	errc := make(chan error, 4)
	go func() { errc <- adminSrv.Serve(ln) }()
	go func() { errc <- dataSrv.ListenAndServeTLS("", "") }()
	go func() { errc <- mcpSrv.ListenAndServe() }()
	go func() { errc <- uiSrv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		_ = adminSrv.Close()
		_ = dataSrv.Close()
		_ = mcpSrv.Close()
		_ = uiSrv.Close()
		d.callback.shutdown()
		_ = os.Remove(d.cfg.SocketPath)
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
