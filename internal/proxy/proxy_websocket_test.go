package proxy

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/upstream"
)

// wsEchoBackend is a minimal HTTP server that accepts an HTTP connection upgrade (simulating
// WebSocket) by hijacking, writes a 101 response, then echoes every byte back. It is a plain
// echo server — no WebSocket framing — which is enough to verify that the proxy does not corrupt
// the duplex stream (no ModifyResponse body-wrap on 101).
func wsEchoBackend(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Require the Upgrade header to be present.
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "expected websocket upgrade", http.StatusBadRequest)
			return
		}
		hj, ok := w.(http.Hijacker)
		require.True(t, ok, "backend ResponseWriter must support hijack")
		conn, _, err := hj.Hijack()
		require.NoError(t, err)
		defer conn.Close()
		// Send 101 Switching Protocols.
		_, err = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\n" +
			"Connection: Upgrade\r\n" +
			"Upgrade: websocket\r\n\r\n"))
		require.NoError(t, err)
		// Echo bytes back until the client closes.
		buf := make([]byte, 64)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if _, werr := conn.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// dialWS opens a raw TCP connection to proxyURL, sends an HTTP/1.1 WebSocket upgrade request,
// and returns the raw connection + buffered reader after parsing the 101 response.
func dialWS(t *testing.T, proxyURL, path, token string) (net.Conn, *bufio.Reader, *http.Response) {
	t.Helper()
	u, err := url.Parse(proxyURL)
	require.NoError(t, err)
	conn, err := net.DialTimeout("tcp", u.Host, 3*time.Second)
	require.NoError(t, err)
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + u.Host + "\r\n" +
		"Authorization: Bearer " + token + "\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: websocket\r\n\r\n"
	_, err = conn.Write([]byte(req))
	require.NoError(t, err)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	require.NoError(t, err)
	return conn, br, resp
}

// TestWebSocketUpgradeRoundTrips verifies that a plain (non-k8s) WebSocket upgrade completes
// end-to-end through the proxy: the 101 status arrives and raw bytes round-trip over the
// upgraded duplex connection.
func TestWebSocketUpgradeRoundTrips(t *testing.T) {
	backend := wsEchoBackend(t)

	h, ag, up, pol, _, _ := build(t)
	_, token, err := ag.Register("ws-agent")
	require.NoError(t, err)
	u, err := up.Create("ws", backend.URL, upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)
	allowOp(t, pol, u.ID, "GET", "/stream")

	srv := httptest.NewServer(h)
	defer srv.Close()

	conn, br, resp := dialWS(t, srv.URL, "/ws/stream", token)
	defer conn.Close()
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode,
		"the proxy must complete the WebSocket upgrade (101)")

	// Send a line; it must echo back through the upgraded duplex connection.
	_, err = conn.Write([]byte("hello\n"))
	require.NoError(t, err)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	got, err := br.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "hello\n", got, "bytes must round-trip through the proxy over the upgraded WebSocket connection")
}

// TestWebSocketUpgradeWithAuditEnabled verifies that a WebSocket upgrade succeeds (101 + byte
// round-trip) when an audit Recorder is installed. Without the fix, audit body capture wraps
// resp.Body with an io.ReadCloser (not io.ReadWriteCloser), which httputil.ReverseProxy's
// handleUpgradeResponse requires, causing the upgrade to fail.
func TestWebSocketUpgradeWithAuditEnabled(t *testing.T) {
	backend := wsEchoBackend(t)

	h, ag, up, pol, _, _ := buildWithAudit(t)
	_, token, err := ag.Register("ws-agent")
	require.NoError(t, err)
	u, err := up.Create("ws", backend.URL, upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)
	allowOp(t, pol, u.ID, "GET", "/stream")

	srv := httptest.NewServer(h)
	defer srv.Close()

	conn, br, resp := dialWS(t, srv.URL, "/ws/stream", token)
	defer conn.Close()
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode,
		"the proxy must complete the WebSocket upgrade even with audit enabled")

	_, err = conn.Write([]byte("world\n"))
	require.NoError(t, err)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	got, err := br.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "world\n", got, "bytes must round-trip even with the audit recorder installed")
}

// TestIsHTTPUpgrade is a unit test for the isHTTPUpgrade helper — checks both positive and
// negative cases without needing a full proxy stack.
func TestIsHTTPUpgrade(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{
			name: "WebSocket upgrade",
			headers: map[string]string{
				"Connection": "Upgrade",
				"Upgrade":    "websocket",
			},
			want: true,
		},
		{
			name: "WebSocket upgrade - lowercase connection",
			headers: map[string]string{
				"Connection": "upgrade",
				"Upgrade":    "websocket",
			},
			want: true,
		},
		{
			name: "WebSocket upgrade - mixed case and spaces",
			headers: map[string]string{
				"Connection": "keep-alive, Upgrade",
				"Upgrade":    "websocket",
			},
			want: true,
		},
		{
			name: "SPDY upgrade",
			headers: map[string]string{
				"Connection": "Upgrade",
				"Upgrade":    "SPDY/3.1",
			},
			want: true,
		},
		{
			name:    "no upgrade headers",
			headers: map[string]string{},
			want:    false,
		},
		{
			name: "Upgrade header present but Connection not set to Upgrade",
			headers: map[string]string{
				"Connection": "keep-alive",
				"Upgrade":    "websocket",
			},
			want: false,
		},
		{
			name: "Connection Upgrade but no Upgrade header",
			headers: map[string]string{
				"Connection": "Upgrade",
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := http.NewRequest("GET", "http://example.com/", nil)
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			got := isHTTPUpgrade(r)
			require.Equal(t, tc.want, got)
		})
	}
}
