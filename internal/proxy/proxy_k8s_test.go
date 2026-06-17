package proxy

import (
	"bufio"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/upstream"
)

// certPEM returns the PEM-encoded leaf certificate of a TLS httptest server, suitable as a
// CABundle that trusts that server (httptest's server cert is self-signed for 127.0.0.1).
func certPEM(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	cert := srv.Certificate()
	require.NotNil(t, cert)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
}

func TestK8sProxyReadInjectsClusterToken(t *testing.T) {
	var gotAuth, gotPath string
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"kind":"PodList","items":[]}`)
	}))
	defer api.Close()

	h, ag, up, pol, _, _ := build(t)
	_, err := up.CreateKind("prod-cluster", api.URL, upstream.KindK8s, upstream.AuthConfig{
		Type: "none", K8sAuth: "token", Token: "cluster-tok", CABundle: certPEM(t, api),
	})
	require.NoError(t, err)
	cl, err := up.GetByName("prod-cluster")
	require.NoError(t, err)
	_, err = pol.Create(policy.Rule{UpstreamID: cl.ID, Namespace: "prod", Resource: "pods", Verb: "list", Outcome: policy.Allow})
	require.NoError(t, err)

	_, token, err := ag.Register("claude")
	require.NoError(t, err)

	w := do(t, h, http.MethodGet, "/prod-cluster/api/v1/namespaces/prod/pods", token)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "PodList")
	require.Equal(t, "Bearer cluster-tok", gotAuth, "the cluster token must replace the agent token upstream")
	require.Equal(t, "/api/v1/namespaces/prod/pods", gotPath)
}

func TestK8sProxyNamespaceScopingDenies(t *testing.T) {
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"kind":"PodList"}`)
	}))
	defer api.Close()

	h, ag, up, pol, _, _ := build(t)
	_, err := up.CreateKind("prod-cluster", api.URL, upstream.KindK8s, upstream.AuthConfig{
		Type: "none", K8sAuth: "token", Token: "cluster-tok", CABundle: certPEM(t, api),
	})
	require.NoError(t, err)
	cl, err := up.GetByName("prod-cluster")
	require.NoError(t, err)
	_, err = pol.Create(policy.Rule{UpstreamID: cl.ID, Namespace: "prod", Resource: "pods", Verb: "list", Outcome: policy.Allow})
	require.NoError(t, err)

	_, token, err := ag.Register("claude")
	require.NoError(t, err)

	// prod grant does NOT reach staging.
	w := do(t, h, http.MethodGet, "/prod-cluster/api/v1/namespaces/staging/pods", token)
	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestK8sProxyStreamsIncrementally(t *testing.T) {
	// The fake API server streams three chunked log lines with a delay between them.
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		require.True(t, ok)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		for i := 1; i <= 3; i++ {
			_, _ = fmt.Fprintf(w, "line-%d\n", i)
			fl.Flush()
			time.Sleep(150 * time.Millisecond)
		}
	}))
	defer api.Close()

	h, ag, up, pol, _, _ := build(t)
	_, err := up.CreateKind("prod-cluster", api.URL, upstream.KindK8s, upstream.AuthConfig{
		Type: "none", K8sAuth: "token", Token: "cluster-tok", CABundle: certPEM(t, api),
	})
	require.NoError(t, err)
	cl, err := up.GetByName("prod-cluster")
	require.NoError(t, err)
	// pods/log watch (follow=true)
	_, err = pol.Create(policy.Rule{UpstreamID: cl.ID, Namespace: "prod", Resource: "pods/log", Verb: "watch", Outcome: policy.Allow})
	require.NoError(t, err)

	_, token, err := ag.Register("claude")
	require.NoError(t, err)

	// Serve through a real listener so we can read the stream incrementally.
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet,
		srv.URL+"/prod-cluster/api/v1/namespaces/prod/pods/web-1/log?follow=true", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	br := bufio.NewReader(resp.Body)
	line1, err := br.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "line-1\n", line1)
	// line-1 must arrive well before the server finishes writing line-3 (~300ms later).
	require.Less(t, time.Since(start), 250*time.Millisecond, "first line must stream before the server finishes")

	rest, err := io.ReadAll(br)
	require.NoError(t, err)
	require.Contains(t, string(rest), "line-3")
}

// keep x509 import honest (used indirectly when building certs in helpers).
var _ = x509.NewCertPool
