package proxy

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
)

// hijackAuditWriter wraps a ResponseWriter for an interactive k8s upgrade (exec/attach/
// portforward). It records the negotiated status code and, when httputil.ReverseProxy hijacks
// the connection to stream the duplex session, returns a countingConn that tallies the bytes
// flowing each way and fires onClose exactly once when the session connection is closed. The
// onClose callback writes the metadata audit record (no body blob).
//
// ReverseProxy only hijacks the client connection on a successful 101 upgrade; the metadata
// record is therefore emitted from the hijacked conn's Close — the session lifecycle, and the
// only path that streams bytes. If the upgrade never happens (e.g. the upstream returns an
// error before 101) no conn is handed out and no session record is written, which is correct:
// there was no interactive session to audit.
type hijackAuditWriter struct {
	http.ResponseWriter
	onClose func(in, out int64, status int)

	status int
}

func (w *hijackAuditWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying ResponseWriter when it supports flushing (ReverseProxy uses
// this for streaming responses).
func (w *hijackAuditWriter) Flush() {
	if fl, ok := w.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

// Hijack takes over the client connection (as ReverseProxy does for an upgrade) and wraps it in
// a countingConn so the streamed bytes are tallied and the session close is audited.
func (w *hijackAuditWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("upstream ResponseWriter does not support hijack")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, nil, err
	}
	status := w.status
	if status == 0 {
		status = http.StatusSwitchingProtocols
	}
	cc := &countingConn{Conn: conn}
	cc.onClose = func() {
		if w.onClose != nil {
			w.onClose(cc.read.Load(), cc.written.Load(), status)
		}
	}
	return cc, rw, nil
}

// countingConn tallies bytes read (agent → upstream) and written (upstream → agent) over a
// hijacked duplex connection and fires onClose once, when the connection is first closed.
type countingConn struct {
	net.Conn
	read      atomic.Int64
	written   atomic.Int64
	onClose   func()
	closeOnce sync.Once
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.read.Add(int64(n))
	}
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.written.Add(int64(n))
	}
	return n, err
}

func (c *countingConn) Close() error {
	err := c.Conn.Close()
	c.closeOnce.Do(func() {
		if c.onClose != nil {
			c.onClose()
		}
	})
	return err
}
