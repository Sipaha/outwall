package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/approval"
	"github.com/Sipaha/outwall/internal/audit"
	"github.com/Sipaha/outwall/internal/authn"
	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
	"github.com/Sipaha/outwall/internal/upstream"
)

func build(t *testing.T) (http.Handler, *agent.Registry, *upstream.Registry, *policy.Registry, *approval.Queue, *secret.Vault) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	v := secret.NewVault(s)
	require.NoError(t, v.Init("pw"))
	ag := agent.NewRegistry(s)
	up := upstream.NewRegistry(s, v)
	pol := policy.NewRegistry(s)
	appr := approval.NewQueueWithTimeout(2 * time.Second)
	h := New(Deps{Agents: ag, Upstreams: up, Policy: pol, Limiter: policy.NewLimiter(),
		Approvals: appr, AuthManager: authn.NewManager(nil), Vault: v})
	return h, ag, up, pol, appr, v
}

func buildWithAudit(t *testing.T) (http.Handler, *agent.Registry, *upstream.Registry, *policy.Registry, *approval.Queue, *audit.Recorder) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	v := secret.NewVault(s)
	require.NoError(t, v.Init("pw"))
	ag := agent.NewRegistry(s)
	up := upstream.NewRegistry(s, v)
	pol := policy.NewRegistry(s)
	appr := approval.NewQueueWithTimeout(2 * time.Second)
	rec := audit.NewRecorder(s)
	h := New(Deps{Agents: ag, Upstreams: up, Policy: pol, Limiter: policy.NewLimiter(),
		Approvals: appr, AuthManager: authn.NewManager(nil), Vault: v, Audit: rec})
	return h, ag, up, pol, appr, rec
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

	h, ag, up, pol, _, _ := build(t)
	_, token, err := ag.Register("claude")
	require.NoError(t, err)
	u, err := up.Create("be", backend.URL, upstream.AuthConfig{
		Type: "static", Header: "Authorization", Token: "Bearer upstreamtok",
	})
	require.NoError(t, err)
	_, err = pol.Create(policy.Rule{UpstreamID: u.ID, Method: "*", PathGlob: "/**", Outcome: policy.Allow})
	require.NoError(t, err)

	w := do(t, h, http.MethodGet, "/be/repos/x?page=2", token)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "ok", w.Body.String())
	require.Equal(t, "Bearer upstreamtok", gotAuth) // upstream cred injected
	require.Equal(t, "/repos/x?page=2", gotPath)    // agent token NOT forwarded
}

func TestProxyGuards(t *testing.T) {
	h, ag, up, pol, _, v := build(t)
	_, token, _ := ag.Register("claude")
	u, _ := up.Create("be", "http://127.0.0.1:1", upstream.AuthConfig{Type: "none"})

	// 401 missing token
	require.Equal(t, http.StatusUnauthorized, do(t, h, http.MethodGet, "/be/x", "").Code)
	// 401 bad token
	require.Equal(t, http.StatusUnauthorized, do(t, h, http.MethodGet, "/be/x", "owa_bad").Code)
	// 404 unknown upstream
	require.Equal(t, http.StatusNotFound, do(t, h, http.MethodGet, "/nope/x", token).Code)
	// 403 default-deny (no rule yet)
	require.Equal(t, http.StatusForbidden, do(t, h, http.MethodGet, "/be/x", token).Code)
	// 503 vault locked
	_, err := pol.Create(policy.Rule{UpstreamID: u.ID, Method: "*", PathGlob: "/**", Outcome: policy.Allow})
	require.NoError(t, err)
	v.Lock()
	require.Equal(t, http.StatusServiceUnavailable, do(t, h, http.MethodGet, "/be/x", token).Code)
}

func TestProxyRequireApprovalBlocksUntilResolved(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()
	h, ag, up, pol, appr, _ := build(t)
	_, token, _ := ag.Register("claude")
	u, _ := up.Create("be", backend.URL, upstream.AuthConfig{Type: "none"})
	_, err := pol.Create(policy.Rule{UpstreamID: u.ID, Method: "*", PathGlob: "/**", Outcome: policy.RequireApproval})
	require.NoError(t, err)

	go func() {
		require.Eventually(t, func() bool { return len(appr.List()) == 1 }, time.Second, 10*time.Millisecond)
		_ = appr.Resolve(appr.List()[0].ID, true)
	}()
	w := do(t, h, http.MethodGet, "/be/x", token)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "ok", w.Body.String())
}

func TestProxyRateLimit(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()
	h, ag, up, pol, _, _ := build(t)
	_, token, _ := ag.Register("claude")
	u, _ := up.Create("be", backend.URL, upstream.AuthConfig{Type: "none"})
	_, err := pol.Create(policy.Rule{UpstreamID: u.ID, Method: "*", PathGlob: "/**", Outcome: policy.Allow, RateLimitPerMin: 1})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, do(t, h, http.MethodGet, "/be/x", token).Code)
	require.Equal(t, http.StatusTooManyRequests, do(t, h, http.MethodGet, "/be/x", token).Code)
}

func TestProxyRecordsAudit(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer backend.Close()
	h, ag, up, pol, _, rec := buildWithAudit(t)
	_, token, _ := ag.Register("claude")
	u, _ := up.Create("be", backend.URL, upstream.AuthConfig{Type: "static", Header: "Authorization", Token: "Bearer up"})
	_, _ = pol.Create(policy.Rule{UpstreamID: u.ID, Method: "*", PathGlob: "/**", Outcome: policy.Allow})

	r := httptest.NewRequest(http.MethodPost, "/be/things?x=1", strings.NewReader(`{"hi":1}`))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	require.Eventually(t, func() bool { l, _ := rec.List(10); return len(l) == 1 }, time.Second, 10*time.Millisecond)
	list, _ := rec.List(10)
	e := list[0]
	require.Equal(t, "POST", e.Method)
	require.Equal(t, "/things", e.Path)
	require.Equal(t, "x=1", e.Query)
	require.Equal(t, 200, e.StatusCode)
	require.Equal(t, "***", e.Headers["Authorization"]) // agent token masked
	_, bodies, _ := rec.Get(e.ID)
	require.Len(t, bodies, 2)
}

func TestProxyRecordsDeny(t *testing.T) {
	h, ag, up, _, _, rec := buildWithAudit(t)
	_, token, _ := ag.Register("claude")
	_, _ = up.Create("be", "http://127.0.0.1:1", upstream.AuthConfig{Type: "none"})

	require.Equal(t, http.StatusForbidden, do(t, h, http.MethodGet, "/be/x", token).Code)
	list, _ := rec.List(10)
	require.Len(t, list, 1)
	require.Equal(t, "deny", list[0].Decision)
	require.Equal(t, 403, list[0].StatusCode)
}
