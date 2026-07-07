package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/approval"
	"github.com/Sipaha/outwall/internal/events"
	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/serverprofile"
	_ "github.com/Sipaha/outwall/internal/serverprofile/citeck"
	"github.com/Sipaha/outwall/internal/upstream"
)

func newDaemon(t *testing.T) *Daemon {
	t.Helper()
	// Isolate kubeconfig discovery from the host so auto-import is a deterministic no-op: an
	// empty $HOME means <home>/.kube does not exist (discovery now scans that whole dir), and
	// $KUBECONFIG points at a nonexistent file. Tests that exercise auto-import override
	// KUBECONFIG with their own temp file afterward.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "no-kubeconfig"))
	d, err := New(Config{
		DBPath:     filepath.Join(t.TempDir(), "d.db"),
		SocketPath: filepath.Join(t.TempDir(), "d.sock"),
		Listen:     "127.0.0.1:0",
		// Ephemeral callback bind so an OIDC login in a test never collides with the fixed 23312
		// (a real outwall-desktop may hold it).
		CallbackListen: "127.0.0.1:0",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func req(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAdminVaultAndUpstreamFlow(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()

	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	// Wrong password on unlock → 401.
	d.vault.Lock()
	require.Equal(t, http.StatusUnauthorized, req(t, h, "POST", "/vault/unlock", `{"password":"no"}`).Code)
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/unlock", `{"password":"pw"}`).Code)

	// Create upstream.
	w := req(t, h, "POST", "/upstreams",
		`{"name":"gh","base_url":"https://api.github.com","auth":{"type":"static","header":"Authorization","token":"Bearer x"}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// List upstreams must not leak the secret token.
	wl := req(t, h, "GET", "/upstreams", "")
	require.Equal(t, http.StatusOK, wl.Code)
	require.NotContains(t, wl.Body.String(), "Bearer x")

	// Register agent returns a token.
	wa := req(t, h, "POST", "/agents/register", `{"name":"claude"}`)
	require.Equal(t, http.StatusOK, wa.Code)
	require.Contains(t, wa.Body.String(), "owa_")
}

func TestAgentListShape(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	wa := req(t, h, "POST", "/agents/register", `{"name":"claude"}`)
	require.Equal(t, http.StatusOK, wa.Code)
	var reg map[string]string
	require.NoError(t, json.Unmarshal(wa.Body.Bytes(), &reg))

	wl := req(t, h, "GET", "/agents", "")
	require.Equal(t, http.StatusOK, wl.Code)
	var list []map[string]string
	require.NoError(t, json.Unmarshal(wl.Body.Bytes(), &list))
	require.Len(t, list, 1)
	a := list[0]
	require.Equal(t, reg["id"], a["id"])
	require.Equal(t, "claude", a["name"])
	require.NotEmpty(t, a["created_at"])
	_, err := time.Parse(time.RFC3339Nano, a["created_at"])
	require.NoError(t, err)
	// Never authenticated yet: last_seen_at is the empty string.
	require.Equal(t, "", a["last_seen_at"])

	// Authenticating (agent-socket / data-plane path) sets last_seen_at.
	_, err = d.agents.Authenticate(reg["token"])
	require.NoError(t, err)
	wl2 := req(t, h, "GET", "/agents", "")
	var list2 []map[string]string
	require.NoError(t, json.Unmarshal(wl2.Body.Bytes(), &list2))
	require.NotEmpty(t, list2[0]["last_seen_at"])
	_, err = time.Parse(time.RFC3339Nano, list2[0]["last_seen_at"])
	require.NoError(t, err)
}

func TestAccessRequestRevokeRemovesRuleAndIsGated(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	wu := req(t, h, "POST", "/upstreams", `{"name":"gh","base_url":"https://api.github.com","auth":{"type":"none"}}`)
	require.Equal(t, http.StatusOK, wu.Code)
	var up map[string]string
	require.NoError(t, json.Unmarshal(wu.Body.Bytes(), &up))

	wa := req(t, h, "POST", "/agents/register", `{"name":"claude"}`)
	require.Equal(t, http.StatusOK, wa.Code)
	var reg map[string]string
	require.NoError(t, json.Unmarshal(wa.Body.Bytes(), &reg))
	agentID := reg["id"]

	// Simulate a prior grant: an access-request record plus the rule it produced.
	accReq, err := d.access.Create(agentID, up["id"], "ci access")
	require.NoError(t, err)
	wr := req(t, h, "POST", "/rules",
		`{"subject_agent_id":"`+agentID+`","upstream_id":"`+up["id"]+`","outcome":"allow","browse_methods":"GET","browse_path":"/**"}`)
	require.Equal(t, http.StatusOK, wr.Code, wr.Body.String())

	// Gated: closing the operator session makes the revoke 403.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/operator/session/lock", "").Code)
	require.Equal(t, http.StatusForbidden, req(t, h, "POST", "/access-requests/"+accReq.ID+"/revoke", "").Code)

	// Re-open and revoke: the rule is gone and the request status is "revoked".
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/operator/session/open", `{"password":"pw"}`).Code)
	wv := req(t, h, "POST", "/access-requests/"+accReq.ID+"/revoke", "")
	require.Equal(t, http.StatusOK, wv.Code, wv.Body.String())

	var rules []map[string]any
	require.NoError(t, json.Unmarshal(req(t, h, "GET", "/rules", "").Body.Bytes(), &rules))
	require.Empty(t, rules)

	var accessReqs []map[string]string
	require.NoError(t, json.Unmarshal(req(t, h, "GET", "/access-requests", "").Body.Bytes(), &accessReqs))
	require.Len(t, accessReqs, 1)
	require.Equal(t, "revoked", accessReqs[0]["status"])
	require.NotEmpty(t, accessReqs[0]["resolved_at"])

	// Unknown id -> 404.
	require.Equal(t, http.StatusNotFound, req(t, h, "POST", "/access-requests/nope/revoke", "").Code)
}

func TestGrantRevokeRemovesRuleAndIsGated(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	wu := req(t, h, "POST", "/upstreams", `{"name":"gh","base_url":"https://api.github.com","auth":{"type":"none"}}`)
	require.Equal(t, http.StatusOK, wu.Code)
	var up map[string]string
	require.NoError(t, json.Unmarshal(wu.Body.Bytes(), &up))

	wa := req(t, h, "POST", "/agents/register", `{"name":"claude"}`)
	require.Equal(t, http.StatusOK, wa.Code)
	var reg map[string]string
	require.NoError(t, json.Unmarshal(wa.Body.Bytes(), &reg))
	agentID := reg["id"]

	// Simulate a prior grant: an access-request record resolved to "granted", plus the rule it
	// produced.
	accReq, err := d.access.Create(agentID, up["id"], "ci access")
	require.NoError(t, err)
	wres := req(t, h, "POST", "/access-requests/"+accReq.ID+"/resolve", `{"status":"granted"}`)
	require.Equal(t, http.StatusOK, wres.Code, wres.Body.String())
	wr := req(t, h, "POST", "/rules",
		`{"subject_agent_id":"`+agentID+`","upstream_id":"`+up["id"]+`","outcome":"allow","browse_methods":"GET","browse_path":"/**"}`)
	require.Equal(t, http.StatusOK, wr.Code, wr.Body.String())

	body := `{"agent_id":"` + agentID + `","upstream_id":"` + up["id"] + `"}`

	// Gated: closing the operator session makes the revoke 403.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/operator/session/lock", "").Code)
	require.Equal(t, http.StatusForbidden, req(t, h, "POST", "/grants/revoke", body).Code)

	// Re-open and revoke: the rule is gone and the access-request status is "revoked".
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/operator/session/open", `{"password":"pw"}`).Code)
	wv := req(t, h, "POST", "/grants/revoke", body)
	require.Equal(t, http.StatusOK, wv.Code, wv.Body.String())
	var revokeResp map[string]any
	require.NoError(t, json.Unmarshal(wv.Body.Bytes(), &revokeResp))
	require.Equal(t, true, revokeResp["ok"])
	require.EqualValues(t, 1, revokeResp["rules_removed"])

	rules, err := d.policy.List()
	require.NoError(t, err)
	for _, r := range rules {
		if r.SubjectAgentID == agentID && r.UpstreamID == up["id"] {
			t.Fatal("rule for the revoked grant still present")
		}
	}
	require.Empty(t, rules)

	accessReqs, err := d.access.List()
	require.NoError(t, err)
	require.Len(t, accessReqs, 1)
	require.Equal(t, accReq.ID, accessReqs[0].ID)
	require.Equal(t, "revoked", accessReqs[0].Status)
}

func TestAgentDeleteCascadesRulesAndIsGated(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	wa := req(t, h, "POST", "/agents/register", `{"name":"claude"}`)
	require.Equal(t, http.StatusOK, wa.Code)
	var reg map[string]string
	require.NoError(t, json.Unmarshal(wa.Body.Bytes(), &reg))
	agentID := reg["id"]

	wr := req(t, h, "POST", "/rules",
		`{"subject_agent_id":"`+agentID+`","upstream_id":"u1","outcome":"allow","browse_methods":"GET","browse_path":"/**"}`)
	require.Equal(t, http.StatusOK, wr.Code, wr.Body.String())

	// Gated: closing the operator session makes the delete 403.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/operator/session/lock", "").Code)
	require.Equal(t, http.StatusForbidden, req(t, h, "DELETE", "/agents/"+agentID, "").Code)

	// Re-open and delete: the agent and its rule are both gone.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/operator/session/open", `{"password":"pw"}`).Code)
	wd := req(t, h, "DELETE", "/agents/"+agentID, "")
	require.Equal(t, http.StatusOK, wd.Code, wd.Body.String())

	var agents []map[string]string
	require.NoError(t, json.Unmarshal(req(t, h, "GET", "/agents", "").Body.Bytes(), &agents))
	require.Empty(t, agents)

	var rules []map[string]any
	require.NoError(t, json.Unmarshal(req(t, h, "GET", "/rules", "").Body.Bytes(), &rules))
	require.Empty(t, rules)
}

func TestAdminVaultLock(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()

	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	// After init the vault is unlocked.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/lock", "").Code)

	// Status now reports locked:true.
	ws := req(t, h, "GET", "/vault/status", "")
	require.Equal(t, http.StatusOK, ws.Code)
	var st map[string]bool
	require.NoError(t, json.Unmarshal(ws.Body.Bytes(), &st))
	require.True(t, st["locked"])
}

func TestAdminRulesAndApprovals(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	// create an upstream + a rule.
	wu := req(t, h, "POST", "/upstreams", `{"name":"gh","base_url":"https://api.github.com","auth":{"type":"none"}}`)
	require.Equal(t, http.StatusOK, wu.Code)
	var up map[string]string
	require.NoError(t, json.Unmarshal(wu.Body.Bytes(), &up))

	wr := req(t, h, "POST", "/rules", `{"upstream_id":"`+up["id"]+`","op_method":"GET","op_path_template":"/repos/{repo:text}","op_value_policies":{"repo":{"type":"text","mode":"any"}},"outcome":"allow"}`)
	require.Equal(t, http.StatusOK, wr.Code, wr.Body.String())

	wl := req(t, h, "GET", "/rules", "")
	require.Equal(t, http.StatusOK, wl.Code)
	require.Contains(t, wl.Body.String(), up["id"])

	// resolving an unknown approval → 404.
	require.Equal(t, http.StatusNotFound, req(t, h, "POST", "/approvals/nope/resolve", `{"approve":true}`).Code)
}

// submitOp parks a KindOperation pending and waits until it is listed, returning its id.
func submitOp(t *testing.T, d *Daemon, upstreamID string, values map[string]string) string {
	t.Helper()
	go func() {
		_, _ = d.approvals.Submit(context.Background(), approval.Pending{
			Kind: approval.KindOperation, AgentID: "a1", UpstreamID: upstreamID, Host: "gitlab.example",
			Method: "GET", Path: "/api/v4/projects/{project_path:text}/pipelines", Purpose: "ci",
			OpMethod:        "GET",
			OpPathTemplate:  "/api/v4/projects/{project_path:text}/pipelines",
			OpQueryTemplate: map[string]string{"updated_after": "{since:date}"},
			OpVariables: []approval.Variable{
				{Name: "project_path", Type: "text"}, {Name: "since", Type: "date"},
			},
			OpValues: values,
		})
	}()
	var id string
	require.Eventually(t, func() bool {
		for _, p := range d.approvals.List() {
			if p.Kind == approval.KindOperation && p.OpValues["project_path"] == values["project_path"] {
				id = p.ID
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond)
	return id
}

func TestOperationApprovalCreatesThenExtendsRule(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	hostUp, _, err := d.upstreams.GetOrCreateByHost("gitlab.example")
	require.NoError(t, err)

	// First operation approval (approve) → one rule with project_path's set = {infra/helm}.
	id1 := submitOp(t, d, hostUp.ID, map[string]string{"project_path": "infra/helm"})
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/approvals/"+id1+"/resolve", `{"approve":true}`).Code)

	rules, err := d.policy.ForUpstream(hostUp.ID)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	r := rules[0]
	require.Equal(t, policy.Allow, r.Outcome)
	require.Equal(t, "GET", r.OpMethod)
	require.Equal(t, "/api/v4/projects/{project_path:text}/pipelines", r.OpPathTemplate)
	require.Equal(t, []string{"infra/helm"}, r.OpValuePolicies["project_path"].Values)
	require.Equal(t, "set", r.OpValuePolicies["project_path"].Mode)
	// date var auto-allows.
	require.Equal(t, "any", r.OpValuePolicies["since"].Mode)

	// Second approval for a NEW value on the SAME template extends the set — one rule, two values.
	id2 := submitOp(t, d, hostUp.ID, map[string]string{"project_path": "apps/web"})
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/approvals/"+id2+"/resolve", `{"approve":true}`).Code)

	rules, err = d.policy.ForUpstream(hostUp.ID)
	require.NoError(t, err)
	require.Len(t, rules, 1, "approving a new value must EXTEND the existing rule, not create a new one")
	require.ElementsMatch(t, []string{"infra/helm", "apps/web"}, rules[0].OpValuePolicies["project_path"].Values)

	// trust_any:[project_path] → that var's policy flips to mode "any".
	id3 := submitOp(t, d, hostUp.ID, map[string]string{"project_path": "anything/here"})
	require.Equal(t, http.StatusOK,
		req(t, h, "POST", "/approvals/"+id3+"/resolve", `{"approve":true,"trust_any":["project_path"]}`).Code)
	rules, err = d.policy.ForUpstream(hostUp.ID)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	require.Equal(t, "any", rules[0].OpValuePolicies["project_path"].Mode)
}

func TestOperationApprovalDenyMakesNoRule(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	hostUp, _, err := d.upstreams.GetOrCreateByHost("gitlab.example")
	require.NoError(t, err)

	id := submitOp(t, d, hostUp.ID, map[string]string{"project_path": "infra/helm"})
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/approvals/"+id+"/resolve", `{"approve":false}`).Code)

	rules, err := d.policy.ForUpstream(hostUp.ID)
	require.NoError(t, err)
	require.Empty(t, rules, "a denied operation approval must create no rule")
}

func TestAdminApprovalListExposesK8sTupleAndMaskedBody(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	// A k8s patch body that (adversarially) embeds a credential — it must be masked out of
	// the surfaced preview so the operator console never leaks it.
	const secretTok = "sk-supersecret-deadbeef"
	body := `{"op":"replace","Authorization":"Bearer ` + secretTok + `","spec":{"image":"web:v2"}}`

	go func() {
		_, _ = d.approvals.Submit(context.Background(), approval.Pending{
			AgentID: "a1", UpstreamID: "u1", Method: "PATCH",
			Path:      "/api/v1/namespaces/prod/deployments/web",
			Namespace: "prod", Resource: "deployments", Verb: "patch",
			RequestBody: []byte(body),
		})
	}()
	require.Eventually(t, func() bool { return len(d.approvals.List()) == 1 }, time.Second, 10*time.Millisecond)

	w := req(t, h, "GET", "/approvals", "")
	require.Equal(t, http.StatusOK, w.Code)
	var list []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	require.Len(t, list, 1)
	a := list[0]
	require.Equal(t, "prod", a["namespace"])
	require.Equal(t, "deployments", a["resource"])
	require.Equal(t, "patch", a["verb"])
	rb, _ := a["request_body"].(string)
	require.Contains(t, rb, "web:v2", "the patch change must be visible")
	require.NotContains(t, rb, secretTok, "the credential must NOT leak into the surfaced body")
	require.NotContains(t, w.Body.String(), secretTok)
}

func TestApprovalEnqueuedSSECarriesTupleAndMaskedBody(t *testing.T) {
	bus := events.NewBus()
	q := approval.NewQueueWithTimeout(time.Second)
	q.SetPublisher(bus)

	srv := httptest.NewServer(sseHandler(bus))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	defer resp.Body.Close()
	time.Sleep(50 * time.Millisecond)

	const secretTok = "sk-supersecret-deadbeef"
	body := `{"Authorization":"Bearer ` + secretTok + `","image":"web:v2"}`
	go func() {
		_, _ = q.Submit(context.Background(), approval.Pending{
			AgentID: "a1", Namespace: "prod", Resource: "deployments", Verb: "patch",
			RequestBody: []byte(body),
		})
	}()

	sc := bufio.NewScanner(resp.Body)
	var dataLine string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, "deployments") {
			dataLine = line
			break
		}
	}
	require.Contains(t, dataLine, `"namespace":"prod"`)
	require.Contains(t, dataLine, `"resource":"deployments"`)
	require.Contains(t, dataLine, `"verb":"patch"`)
	require.Contains(t, dataLine, "web:v2")
	require.NotContains(t, dataLine, secretTok, "the SSE event must not leak the credential")
}

func TestAdminAuditEmptyOK(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	require.Equal(t, http.StatusOK, req(t, h, "GET", "/audit", "").Code)
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/audit/prune", `{"older_than_rfc3339":"2020-01-01T00:00:00Z"}`).Code)
	require.Equal(t, http.StatusNotFound, req(t, h, "GET", "/audit/nope", "").Code)
}

func TestDesktopFocusRoute(t *testing.T) {
	// A daemon built with OnFocusRequest set: POST /desktop/focus over the CSRF-free
	// admin (unix socket) handler returns 2xx and the registered callback ran.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "no-kubeconfig"))
	focused := make(chan struct{}, 1)
	d, err := New(Config{
		DBPath:         filepath.Join(t.TempDir(), "d.db"),
		SocketPath:     filepath.Join(t.TempDir(), "d.sock"),
		Listen:         "127.0.0.1:0",
		OnFocusRequest: func() { focused <- struct{}{} },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	w := req(t, d.AdminHandler(), "POST", "/desktop/focus", "")
	require.GreaterOrEqual(t, w.Code, 200)
	require.Less(t, w.Code, 300)
	select {
	case <-focused:
	default:
		t.Fatal("OnFocusRequest callback did not run")
	}

	// A daemon with a nil OnFocusRequest returns a non-2xx (no window to focus) and
	// must not panic.
	dNil := newDaemon(t)
	wNil := req(t, dNil.AdminHandler(), "POST", "/desktop/focus", "")
	require.True(t, wNil.Code < 200 || wNil.Code >= 300, "expected non-2xx, got %d", wNil.Code)
}

func TestUIServesStaticIndex(t *testing.T) {
	d := newDaemon(t)
	h := d.UIHandler()
	// GET / serves the embedded SPA index (no CSRF needed for static assets).
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "outwall")
	// Unknown client-route path falls back to index.html (SPA routing).
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, httptest.NewRequest("GET", "/dashboard", nil))
	require.Equal(t, http.StatusOK, w2.Code)
	require.Contains(t, w2.Body.String(), "outwall")
}

func TestUISSEUngated(t *testing.T) {
	d := newDaemon(t)
	// SSE is read-only and ungated (no CSRF, no operator session). EventSource cannot set headers,
	// so /api/events must be reachable with none. Use a real server + client: the SSE handler
	// streams on its own goroutine; the client returns once headers arrive, and canceling the
	// request context unblocks the streaming handler.
	srv := httptest.NewServer(d.UIHandler())
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
}

// TestVaultUnlockDoesNotAutoImport verifies the auto-scan runs ONLY at init, never on unlock
// (ADR-0026). A kubeconfig that appears AFTER init is not pulled in by an unlock — the operator
// must use the explicit Import button.
func TestVaultUnlockDoesNotAutoImport(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()

	// init while KUBECONFIG points at newDaemon's nonexistent path → 0 clusters seeded.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	// Only NOW does a kubeconfig appear on disk.
	src := `
apiVersion: v1
kind: Config
clusters:
  - name: c1
    cluster: { server: https://c1.example:6443, insecure-skip-tls-verify: true }
users:
  - name: u
    user: { token: t }
contexts:
  - name: kc-ctx-1
    context: { cluster: c1, user: u }
`
	kcPath := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(kcPath, []byte(src), 0o600))
	t.Setenv("KUBECONFIG", kcPath)

	// Lock then unlock — the unlock hook must NOT scan/import.
	d.vault.Lock()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/unlock", `{"password":"pw"}`).Code)

	wl := req(t, h, "GET", "/upstreams", "")
	require.Equal(t, http.StatusOK, wl.Code)
	var ups []map[string]any
	require.NoError(t, json.Unmarshal(wl.Body.Bytes(), &ups))
	require.Empty(t, ups, "unlock must not auto-import a kubeconfig that appeared after init")

	// The explicit Import button DOES pull it in.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/clusters/import", "").Code)
	wl2 := req(t, h, "GET", "/upstreams", "")
	var ups2 []map[string]any
	require.NoError(t, json.Unmarshal(wl2.Body.Bytes(), &ups2))
	names := map[string]any{}
	for _, u := range ups2 {
		names[fmt.Sprint(u["name"])] = u["kind"]
	}
	require.Equal(t, "k8s", names["kc-ctx-1"], "explicit import must register the cluster")
}

// TestVaultInitAutoImportsClusters verifies a fresh `vault init` (no lock/unlock cycle) auto-imports
// the host's kubeconfig clusters — first run is exactly when there is nothing yet.
func TestVaultInitAutoImportsClusters(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()

	src := `
apiVersion: v1
kind: Config
clusters:
  - name: c1
    cluster: { server: https://c1.example:6443, insecure-skip-tls-verify: true }
users:
  - name: u
    user: { token: t }
contexts:
  - name: kc-ctx-1
    context: { cluster: c1, user: u }
`
	kcPath := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(kcPath, []byte(src), 0o600))
	t.Setenv("KUBECONFIG", kcPath)

	// init only — no lock/unlock.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	wl := req(t, h, "GET", "/upstreams", "")
	require.Equal(t, http.StatusOK, wl.Code)
	var ups []map[string]any
	require.NoError(t, json.Unmarshal(wl.Body.Bytes(), &ups))
	names := map[string]any{}
	for _, u := range ups {
		names[fmt.Sprint(u["name"])] = u["kind"]
	}
	require.Equal(t, "k8s", names["kc-ctx-1"], "init must auto-import kubeconfig clusters")
}

// TestClustersImportEndpoint drives POST /clusters/import directly.
func TestClustersImportEndpoint(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()

	src := `
apiVersion: v1
kind: Config
clusters:
  - name: ec
    cluster: { server: https://ec.example:6443, insecure-skip-tls-verify: true }
users:
  - name: u
    user: { token: t }
contexts:
  - name: ep-ctx
    context: { cluster: ec, user: u }
`
	kcPath := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(kcPath, []byte(src), 0o600))

	// init while KUBECONFIG still points at newDaemon's nonexistent path, so the init auto-import is
	// a no-op and this test isolates the explicit import ENDPOINT. Point KUBECONFIG at the source
	// only afterward.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	t.Setenv("KUBECONFIG", kcPath)
	w := req(t, h, "POST", "/clusters/import", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var res struct {
		Added   []string `json:"added"`
		Skipped []string `json:"skipped"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Contains(t, res.Added, "ep-ctx")

	// Second call → the explicit path uses update=true, so the existing cluster is refreshed
	// in place (reported under "updated"), not skipped, and nothing is added.
	w2 := req(t, h, "POST", "/clusters/import", "")
	require.Equal(t, http.StatusOK, w2.Code)
	var res2 struct {
		Added   []string `json:"added"`
		Updated []string `json:"updated"`
		Skipped []string `json:"skipped"`
	}
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &res2))
	require.Contains(t, res2.Updated, "ep-ctx")
	require.Empty(t, res2.Added)
	require.Empty(t, res2.Skipped)

	// Lists must encode as empty JSON arrays, never `null`: a nil Go slice serializes to null,
	// and the UI's res.added.length throws on null, firing a false "Failed to import" toast.
	require.JSONEq(t, `{"added":[],"updated":["ep-ctx"],"skipped":[]}`, w2.Body.String())
}

// TestClustersImportFromBody drives POST /clusters/import with an uploaded kubeconfig body (the
// file-picker path): the cluster in the body registers, and the body takes precedence over the
// auto-scan.
func TestClustersImportFromBody(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	body := `
apiVersion: v1
kind: Config
clusters: [{ name: bc, cluster: { server: https://bc.example:6443, insecure-skip-tls-verify: true } }]
users: [{ name: bu, user: { token: bt } }]
contexts: [{ name: body-ctx, context: { cluster: bc, user: bu } }]
`
	w := req(t, h, "POST", "/clusters/import", body)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var res struct {
		Added   []string `json:"added"`
		Skipped []string `json:"skipped"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Equal(t, []string{"body-ctx"}, res.Added)

	// The cluster is now registered.
	wl := req(t, h, "GET", "/upstreams", "")
	require.Equal(t, http.StatusOK, wl.Code)
	require.Contains(t, wl.Body.String(), "body-ctx")

	// A junk body is a 400 (the operator explicitly uploaded a bad file).
	wbad := req(t, h, "POST", "/clusters/import", "not a kubeconfig: [")
	require.Equal(t, http.StatusBadRequest, wbad.Code)
}

// TestHostApproveRefusesK8sCredential is defense-in-depth (ADR-0026): approving a host-access card
// that targets a k8s cluster must NOT overwrite the cluster's k8s credential with an HTTP one.
func TestHostApproveRefusesK8sCredential(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	cl, err := d.upstreams.CreateKind("prod-cluster", "https://api.k8s:6443", upstream.KindK8s,
		upstream.AuthConfig{Type: "none", K8sAuth: "token", Token: "cluster-secret"})
	require.NoError(t, err)

	err = d.applyApprovalSideEffects(
		approval.Pending{Kind: approval.KindHostAccess, UpstreamID: cl.ID, Host: cl.Name},
		&upstream.AuthConfig{Type: "static", Header: "Authorization", Token: "Bearer x"}, nil, nil)
	require.Error(t, err, "attaching an HTTP credential to a k8s cluster must be refused")

	// The cluster's k8s token credential is intact (not clobbered).
	after, err := d.upstreams.GetByID(cl.ID)
	require.NoError(t, err)
	require.Equal(t, "token", after.Auth.K8sAuth)
	require.Equal(t, "cluster-secret", after.Auth.Token)
}

func TestOIDCDiscoverEndpoint(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/x/.well-known/openid-configuration" {
			_, _ = w.Write([]byte(`{"issuer":"https://idp/realms/x","authorization_endpoint":"https://idp/auth","token_endpoint":"https://idp/token","scopes_supported":["openid","profile"]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer idp.Close()

	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	w := req(t, h, "POST", "/oidc/discover", `{"url":"`+idp.URL+`/realms/x"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var res map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Equal(t, "https://idp/auth", res["authorization_endpoint"])
	require.Equal(t, "https://idp/token", res["token_endpoint"])

	// A non-resolving / bad issuer → 502, not a crash.
	wbad := req(t, h, "POST", "/oidc/discover", `{"url":"not-a-url"}`)
	require.Equal(t, http.StatusBadGateway, wbad.Code)
}

func TestSetAuthKeepsSecretsOnSameTypeReplace(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.NoError(t, d.vault.Init("pw"))
	d.opsession.Open() // vault.Init called directly (bypassing the HTTP handler that opens the session)
	_, err := d.upstreams.Create("api.test", "https://api.test", upstream.AuthConfig{
		Type: "oidc-authorization-code", ClientID: "old-cid", ClientSecret: "shh",
		AuthURL: "https://idp/auth", TokenURL: "https://idp/token",
		AccessToken: "at", RefreshToken: "rt",
	})
	require.NoError(t, err)

	// List returns the non-secret auth for pre-fill: client_id present, secret never leaks.
	wl := req(t, h, "GET", "/upstreams", "")
	require.Contains(t, wl.Body.String(), "old-cid")
	require.NotContains(t, wl.Body.String(), "shh")

	// Replace with the SAME type, change client_id, leave secret + tokens blank → they are kept.
	body := `{"auth":{"type":"oidc-authorization-code","client_id":"new-cid","auth_url":"https://idp/auth","token_url":"https://idp/token"}}`
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/upstreams/api.test/auth", body).Code)
	up, err := d.upstreams.GetByName("api.test")
	require.NoError(t, err)
	require.Equal(t, "new-cid", up.Auth.ClientID)
	require.Equal(t, "shh", up.Auth.ClientSecret, "blank secret keeps the stored value")
	require.Equal(t, "at", up.Auth.AccessToken, "OIDC tokens are preserved")
	require.Equal(t, "rt", up.Auth.RefreshToken)

	// Changing the TYPE is a full reconfigure — no secret carryover.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/upstreams/api.test/auth", `{"auth":{"type":"static","header":"Authorization"}}`).Code)
	up2, err := d.upstreams.GetByName("api.test")
	require.NoError(t, err)
	require.Equal(t, "", up2.Auth.ClientSecret)
	require.Equal(t, "", up2.Auth.AccessToken)
}

func TestAdminAccessRequests(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	// resolving an unknown request → 404
	require.Equal(t, http.StatusNotFound, req(t, h, "POST", "/access-requests/nope/resolve", `{"status":"granted"}`).Code)
	// list is empty-but-OK initially
	require.Equal(t, http.StatusOK, req(t, h, "GET", "/access-requests", "").Code)
}

func TestUpstreamCreateWithProfile(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	w := req(t, h, "POST", "/upstreams", `{"name":"c.test","base_url":"https://c.test","profile":"citeck","auth":{"type":"none"}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	list := req(t, h, "GET", "/upstreams", "")
	require.Equal(t, http.StatusOK, list.Code)
	var upstreams []map[string]any
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &upstreams))
	require.Len(t, upstreams, 1)
	require.Equal(t, "citeck", upstreams[0]["profile"])
}

// TestUpstreamListIncludesBrowseURL asserts GET /upstreams includes browse_url for http upstreams
// when a browse domain is configured (default "outwall.localhost" in newDaemon).
func TestUpstreamListIncludesBrowseURL(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	w := req(t, h, "POST", "/upstreams",
		`{"name":"be","base_url":"https://be.example","auth":{"type":"none"}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	wl := req(t, h, "GET", "/upstreams", "")
	require.Equal(t, http.StatusOK, wl.Code)
	var ups []map[string]any
	require.NoError(t, json.Unmarshal(wl.Body.Bytes(), &ups))
	require.Len(t, ups, 1)
	bu, _ := ups[0]["browse_url"].(string)
	require.Contains(t, bu, "be.outwall.localhost", "browse_url should contain the upstream name + domain")
	require.Contains(t, bu, "https://", "browse_url should be an https URL")
}

func TestRuleCreateWithProfileParams(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	// Create an upstream first, then a rule with profile params.
	wu := req(t, h, "POST", "/upstreams", `{"name":"citeck.test","base_url":"https://citeck.test","auth":{"type":"none"}}`)
	require.Equal(t, http.StatusOK, wu.Code, wu.Body.String())
	var up map[string]string
	require.NoError(t, json.Unmarshal(wu.Body.Bytes(), &up))

	wr := req(t, h, "POST", "/rules", `{"upstream_id":"`+up["id"]+`","outcome":"allow","profile":"citeck","profile_params":{"op":"read","source_id":"emodel/type","workspace":"*"}}`)
	require.Equal(t, http.StatusOK, wr.Code, wr.Body.String())

	list := req(t, h, "GET", "/rules", "")
	require.Equal(t, http.StatusOK, list.Code)
	var rules []map[string]any
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &rules))
	require.Len(t, rules, 1)
	require.Equal(t, "citeck", rules[0]["profile"])
	params, ok := rules[0]["profile_params"].(map[string]any)
	require.True(t, ok, "profile_params should be a JSON object")
	require.Equal(t, "emodel/type", params["source_id"])
}

func TestProfilesEndpoint(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	w := req(t, h, "GET", "/profiles", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var profiles []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &profiles))
	require.NotEmpty(t, profiles, "profiles list should be non-empty")
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		if name, ok := p["profile"].(string); ok {
			names = append(names, name)
		}
	}
	require.Contains(t, names, "citeck")
}

func TestRuleCreateWithBrowseFields(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, 200, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	require.Equal(t, 200, req(t, h, "POST", "/rules",
		`{"upstream_id":"u1","outcome":"allow","browse_methods":"GET,HEAD","browse_path":"/**"}`).Code)
	body := req(t, h, "GET", "/rules", "").Body.String()
	require.Contains(t, body, `"browse_path":"/**"`)
	require.Contains(t, body, `"browse_methods":"GET,HEAD"`)
}

// mustUpstreamProfiled creates an upstream with the given profile for use in tests.
func (d *Daemon) mustUpstreamProfiled(t *testing.T, name, baseURL, kind, profile string) *upstream.Upstream {
	t.Helper()
	up, err := d.upstreams.CreateProfiled(name, baseURL, kind, profile, upstream.AuthConfig{Type: "none"})
	if err != nil {
		t.Fatalf("mustUpstreamProfiled: %v", err)
	}
	return up
}

// mustAgent registers an agent by name for use in tests and returns its ID.
func (d *Daemon) mustAgent(t *testing.T, name string) string {
	t.Helper()
	a, _, err := d.agents.Register(name)
	if err != nil {
		t.Fatalf("mustAgent: %v", err)
	}
	return a.ID
}

func TestApprovePresetFansOutAgentScopedRules(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, 200, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	// A citeck upstream + an agent.
	up := d.mustUpstreamProfiled(t, "cite.test", "https://cite.test", "http", "citeck")
	agentID := d.mustAgent(t, "a1")

	// Park a KindPreset pending directly on the queue (the daemon owns the resolve side effects).
	go func() {
		_, _ = d.approvals.Submit(context.Background(), approval.Pending{
			Kind: approval.KindPreset, AgentID: agentID, UpstreamID: up.ID, Host: up.Name,
			PresetID: "citeck-readonly", Bindings: map[string]string{"sourceId": "*", "workspace": "proj-x"},
		})
	}()
	var id string
	require.Eventually(t, func() bool {
		for _, p := range d.approvals.List() {
			if p.Kind == approval.KindPreset {
				id = p.ID
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond)

	// Operator narrows nothing; approve with the requested bindings echoed back.
	body := `{"approve":true,"bindings":{"sourceId":"*","workspace":"proj-x"}}`
	require.Equal(t, 200, req(t, h, "POST", "/approvals/"+id+"/resolve", body).Code)

	rules, err := d.policy.ForUpstream(up.ID)
	require.NoError(t, err)
	var browse, citeckRead int
	for _, r := range rules {
		require.Equal(t, agentID, r.SubjectAgentID) // every fanned-out rule is agent-scoped
		if r.BrowsePath == "/**" {
			browse++
		}
		if r.Profile == "citeck" {
			citeckRead++
		}
	}
	require.Equal(t, 1, browse)
	require.Equal(t, 1, citeckRead)
}

// Re-approving a preset (or approving two presets that share a rule) must not create duplicate
// rules — approvePreset is idempotent (ADR-0029).
func TestApprovePresetIsIdempotent(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, 200, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	up := d.mustUpstreamProfiled(t, "cite2.test", "https://cite2.test", "http", "citeck")
	agentID := d.mustAgent(t, "a1")

	approveOnce := func() {
		before := map[string]bool{}
		for _, p := range d.approvals.List() {
			before[p.ID] = true
		}
		go func() {
			_, _ = d.approvals.Submit(context.Background(), approval.Pending{
				Kind: approval.KindPreset, AgentID: agentID, UpstreamID: up.ID, Host: up.Name,
				PresetID: "citeck-readonly", Bindings: map[string]string{"sourceId": "*", "workspace": "*"},
			})
		}()
		var id string
		require.Eventually(t, func() bool {
			for _, p := range d.approvals.List() {
				if p.Kind == approval.KindPreset && !before[p.ID] { // the newly-parked one
					id = p.ID
					return true
				}
			}
			return false
		}, time.Second, 10*time.Millisecond)
		require.Equal(t, 200, req(t, h, "POST", "/approvals/"+id+"/resolve",
			`{"approve":true,"bindings":{"sourceId":"*","workspace":"*"}}`).Code)
	}

	approveOnce()
	approveOnce() // identical second approval must be a no-op, not a duplicate

	rules, err := d.policy.ForUpstream(up.ID)
	require.NoError(t, err)
	var browse, citeckRead int
	for _, r := range rules {
		if r.BrowsePath == "/**" {
			browse++
		}
		if r.Profile == "citeck" {
			citeckRead++
		}
	}
	require.Equal(t, 1, browse, "browse rule duplicated on re-approval")
	require.Equal(t, 1, citeckRead, "citeck read rule duplicated on re-approval")
}

func TestApprovePresetFanoutIsAtomic(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, 200, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	// A fake profile whose preset Build yields a valid rule followed by an invalid-outcome rule.
	serverprofile.Register("atomicfake", atomicFakeProfile{})
	up := d.mustUpstreamProfiled(t, "af.test", "https://af.test", "http", "atomicfake")
	agentID := d.mustAgent(t, "a1")

	go func() {
		_, _ = d.approvals.Submit(context.Background(), approval.Pending{
			Kind: approval.KindPreset, AgentID: agentID, UpstreamID: up.ID, Host: up.Name,
			PresetID: "bad", Bindings: map[string]string{},
		})
	}()
	var id string
	require.Eventually(t, func() bool {
		for _, p := range d.approvals.List() {
			if p.Kind == approval.KindPreset {
				id = p.ID
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond)

	// Approve → fan-out fails on the invalid rule → 400 → NO rules created (atomic).
	require.Equal(t, 400, req(t, h, "POST", "/approvals/"+id+"/resolve", `{"approve":true}`).Code)
	rules, err := d.policy.ForUpstream(up.ID)
	require.NoError(t, err)
	require.Empty(t, rules)
}

// atomicFakeProfile's preset "bad" Builds one valid rule then one with an invalid outcome.
type atomicFakeProfile struct{}

func (atomicFakeProfile) Name() string { return "atomicfake" }
func (atomicFakeProfile) Classify(serverprofile.Request) (serverprofile.Operation, bool, error) {
	return serverprofile.Operation{}, false, nil
}
func (atomicFakeProfile) Authorize(serverprofile.AuthInput) (serverprofile.AuthResult, error) {
	return serverprofile.AuthResult{Outcome: serverprofile.Allow}, nil
}
func (atomicFakeProfile) RuleSchema() serverprofile.RuleSchema {
	return serverprofile.RuleSchema{Profile: "atomicfake"}
}
func (atomicFakeProfile) Presets() []serverprofile.Preset {
	return []serverprofile.Preset{{
		ID: "bad", Label: "Bad",
		Build: func(serverprofile.Bindings) ([]serverprofile.RuleTemplate, error) {
			return []serverprofile.RuleTemplate{
				{Outcome: serverprofile.Allow, BrowseMethods: "GET", BrowsePath: "/**"},
				{Outcome: "bogus"}, // invalid → CreateMany rolls the whole batch back
			}, nil
		},
	}}
}

// TestApprovePresetNilBindingsFallsBackToPendingBindings verifies that when the operator approves a
// KindPreset card with body {"approve":true} but NO "bindings" key (body.Bindings == nil), the
// approvePreset implementation falls back to the pending's own requested bindings to expand the
// rules. The resulting rules must still be agent-scoped and match the preset's expected templates.
func TestApprovePresetNilBindingsFallsBackToPendingBindings(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, 200, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	// A citeck upstream + an agent.
	up := d.mustUpstreamProfiled(t, "cite.test", "https://cite.test", "http", "citeck")
	agentID := d.mustAgent(t, "a1")

	// Park a KindPreset pending with bindings set (requested by the agent).
	go func() {
		_, _ = d.approvals.Submit(context.Background(), approval.Pending{
			Kind: approval.KindPreset, AgentID: agentID, UpstreamID: up.ID, Host: up.Name,
			PresetID: "citeck-readonly", Bindings: map[string]string{"sourceId": "*", "workspace": "proj-y"},
		})
	}()
	var id string
	require.Eventually(t, func() bool {
		for _, p := range d.approvals.List() {
			if p.Kind == approval.KindPreset {
				id = p.ID
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond)

	// Approve with no "bindings" key — operator did not edit anything; approvePreset must use p.Bindings.
	require.Equal(t, 200, req(t, h, "POST", "/approvals/"+id+"/resolve", `{"approve":true}`).Code)

	rules, err := d.policy.ForUpstream(up.ID)
	require.NoError(t, err)
	require.NotEmpty(t, rules, "rules must be created from the pending's bindings when operator sends no bindings")
	var browse, citeckRead int
	for _, r := range rules {
		require.Equal(t, agentID, r.SubjectAgentID, "every fanned-out rule must be agent-scoped")
		if r.BrowsePath == "/**" {
			browse++
		}
		if r.Profile == "citeck" {
			citeckRead++
		}
	}
	require.Equal(t, 1, browse, "one browse rule expected")
	require.Equal(t, 1, citeckRead, "one citeck read rule expected")
}

func TestPresetPreview(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, 200, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	up := d.mustUpstreamProfiled(t, "cite.test", "https://cite.test", "http", "citeck")

	body := `{"upstream_id":"` + up.ID + `","preset_id":"citeck-readonly","bindings":{"sourceId":"*","workspace":"proj-x"}}`
	rec := req(t, h, "POST", "/presets/preview", body)
	require.Equal(t, 200, rec.Code)
	out := rec.Body.String()
	require.Contains(t, out, "GET,HEAD") // browse rule summarized
	require.Contains(t, out, "proj-x")   // citeck read rule with the bound workspace

	// Valid bindings with workspace="*" → 200 (ADR-0039).
	wildcard := `{"upstream_id":"` + up.ID + `","preset_id":"citeck-readonly","bindings":{"sourceId":"*","workspace":"*"}}`
	rec = req(t, h, "POST", "/presets/preview", wildcard)
	require.Equal(t, 200, rec.Code)
	out = rec.Body.String()
	require.Contains(t, out, "GET,HEAD") // browse rule summarized
	require.Contains(t, out, "*")        // citeck read rule with wildcard workspace
}

func TestOperatorSessionControlRoutes(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()

	// Fresh vault (uninitialized): opening a session → 400 "vault not initialized".
	require.Equal(t, http.StatusBadRequest, req(t, h, "POST", "/operator/session/open", `{"password":"pw"}`).Code)

	// Init (ungated bootstrap) sets the master password AND opens the session.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	ws := req(t, h, "GET", "/operator/session/status", "")
	require.Equal(t, http.StatusOK, ws.Code)
	require.Contains(t, ws.Body.String(), `"open":true`)

	// Lock closes it.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/operator/session/lock", "").Code)
	require.Contains(t, req(t, h, "GET", "/operator/session/status", "").Body.String(), `"open":false`)

	// Wrong password → 401 and it stays closed.
	require.Equal(t, http.StatusUnauthorized, req(t, h, "POST", "/operator/session/open", `{"password":"no"}`).Code)
	require.Contains(t, req(t, h, "GET", "/operator/session/status", "").Body.String(), `"open":false`)

	// Correct password → 200, open, with a positive idle_remaining_seconds.
	wo := req(t, h, "POST", "/operator/session/open", `{"password":"pw"}`)
	require.Equal(t, http.StatusOK, wo.Code)
	var st map[string]any
	require.NoError(t, json.Unmarshal(wo.Body.Bytes(), &st))
	require.Equal(t, true, st["open"])
	require.Greater(t, st["idle_remaining_seconds"].(float64), float64(0))
}

func TestOperatorGateSealsBothTransports(t *testing.T) {
	d := newDaemon(t)
	admin := d.AdminHandler() // unix-socket transport (the old CSRF-free full-admin path)
	ui := d.UIHandler()       // TCP /api transport (the old X-Outwall-CSRF path)

	// Bootstrap: init is ungated and opens the operator session.
	require.Equal(t, http.StatusOK, req(t, admin, "POST", "/vault/init", `{"password":"pw"}`).Code)

	// Ungated GET works with no session AND no CSRF header, on both transports.
	require.Equal(t, http.StatusOK, req(t, admin, "GET", "/vault/status", "").Code)
	require.Equal(t, http.StatusOK, req(t, ui, "GET", "/api/vault/status", "").Code)

	const ruleBody = `{"upstream_id":"u1","outcome":"allow","browse_methods":"GET","browse_path":"/**"}`

	// Close the session; a privileged mutation is now 403 over BOTH transports — the old
	// self-approval curl paths (unix socket AND /api) are sealed.
	require.Equal(t, http.StatusOK, req(t, admin, "POST", "/operator/session/lock", "").Code)
	require.Equal(t, http.StatusForbidden, req(t, admin, "POST", "/rules", ruleBody).Code)
	require.Equal(t, http.StatusForbidden, req(t, ui, "POST", "/api/rules", ruleBody).Code)
	require.Equal(t, http.StatusForbidden, req(t, admin, "POST", "/approvals/x/resolve", `{"approve":true}`).Code)
	require.Equal(t, http.StatusForbidden, req(t, ui, "POST", "/api/approvals/x/resolve", `{"approve":true}`).Code)

	// Wrong master password → 401 and the session stays closed (still 403).
	require.Equal(t, http.StatusUnauthorized, req(t, admin, "POST", "/operator/session/open", `{"password":"no"}`).Code)
	require.Equal(t, http.StatusForbidden, req(t, admin, "POST", "/rules", ruleBody).Code)

	// Correct master password opens the session; the gated mutation now succeeds on both transports.
	require.Equal(t, http.StatusOK, req(t, admin, "POST", "/operator/session/open", `{"password":"pw"}`).Code)
	require.Equal(t, http.StatusOK, req(t, admin, "POST", "/rules", ruleBody).Code)
	require.Equal(t, http.StatusOK, req(t, ui, "POST", "/api/rules", ruleBody).Code)
}

func TestVaultUnlockUngatedAndOpensSession(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	// Start fully closed: vault locked AND operator session closed.
	d.vault.Lock()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/operator/session/lock", "").Code)

	// Unlock is UNGATED and self-authenticating: it works with NO open operator session, unlocks the
	// vault, and OPENS the session — so the operator is never prompted for the same master password
	// twice (the old unlock-gate 403 -> session-modal double prompt is gone).
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/unlock", `{"password":"pw"}`).Code)
	require.False(t, d.vault.Locked())
	require.Contains(t, req(t, h, "GET", "/operator/session/status", "").Body.String(), `"open":true`)
	// A subsequent gated mutation now succeeds without any further prompt (the session is open).
	require.Equal(t, http.StatusOK,
		req(t, h, "POST", "/rules", `{"upstream_id":"u1","outcome":"allow","browse_methods":"GET","browse_path":"/**"}`).Code)

	// A wrong password → 401, and neither the vault nor the operator session opens.
	d.vault.Lock()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/operator/session/lock", "").Code)
	require.Equal(t, http.StatusUnauthorized, req(t, h, "POST", "/vault/unlock", `{"password":"nope"}`).Code)
	require.True(t, d.vault.Locked(), "a failed unlock must not unlock the vault")
	require.Contains(t, req(t, h, "GET", "/operator/session/status", "").Body.String(), `"open":false`)
}

func TestOperatorSessionLockKeepsVaultUnlocked(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	require.False(t, d.vault.Locked()) // init leaves the vault unlocked

	// Locking the operator session must NOT lock the vault — the data plane keeps serving.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/operator/session/lock", "").Code)
	require.False(t, d.vault.Locked(), "operator-session lock must not lock the vault")
}
