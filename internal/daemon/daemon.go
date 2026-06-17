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

	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/grant"
	"github.com/Sipaha/outwall/internal/proxy"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
	"github.com/Sipaha/outwall/internal/upstream"
)

// Config holds daemon paths/addresses.
type Config struct {
	DBPath     string
	SocketPath string
	Listen     string // data-plane TCP listen address, e.g. 127.0.0.1:8080
}

// Daemon owns the running gateway.
type Daemon struct {
	cfg       Config
	store     *store.Store
	vault     *secret.Vault
	agents    *agent.Registry
	upstreams *upstream.Registry
	grants    *grant.Registry
	dataPlane http.Handler
}

// New constructs a Daemon (does not start listeners).
func New(cfg Config) (*Daemon, error) {
	s, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	v := secret.NewVault(s)
	ag := agent.NewRegistry(s)
	up := upstream.NewRegistry(s, v)
	gr := grant.NewRegistry(s)
	d := &Daemon{
		cfg: cfg, store: s, vault: v, agents: ag, upstreams: up, grants: gr,
		dataPlane: proxy.New(proxy.Deps{Agents: ag, Upstreams: up, Grants: gr, Vault: v}),
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

	errc := make(chan error, 2)
	go func() { errc <- adminSrv.Serve(ln) }()
	go func() { errc <- dataSrv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		_ = adminSrv.Close()
		_ = dataSrv.Close()
		_ = os.Remove(d.cfg.SocketPath)
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
