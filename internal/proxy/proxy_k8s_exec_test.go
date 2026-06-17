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

	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/approval"
	"github.com/Sipaha/outwall/internal/audit"
	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/upstream"
)

// upgradeEchoServer is a minimal fake k8s API server that accepts an HTTP connection upgrade
// on a pod exec/attach/portforward subresource and then echoes every byte it receives back to
// the client over the same hijacked connection. It hand-rolls the 101 handshake (no WebSocket
// framing) so the test proves raw bidirectional bytes traverse outwall's reverse proxy without
// pulling in a runtime or test WebSocket dependency. gotAuth captures the Authorization header
// the upstream saw (to confirm the cluster token replaced the agent token).
func upgradeEchoServer(t *testing.T, gotAuth *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Connection"), "Upgrade") {
			http.Error(w, "expected upgrade", http.StatusBadRequest)
			return
		}
		*gotAuth = r.Header.Get("Authorization")
		hj, ok := w.(http.Hijacker)
		require.True(t, ok, "upstream ResponseWriter must support hijack")
		conn, _, err := hj.Hijack()
		require.NoError(t, err)
		defer conn.Close()
		// Complete the 101 handshake, echoing the negotiated protocol.
		proto := r.Header.Get("Upgrade")
		_, err = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\n" +
			"Connection: Upgrade\r\n" +
			"Upgrade: " + proto + "\r\n\r\n"))
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

// dialUpgrade opens a raw TCP connection to proxyURL, sends an HTTP/1.1 upgrade request for the
// given path with the agent bearer token, and returns the connection after reading the status
// line + headers. It does NOT close the conn (caller owns it). The returned reader is positioned
// at the start of the post-handshake byte stream.
func dialUpgrade(t *testing.T, proxyURL, path, token string) (net.Conn, *bufio.Reader, *http.Response) {
	t.Helper()
	u, err := url.Parse(proxyURL)
	require.NoError(t, err)
	conn, err := net.DialTimeout("tcp", u.Host, 3*time.Second)
	require.NoError(t, err)
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + u.Host + "\r\n" +
		"Authorization: Bearer " + token + "\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: SPDY/3.1\r\n\r\n"
	_, err = conn.Write([]byte(req))
	require.NoError(t, err)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	require.NoError(t, err)
	return conn, br, resp
}

func execUpstreamSetup(t *testing.T, outcome string) (h http.Handler, appr *approval.Queue, token string, gotAuth *string) {
	h, appr, token, gotAuth, _ = execUpstreamSetupAudit(t, outcome, false)
	return h, appr, token, gotAuth
}

// execUpstreamSetupAudit builds an exec-capable proxy; when withAudit is true the proxy carries
// an audit Recorder (so the test also exercises the upgrade path with audit installed — proving
// the 101 stream is not corrupted by body capture and that a metadata record is written).
func execUpstreamSetupAudit(t *testing.T, outcome string, withAudit bool) (h http.Handler, appr *approval.Queue, token string, gotAuth *string, rec *audit.Recorder) {
	t.Helper()
	gotAuth = new(string)
	api := upgradeEchoServer(t, gotAuth)

	var ag *agent.Registry
	var up *upstream.Registry
	var pol *policy.Registry
	if withAudit {
		h, ag, up, pol, appr, rec = buildWithAudit(t)
	} else {
		h, ag, up, pol, appr, _ = build(t)
	}
	_, err := up.CreateKind("prod-cluster", api.URL, upstream.KindK8s, upstream.AuthConfig{
		Type: "none", K8sAuth: "token", Token: "cluster-tok", CABundle: certPEM(t, api),
	})
	require.NoError(t, err)
	cl, err := up.GetByName("prod-cluster")
	require.NoError(t, err)
	if outcome != "" {
		_, err = pol.Create(policy.Rule{
			UpstreamID: cl.ID, Namespace: "prod", Resource: "pods/exec", Verb: "create", Outcome: outcome,
		})
		require.NoError(t, err)
	}
	_, token, err = ag.Register("claude")
	require.NoError(t, err)
	return h, appr, token, gotAuth, rec
}

const execPath = "/prod-cluster/api/v1/namespaces/prod/pods/web-1/exec?command=sh&container=app"

func TestK8sExecUpgradeRoundTripsBytesWhenAllowed(t *testing.T) {
	h, _, token, gotAuth := execUpstreamSetup(t, policy.Allow)

	srv := httptest.NewServer(h)
	defer srv.Close()

	conn, br, resp := dialUpgrade(t, srv.URL, execPath, token)
	defer conn.Close()
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode, "the proxy must complete the upgrade")

	// A byte sent by the client must echo back through the upgraded duplex stream.
	_, err := conn.Write([]byte("ping\n"))
	require.NoError(t, err)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	got, err := br.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "ping\n", got, "a byte must round-trip through the proxy over the upgraded connection")

	require.Equal(t, "Bearer cluster-tok", *gotAuth, "the cluster token must replace the agent token on the upgrade")
}

func TestK8sExecUpgradeRoundTripsWithAuditInstalled(t *testing.T) {
	// With an audit Recorder attached, the proxy must STILL complete the upgrade and round-trip
	// a byte: the response-body capture (ModifyResponse) corrupts a 101 duplex stream, so it must
	// be bypassed for upgrades.
	h, _, token, gotAuth, _ := execUpstreamSetupAudit(t, policy.Allow, true)

	srv := httptest.NewServer(h)
	defer srv.Close()

	conn, br, resp := dialUpgrade(t, srv.URL, execPath, token)
	defer conn.Close()
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode, "the upgrade must complete even with audit on")

	_, err := conn.Write([]byte("ping\n"))
	require.NoError(t, err)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	got, err := br.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "ping\n", got, "a byte must round-trip even with the audit recorder installed")
	require.Equal(t, "Bearer cluster-tok", *gotAuth)
}

func TestK8sExecUpgradeDeniedReturns403BeforeUpgrade(t *testing.T) {
	// No rule at all → default deny.
	h, _, token, gotAuth := execUpstreamSetup(t, "")

	srv := httptest.NewServer(h)
	defer srv.Close()

	conn, _, resp := dialUpgrade(t, srv.URL, execPath, token)
	defer conn.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode, "denied exec must be refused with 403, never upgraded")
	require.NotEqual(t, http.StatusSwitchingProtocols, resp.StatusCode)
	require.Empty(t, *gotAuth, "the upstream must never be contacted for a denied upgrade")
}

func TestK8sExecUpgradeApprovalBlocksUntilApproved(t *testing.T) {
	h, appr, token, gotAuth := execUpstreamSetup(t, policy.RequireApproval)

	srv := httptest.NewServer(h)
	defer srv.Close()

	// Resolve the approval (allow) once it parks.
	go func() {
		require.Eventually(t, func() bool { return len(appr.List()) == 1 }, 3*time.Second, 10*time.Millisecond)
		_ = appr.Resolve(appr.List()[0].ID, true)
	}()

	conn, br, resp := dialUpgrade(t, srv.URL, execPath, token)
	defer conn.Close()
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode, "after approval the upgrade must proceed")

	_, err := conn.Write([]byte("hi\n"))
	require.NoError(t, err)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	got, err := br.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "hi\n", got)
	require.Equal(t, "Bearer cluster-tok", *gotAuth)
}
