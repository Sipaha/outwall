// Package daemon wires the store, vault, registries, and data-plane proxy together
// and serves the data plane (TCP localhost) plus an admin API (unix socket).
package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/Sipaha/outwall/internal/access"
	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/approval"
	"github.com/Sipaha/outwall/internal/audit"
	"github.com/Sipaha/outwall/internal/authn"
	"github.com/Sipaha/outwall/internal/events"
	owmcp "github.com/Sipaha/outwall/internal/mcp"
	"github.com/Sipaha/outwall/internal/mcpsvc"
	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/proxy"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
	"github.com/Sipaha/outwall/internal/upstream"
)

// DefaultMCPListen is the default localhost address for the MCP control-plane listener.
const DefaultMCPListen = "127.0.0.1:8181"

// DefaultUIListen is the default localhost address for the desktop-UI control API + SSE listener.
const DefaultUIListen = "127.0.0.1:8182"

// Config holds daemon paths/addresses.
type Config struct {
	DBPath     string
	SocketPath string
	Listen     string // data-plane TCP listen address, e.g. 127.0.0.1:8080
	MCPListen  string // MCP control-plane TCP listen address, e.g. 127.0.0.1:8181
	UIListen   string // desktop-UI control API + SSE TCP listen address, e.g. 127.0.0.1:8182
}

// Daemon owns the running gateway.
type Daemon struct {
	cfg       Config
	store     *store.Store
	vault     *secret.Vault
	agents    *agent.Registry
	upstreams *upstream.Registry
	policy    *policy.Registry
	access    *access.Registry
	approvals *approval.Queue
	audit     *audit.Recorder
	bus       *events.Bus
	dataPlane http.Handler
	mcp       http.Handler
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
	mcpHandler, err := owmcp.NewHandler(owmcp.Deps{
		Svc: svc, Agents: ag, Locked: v.Locked,
	})
	if err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("build mcp handler: %w", err)
	}
	d := &Daemon{
		cfg: cfg, store: s, vault: v, agents: ag, upstreams: up, policy: pol, access: acc,
		approvals: appr, audit: aud, bus: bus,
		dataPlane: proxy.New(proxy.Deps{
			Agents: ag, Upstreams: up, Policy: pol, Limiter: policy.NewLimiter(),
			Approvals: appr, AuthManager: authn.NewManager(nil), Vault: v, Audit: aud,
		}),
		mcp: mcpHandler,
	}
	return d, nil
}

// Close releases resources.
func (d *Daemon) Close() error { return d.store.Close() }

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
	adminSrv := &http.Server{Handler: d.AdminHandler()}
	dataSrv := &http.Server{Addr: d.cfg.Listen, Handler: d.dataPlane}
	mcpSrv := &http.Server{Addr: d.cfg.MCPListen, Handler: d.mcp}
	uiSrv := &http.Server{Addr: d.cfg.UIListen, Handler: d.UIHandler()}

	errc := make(chan error, 4)
	go func() { errc <- adminSrv.Serve(ln) }()
	go func() { errc <- dataSrv.ListenAndServe() }()
	go func() { errc <- mcpSrv.ListenAndServe() }()
	go func() { errc <- uiSrv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		_ = adminSrv.Close()
		_ = dataSrv.Close()
		_ = mcpSrv.Close()
		_ = uiSrv.Close()
		_ = os.Remove(d.cfg.SocketPath)
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
