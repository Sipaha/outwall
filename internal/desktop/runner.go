// Package desktop runs the outwall daemon in-process for the desktop wrapper.
//
// outwall has no Docker or upgrade contract, so supervising the daemon as a
// separate child process buys nothing: the daemon runs in the SAME process as
// the Wails GUI shell. This file is the CGO-free, Wails-free, build-tag-free
// seam between the two — it starts daemon.Serve in a goroutine, waits for the
// UI bind to answer, and hands back the URL plus a stop func. Keeping it free of
// CGO/Wails/build tags means it is covered by the normal `go test ./...` gate
// (see ADR-0007).
package desktop

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Sipaha/outwall/internal/daemon"
	"github.com/Sipaha/outwall/internal/events"
)

// readyTimeout bounds how long Run waits for the UIListen bind to answer 200
// before giving up and tearing the daemon back down.
const readyTimeout = 10 * time.Second

// pollInterval is the gap between readiness probes against the UI bind.
const pollInterval = 50 * time.Millisecond

// Handle is a running in-process daemon owned by the desktop wrapper. UIURL is
// the loopback address of the embedded UI; Stop tears the daemon down.
type Handle struct {
	// UIURL is http://<cfg.UIListen>/ — the address the webview loads.
	UIURL string

	cancel context.CancelFunc
	d      *daemon.Daemon

	// serveDone is closed by the goroutine running Serve once it returns. Closing
	// (rather than sending) makes it safe to wait on from multiple Stop calls.
	serveDone chan struct{}

	// stopOnce guards the one-time teardown (cancel + wait + Close) so Stop is
	// safely idempotent: a second call returns the first call's result without
	// re-waiting on an already-drained channel or double-closing the store.
	stopOnce sync.Once
	stopErr  error
}

// Run builds daemon.New(cfg), starts Serve in a goroutine, and polls the UI
// bind until it answers 200 (or readyTimeout elapses). On success it returns a
// Handle whose UIURL points at the embedded UI. On any failure it tears the
// half-started daemon down and returns the error, so the caller never leaks a
// goroutine or an open store.
func Run(cfg daemon.Config) (*Handle, error) {
	if cfg.UIListen == "" {
		cfg.UIListen = daemon.DefaultUIListen
	}
	d, err := daemon.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("build daemon: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	serveDone := make(chan struct{})
	go func() {
		serveErr <- d.Serve(ctx)
		close(serveDone)
	}()

	h := &Handle{
		UIURL:     "http://" + cfg.UIListen + "/",
		cancel:    cancel,
		d:         d,
		serveDone: serveDone,
	}

	if err := waitReady(h.UIURL, serveErr); err != nil {
		// Tear the half-started daemon down before surfacing the error so we
		// don't leak the Serve goroutine or the open store.
		stopCtx, stopCancel := context.WithTimeout(context.Background(), readyTimeout)
		defer stopCancel()
		_ = h.Stop(stopCtx)
		return nil, err
	}
	return h, nil
}

// waitReady polls uiURL until it answers HTTP 200 or readyTimeout elapses. If
// Serve returns early (a bind failure), it surfaces that error immediately
// rather than waiting out the full timeout.
func waitReady(uiURL string, serveErr <-chan error) error {
	client := &http.Client{Timeout: pollInterval}
	deadline := time.Now().Add(readyTimeout)
	for {
		select {
		case err := <-serveErr:
			if err != nil {
				return fmt.Errorf("daemon serve: %w", err)
			}
			return fmt.Errorf("daemon serve exited before UI became ready")
		default:
		}

		resp, err := client.Get(uiURL)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("ui at %s not ready after %s", uiURL, readyTimeout)
		}
		time.Sleep(pollInterval)
	}
}

// Subscribe returns a channel of the in-process daemon's domain events plus a cancel func, so the
// desktop wrapper can raise OS notifications (e.g. on approval.enqueued) directly.
func (h *Handle) Subscribe() (<-chan events.Event, func()) { return h.d.Subscribe() }

// Publish emits an event on the in-process daemon's event bus. The desktop wrapper uses it to push
// UI signals (e.g. "open approvals" on a notification click) to the SPA over the SSE stream.
func (h *Handle) Publish(eventType string, data any) { h.d.Publish(eventType, data) }

// Stop cancels the daemon context, waits (bounded by ctx) for Serve to return,
// and closes the store. It is safe — and idempotent — to call more than once:
// the teardown runs exactly once under stopOnce and later calls return the same
// result. If ctx expires before Serve returns, Stop reports that error and does
// not close the store (Serve is still using it); the caller may retry with a
// fresh ctx.
func (h *Handle) Stop(ctx context.Context) error {
	if h == nil {
		return nil
	}

	// A ctx that expires before the one-time teardown gets a chance to wait must
	// surface as an error without consuming stopOnce, so a retry with a fresh ctx
	// can still complete the shutdown.
	if h.cancel != nil {
		h.cancel()
	}
	select {
	case <-h.serveDone:
	case <-ctx.Done():
		return fmt.Errorf("waiting for daemon shutdown: %w", ctx.Err())
	}

	h.stopOnce.Do(func() {
		if h.d != nil {
			if err := h.d.Close(); err != nil {
				h.stopErr = fmt.Errorf("close daemon: %w", err)
			}
		}
	})
	return h.stopErr
}
