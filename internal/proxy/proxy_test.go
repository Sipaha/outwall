package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	"github.com/Sipaha/outwall/internal/serverprofile"
	_ "github.com/Sipaha/outwall/internal/serverprofile/citeck"
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
		Approvals: appr, AuthManager: authn.NewManager(nil), Vault: v,
		BrowseDomain: "outwall.localhost"})
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
		Approvals: appr, AuthManager: authn.NewManager(nil), Vault: v, Audit: rec,
		BrowseDomain: "outwall.localhost"})
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

	// CSRF guard: a valid cookie from a cross-site context is refused (the header path is not).
	r3 := httptest.NewRequest(http.MethodGet, "/be/repos/x", nil)
	r3.AddCookie(&http.Cookie{Name: "outwall_token", Value: token})
	r3.Header.Set("Sec-Fetch-Site", "cross-site")
	w3 := httptest.NewRecorder()
	h.ServeHTTP(w3, r3)
	require.Equal(t, http.StatusForbidden, w3.Code)

	// Same cross-site context but via the Authorization header still works (header isn't ambient).
	r4 := httptest.NewRequest(http.MethodGet, "/be/repos/x", nil)
	r4.Header.Set("Authorization", "Bearer "+token)
	r4.Header.Set("Sec-Fetch-Site", "cross-site")
	w4 := httptest.NewRecorder()
	h.ServeHTTP(w4, r4)
	require.Equal(t, http.StatusOK, w4.Code)
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

func TestProxyPassesProfileToPolicy(t *testing.T) {
	// Verify the citeck profile is registered (the blank import above registers it).
	_, ok := serverprofile.Get("citeck")
	require.True(t, ok, "citeck serverprofile must be registered via blank import")

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	h, ag, up, pol, _, _ := build(t)
	_, token, err := ag.Register("claude")
	require.NoError(t, err)

	// Register a citeck-profiled upstream.
	u, err := up.CreateProfiled("be", backend.URL, "http", "citeck", upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)

	// Add a citeck READ allow rule (no write rule).
	_, err = pol.Create(policy.Rule{
		UpstreamID:    u.ID,
		Outcome:       "allow",
		Profile:       "citeck",
		ProfileParams: json.RawMessage(`{"op":"read","source_id":"*","workspace":"*"}`),
	})
	require.NoError(t, err)

	postJSON := func(path string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// (1) Records query (read) with a sourceId → the citeck read rule allows it.
	readBody := []byte(`{"query":{"sourceId":"emodel/type","workspaces":["w1"]}}`)
	w := postJSON("/be/api/records/query", readBody)
	require.Equal(t, http.StatusOK, w.Code, "citeck read rule must allow records/query")

	// (2) Records mutate (write) with no write rule → default-deny → 403.
	writeBody := []byte(`{"records":[{"id":"emodel/type@someid","attributes":{}}]}`)
	w = postJSON("/be/api/records/mutate", writeBody)
	require.Equal(t, http.StatusForbidden, w.Code, "no write rule: records/mutate must be denied")
}

func TestProxyHostRoutesToUpstream(t *testing.T) {
	h, ag, up, pol, _, _ := build(t)
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(200)
	}))
	defer backend.Close()

	u, err := up.Create("be", backend.URL, upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)
	allowOp(t, pol, u.ID, "GET", "/dashboard")
	_, token, err := ag.Register("claude")
	require.NoError(t, err)

	// Host-routed (browse) request: no /<name> prefix in the path; full path forwarded.
	r := httptest.NewRequest("GET", "https://be.outwall.localhost/dashboard", nil)
	r.Host = "be.outwall.localhost"
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "/dashboard", gotPath) // full path forwarded, NOT stripped
}

// postJSONViaCookie issues a POST with Content-Type application/json and the agent token in the
// outwall_token cookie (browser context), so viaCookie=true reaches the profile's Authorize.
func postJSONViaCookie(h http.Handler, path, token string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	r.AddCookie(&http.Cookie{Name: TokenCookieName, Value: token})
	r.Header.Set("Content-Type", "application/json")
	// No Sec-Fetch-Site header → CSRF guard passes (absent = same-origin).
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// citeckReadUpstream sets up a citeck-profiled upstream with a single concrete workspace read-allow
// rule (workspace=ws, source=*). Returns the upstream, a registered agent token, and the cleanup.
func citeckReadUpstream(t *testing.T, backendURL string, ag *agent.Registry, up *upstream.Registry, pol *policy.Registry, ws string) (string, string) {
	t.Helper()
	_, token, err := ag.Register("claude")
	require.NoError(t, err)
	u, err := up.CreateProfiled("citeck-be", backendURL, "http", "citeck", upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)
	_, err = pol.Create(policy.Rule{
		UpstreamID:    u.ID,
		Outcome:       "allow",
		Profile:       "citeck",
		ProfileParams: json.RawMessage(`{"op":"read","source_id":"*","workspace":"` + ws + `"}`),
	})
	require.NoError(t, err)
	return u.ID, token
}

// TestProxy_SyntheticResponse_NoUpstreamCall verifies that when dec.Response is set (citeck filter
// returns emptyRecordsResponse because all requested workspaces are denied, browser mode), the proxy
// returns a 200 application/json synthetic body WITHOUT contacting the upstream.
func TestProxy_SyntheticResponse_NoUpstreamCall(t *testing.T) {
	upstreamHit := false
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	h, agReg, upReg, polReg, _, _ := build(t)
	_, token := citeckReadUpstream(t, up.URL, agReg, upReg, polReg, "allowed-ws")

	// Request workspaces:["denied-ws"] — no rule allows it; legacy=Deny → filterReadQuery →
	// keep=[] → browser=true → Response = emptyRecordsResponse.
	body := []byte(`{"query":{"sourceId":"emodel/person","workspaces":["denied-ws"]}}`)
	rec := postJSONViaCookie(h, "/citeck-be/api/records/query", token, body)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"records":[],"hasMore":false,"totalCount":0,"messages":[],"version":1}`, rec.Body.String())
	require.False(t, upstreamHit, "upstream must not be contacted for a synthetic response")
}

// TestProxy_RewriteBody_ForwardsNarrowedBody verifies that when dec.RewriteBody is set (citeck
// filter narrows the workspaces list to the allowed subset, browser mode), the proxy replaces
// r.Body with the narrowed bytes and the upstream receives the narrowed body.
func TestProxy_RewriteBody_ForwardsNarrowedBody(t *testing.T) {
	var gotBody string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	h, agReg, upReg, polReg, _, _ := build(t)
	_, token := citeckReadUpstream(t, backend.URL, agReg, upReg, polReg, "b")

	// Request workspaces:["a","b"]; only "b" is allowed → legacy=Deny → filterReadQuery →
	// partial explicit list, browser=true → RewriteBody with only ["b"].
	body := []byte(`{"query":{"sourceId":"emodel/person","workspaces":["a","b"]}}`)
	rec := postJSONViaCookie(h, "/citeck-be/api/records/query", token, body)

	require.Equal(t, http.StatusOK, rec.Code)
	// The upstream must see the narrowed body (only workspace "b").
	require.JSONEq(t, `{"query":{"sourceId":"emodel/person","workspaces":["b"]}}`, gotBody,
		"upstream must receive the narrowed body")
}

func mustURLPath(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u.Path
}

func TestProxyBrowseRewritesLocationAndCookieDomain(t *testing.T) {
	h, ag, up, pol, _, _ := build(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Redirect to the backend's own absolute origin + set a domain-scoped cookie.
		w.Header().Set("Location", "http://"+r.Host+"/after-login")
		w.Header().Add("Set-Cookie", "sid=abc; Path=/; Domain="+r.Host+"; HttpOnly")
		w.WriteHeader(302)
	}))
	defer backend.Close()
	u, err := up.Create("be", backend.URL, upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)
	allowOp(t, pol, u.ID, "GET", "/login")
	_, tok, err := ag.Register("claude")
	require.NoError(t, err)

	r := httptest.NewRequest("GET", "https://be.outwall.localhost/login", nil)
	r.Host = "be.outwall.localhost"
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	require.Equal(t, 302, w.Code)
	// Location rewritten to the browse origin (host, not the backend's real host).
	require.Contains(t, w.Header().Get("Location"), "be.outwall.localhost")
	require.NotContains(t, w.Header().Get("Location"), "127.0.0.1:") // not the backend host:port
	require.Equal(t, "/after-login", mustURLPath(t, w.Header().Get("Location")))
	// Set-Cookie Domain stripped (so the browser keeps it on the browse origin).
	require.NotContains(t, w.Header().Get("Set-Cookie"), "Domain=")
	require.Contains(t, w.Header().Get("Set-Cookie"), "sid=abc")
}
