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

// allowOp registers an allow operation rule whose template matches the given method/path
// (any-value text vars), the operation-model equivalent of the old "allow all" path-glob.
func allowOp(t *testing.T, pol *policy.Registry, upstreamID, method, pathTemplate string, vars ...string) {
	t.Helper()
	vp := map[string]policy.ValuePolicy{}
	for _, v := range vars {
		vp[v] = policy.ValuePolicy{Type: "text", Mode: "any"}
	}
	_, err := pol.Create(policy.Rule{UpstreamID: upstreamID, OpMethod: method,
		OpPathTemplate: pathTemplate, OpValuePolicies: vp, Outcome: policy.Allow})
	require.NoError(t, err)
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
	// page is an exempt pagination param; repos/{name} extracts one segment.
	allowOp(t, pol, u.ID, "GET", "/repos/{name:text}", "name")

	w := do(t, h, http.MethodGet, "/be/repos/x?page=2", token)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "ok", w.Body.String())
	require.Equal(t, "Bearer upstreamtok", gotAuth) // upstream cred injected
	require.Equal(t, "/repos/x?page=2", gotPath)    // agent token NOT forwarded
}

func TestProxyCookieTokenAuthAndStrip(t *testing.T) {
	var gotAuth, gotCookie string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
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
	allowOp(t, pol, u.ID, "GET", "/repos/{name:text}", "name")

	// No Authorization header — authenticate via the outwall_token cookie (the browser case).
	r := httptest.NewRequest(http.MethodGet, "/be/repos/x", nil)
	r.AddCookie(&http.Cookie{Name: "outwall_token", Value: token})
	r.AddCookie(&http.Cookie{Name: "sessionid", Value: "keepme"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "Bearer upstreamtok", gotAuth)    // authed via cookie; upstream cred injected
	require.NotContains(t, gotCookie, "outwall_token") // agent token stripped before forwarding
	require.Contains(t, gotCookie, "sessionid=keepme") // the upstream's own cookies pass through

	// A bogus cookie token is rejected.
	r2 := httptest.NewRequest(http.MethodGet, "/be/repos/x", nil)
	r2.AddCookie(&http.Cookie{Name: "outwall_token", Value: "nope"})
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	require.Equal(t, http.StatusUnauthorized, w2.Code)
}

func TestProxyOperationEnforcement(t *testing.T) {
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	h, ag, up, pol, appr, _ := build(t)
	_, token, _ := ag.Register("claude")
	// The upstream Name is the host; the first path segment routes to it.
	u, err := up.Create("example.test", backend.URL, upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)
	_, err = pol.Create(policy.Rule{
		UpstreamID: u.ID, OpMethod: "GET",
		OpPathTemplate:  "/projects/{project_path:text}/pipelines",
		OpValuePolicies: map[string]policy.ValuePolicy{"project_path": {Type: "text", Mode: "set", Values: []string{"a"}}},
		Outcome:         policy.Allow,
	})
	require.NoError(t, err)

	// (1) an allowed value proxies through (200).
	w := do(t, h, http.MethodGet, "/example.test/projects/a/pipelines", token)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "ok", w.Body.String())

	// (2) a new value blocks on approval; once approved the set is extended and it proceeds.
	go func() {
		require.Eventually(t, func() bool { return len(appr.List()) == 1 }, time.Second, 10*time.Millisecond)
		_ = appr.Resolve(appr.List()[0].ID, true, "")
	}()
	w = do(t, h, http.MethodGet, "/example.test/projects/b/pipelines", token)
	require.Equal(t, http.StatusOK, w.Code, "new value approved → proceeds")

	// The set was extended: a second request for the same value is now allowed without approval.
	w = do(t, h, http.MethodGet, "/example.test/projects/b/pipelines", token)
	require.Equal(t, http.StatusOK, w.Code, "value b is now in the set")
	require.Equal(t, "/projects/b/pipelines", gotPath)

	// (3) a path that matches no template is denied (403).
	w = do(t, h, http.MethodGet, "/example.test/other", token)
	require.Equal(t, http.StatusForbidden, w.Code)

	// A denied new-value approval returns 403.
	go func() {
		require.Eventually(t, func() bool { return len(appr.List()) == 1 }, time.Second, 10*time.Millisecond)
		_ = appr.Resolve(appr.List()[0].ID, false, "")
	}()
	w = do(t, h, http.MethodGet, "/example.test/projects/c/pipelines", token)
	require.Equal(t, http.StatusForbidden, w.Code, "denied new value → 403")
}

func TestProxyBodyVariableEnforcement(t *testing.T) {
	var gotBody string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	h, ag, up, pol, _, _ := build(t)
	_, token, _ := ag.Register("claude")
	u, err := up.Create("example.test", backend.URL, upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)
	_, err = pol.Create(policy.Rule{
		UpstreamID: u.ID, OpMethod: "POST", OpPathTemplate: "/widgets",
		OpBodyTemplate:  map[string]string{"name": "{name:text}"},
		OpValuePolicies: map[string]policy.ValuePolicy{"name": {Type: "text", Mode: "set", Values: []string{"alpha"}}},
		Outcome:         policy.Allow,
	})
	require.NoError(t, err)

	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/example.test/widgets", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// (1) allowed body value proxies through AND the upstream receives the original body intact.
	w := post(`{"name":"alpha"}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, `{"name":"alpha"}`, gotBody, "the body must be restored for the upstream")

	// (2) a body missing the declared field matches no template → 403.
	w = post(`{"other":"x"}`)
	require.Equal(t, http.StatusForbidden, w.Code)
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
	allowOp(t, pol, u.ID, "GET", "/{p:text}", "p")
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
	_, err := pol.Create(policy.Rule{UpstreamID: u.ID, OpMethod: "GET",
		OpPathTemplate:  "/{p:text}",
		OpValuePolicies: map[string]policy.ValuePolicy{"p": {Type: "text", Mode: "any"}},
		Outcome:         policy.RequireApproval})
	require.NoError(t, err)

	go func() {
		require.Eventually(t, func() bool { return len(appr.List()) == 1 }, time.Second, 10*time.Millisecond)
		_ = appr.Resolve(appr.List()[0].ID, true, "")
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
	_, err := pol.Create(policy.Rule{UpstreamID: u.ID, OpMethod: "GET",
		OpPathTemplate:  "/{p:text}",
		OpValuePolicies: map[string]policy.ValuePolicy{"p": {Type: "text", Mode: "any"}},
		Outcome:         policy.Allow, RateLimitPerMin: 1})
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
	_, _ = pol.Create(policy.Rule{UpstreamID: u.ID, OpMethod: "POST",
		OpPathTemplate:  "/things",
		OpQueryTemplate: map[string]string{"x": "{x:text}"},
		OpValuePolicies: map[string]policy.ValuePolicy{"x": {Type: "text", Mode: "any"}},
		Outcome:         policy.Allow})

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
