package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/grant"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
	"github.com/Sipaha/outwall/internal/upstream"
)

func build(t *testing.T) (http.Handler, *agent.Registry, *upstream.Registry, *grant.Registry, *secret.Vault) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	v := secret.NewVault(s)
	require.NoError(t, v.Init("pw"))
	ag := agent.NewRegistry(s)
	up := upstream.NewRegistry(s, v)
	gr := grant.NewRegistry(s)
	h := New(Deps{Agents: ag, Upstreams: up, Grants: gr, Vault: v})
	return h, ag, up, gr, v
}

func do(t *testing.T, h http.Handler, method, target, token string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestProxyHappyPathInjectsAuthAndStripsAgentToken(t *testing.T) {
	var gotAuth, gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	h, ag, up, gr, _ := build(t)
	_, token, err := ag.Register("claude")
	require.NoError(t, err)
	a, err := ag.Authenticate(token)
	require.NoError(t, err)
	u, err := up.Create("be", backend.URL, upstream.AuthConfig{
		Type: "static", Header: "Authorization", Token: "Bearer upstreamtok",
	})
	require.NoError(t, err)
	require.NoError(t, gr.Add(a.ID, u.ID))

	w := do(t, h, http.MethodGet, "/be/repos/x?page=2", token)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "ok", w.Body.String())
	require.Equal(t, "Bearer upstreamtok", gotAuth) // upstream cred injected
	require.Equal(t, "/repos/x?page=2", gotPath)    // agent token NOT forwarded
}

func TestProxyGuards(t *testing.T) {
	h, ag, up, gr, v := build(t)
	_, token, _ := ag.Register("claude")
	a, _ := ag.Authenticate(token)
	u, _ := up.Create("be", "http://127.0.0.1:1", upstream.AuthConfig{Type: "none"})

	// 401 missing token
	require.Equal(t, http.StatusUnauthorized, do(t, h, http.MethodGet, "/be/x", "").Code)
	// 401 bad token
	require.Equal(t, http.StatusUnauthorized, do(t, h, http.MethodGet, "/be/x", "owa_bad").Code)
	// 404 unknown upstream
	require.Equal(t, http.StatusNotFound, do(t, h, http.MethodGet, "/nope/x", token).Code)
	// 403 default-deny (no grant yet)
	require.Equal(t, http.StatusForbidden, do(t, h, http.MethodGet, "/be/x", token).Code)
	// 503 vault locked
	require.NoError(t, gr.Add(a.ID, u.ID))
	v.Lock()
	require.Equal(t, http.StatusServiceUnavailable, do(t, h, http.MethodGet, "/be/x", token).Code)
}
