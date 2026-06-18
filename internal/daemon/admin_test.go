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
)

func newDaemon(t *testing.T) *Daemon {
	t.Helper()
	// Point kubeconfig discovery at a nonexistent path so the unlock auto-import is a
	// deterministic no-op — tests must not read (or import) the host's real ~/.kube/config.
	// Tests that exercise auto-import override KUBECONFIG with their own temp file afterward.
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "no-kubeconfig"))
	d, err := New(Config{
		DBPath:     filepath.Join(t.TempDir(), "d.db"),
		SocketPath: filepath.Join(t.TempDir(), "d.sock"),
		Listen:     "127.0.0.1:0",
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

	wr := req(t, h, "POST", "/rules", `{"upstream_id":"`+up["id"]+`","method":"*","path_glob":"/**","outcome":"allow"}`)
	require.Equal(t, http.StatusOK, wr.Code, wr.Body.String())

	wl := req(t, h, "GET", "/rules", "")
	require.Equal(t, http.StatusOK, wl.Code)
	require.Contains(t, wl.Body.String(), up["id"])

	// resolving an unknown approval → 404.
	require.Equal(t, http.StatusNotFound, req(t, h, "POST", "/approvals/nope/resolve", `{"approve":true}`).Code)
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

func TestUICSRFGate(t *testing.T) {
	d := newDaemon(t)
	h := d.UIHandler() // the static + /api TCP mux
	// no CSRF header → 403 (API is mounted under /api)
	r1 := httptest.NewRequest("GET", "/api/vault/status", nil)
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, r1)
	require.Equal(t, http.StatusForbidden, w1.Code)
	// with CSRF header → passes through (200)
	r2 := httptest.NewRequest("GET", "/api/vault/status", nil)
	r2.Header.Set("X-Outwall-CSRF", "1")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)
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

func TestUISSEExemptFromCSRF(t *testing.T) {
	d := newDaemon(t)
	// Use a real server + client: the SSE handler streams on its own goroutine, so reading
	// a shared httptest.ResponseRecorder from the test goroutine would race. The HTTP client
	// returns once response headers arrive (read-synchronized), and canceling the request
	// context unblocks the streaming handler.
	srv := httptest.NewServer(d.UIHandler())
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// No X-Outwall-CSRF header — EventSource cannot set one, so the gate must exempt /api/events.
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode) // 200, not 403 from the CSRF gate
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
}

func TestVaultUnlockAutoImportsClusters(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()

	// A kubeconfig with two contexts on disk, pointed at by $KUBECONFIG.
	src := `
apiVersion: v1
kind: Config
clusters:
  - name: c1
    cluster:
      server: https://c1.example:6443
      insecure-skip-tls-verify: true
  - name: c2
    cluster:
      server: https://c2.example:6443
      insecure-skip-tls-verify: true
users:
  - name: u
    user:
      token: t
contexts:
  - name: kc-ctx-1
    context: { cluster: c1, user: u }
  - name: kc-ctx-2
    context: { cluster: c2, user: u }
`
	kcPath := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(kcPath, []byte(src), 0o600))
	t.Setenv("KUBECONFIG", kcPath)

	// init leaves the vault unlocked; lock then unlock to drive the unlock hook.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	d.vault.Lock()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/unlock", `{"password":"pw"}`).Code)

	// The two kubeconfig contexts are now kind=k8s upstreams.
	wl := req(t, h, "GET", "/upstreams", "")
	require.Equal(t, http.StatusOK, wl.Code)
	var ups []map[string]any
	require.NoError(t, json.Unmarshal(wl.Body.Bytes(), &ups))
	names := map[string]any{}
	for _, u := range ups {
		names[fmt.Sprint(u["name"])] = u["kind"]
	}
	require.Equal(t, "k8s", names["kc-ctx-1"])
	require.Equal(t, "k8s", names["kc-ctx-2"])

	// Unlock again (idempotent) — still exactly the two clusters, no duplicates.
	d.vault.Lock()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/unlock", `{"password":"pw"}`).Code)
	wl2 := req(t, h, "GET", "/upstreams", "")
	var ups2 []map[string]any
	require.NoError(t, json.Unmarshal(wl2.Body.Bytes(), &ups2))
	count := 0
	for _, u := range ups2 {
		if u["name"] == "kc-ctx-1" || u["name"] == "kc-ctx-2" {
			count++
		}
	}
	require.Equal(t, 2, count, "auto-import must be idempotent across unlocks")
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
	t.Setenv("KUBECONFIG", kcPath)

	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	w := req(t, h, "POST", "/clusters/import", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var res struct {
		Added   []string `json:"added"`
		Skipped []string `json:"skipped"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Contains(t, res.Added, "ep-ctx")

	// Second call → skipped, nothing added.
	w2 := req(t, h, "POST", "/clusters/import", "")
	require.Equal(t, http.StatusOK, w2.Code)
	var res2 struct {
		Added   []string `json:"added"`
		Skipped []string `json:"skipped"`
	}
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &res2))
	require.Contains(t, res2.Skipped, "ep-ctx")
	require.Empty(t, res2.Added)
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
