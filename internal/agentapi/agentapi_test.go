package agentapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/access"
	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/agentapi"
	"github.com/Sipaha/outwall/internal/approval"
	"github.com/Sipaha/outwall/internal/mcpsvc"
	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
	"github.com/Sipaha/outwall/internal/upstream"
)

type testEnv struct {
	ts     *httptest.Server
	agents *agent.Registry
	ups    *upstream.Registry
	vault  *secret.Vault
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	v := secret.NewVault(s)
	require.NoError(t, v.Init("pw"))
	ag := agent.NewRegistry(s)
	up := upstream.NewRegistry(s, v)
	pol := policy.NewRegistry(s)
	acc := access.NewRegistry(s)
	svc := mcpsvc.New(ag, up, pol, acc)
	svc.SetApprovals(approval.NewQueue())
	h := agentapi.NewHandler(agentapi.Deps{Svc: svc, Agents: ag, Locked: v.Locked})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return &testEnv{ts: ts, agents: ag, ups: up, vault: v}
}

// doJSON issues a request to the agent socket test server and asserts the status code.
func (e *testEnv) doJSON(t *testing.T, token, method, path string, body, out any, wantCode int) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, rdr)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, wantCode, resp.StatusCode)
	if out != nil {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(out))
	}
}

func TestRegisterWhoAmIAndAuth(t *testing.T) {
	e := newEnv(t)

	var reg struct{ ID, Token string }
	e.doJSON(t, "", "POST", "/register", map[string]string{"name": "proj-x"}, &reg, http.StatusOK)
	require.NotEmpty(t, reg.ID)
	require.NotEmpty(t, reg.Token)

	var who map[string]any
	e.doJSON(t, reg.Token, "GET", "/whoami", nil, &who, http.StatusOK)
	require.Equal(t, reg.ID, who["agent_id"])
	require.Equal(t, reg.Token, who["token"])

	// Missing bearer → 401.
	e.doJSON(t, "", "GET", "/whoami", nil, nil, http.StatusUnauthorized)
	// Garbage bearer → 401.
	e.doJSON(t, "owa_nope", "GET", "/whoami", nil, nil, http.StatusUnauthorized)
}

func TestAccessRoutes(t *testing.T) {
	e := newEnv(t)
	_, err := e.ups.Create("github", "https://api.github.com", upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)

	var reg struct{ ID, Token string }
	e.doJSON(t, "", "POST", "/register", map[string]string{"name": "proj"}, &reg, http.StatusOK)

	// list_upstreams → contains github, status needs-request (no rule yet).
	var list struct {
		Upstreams []mcpsvc.UpstreamInfo `json:"upstreams"`
	}
	e.doJSON(t, reg.Token, "GET", "/upstreams", nil, &list, http.StatusOK)
	require.Len(t, list.Upstreams, 1)
	require.Equal(t, "github", list.Upstreams[0].Name)
	require.Equal(t, "needs-request", list.Upstreams[0].Status)

	// request-host-access requires a purpose.
	e.doJSON(t, reg.Token, "POST", "/access/host",
		map[string]string{"host": "github"}, nil, http.StatusBadRequest)

	// request-host-access with a purpose → 200, pending.
	var hostRes mcpsvc.AccessResult
	e.doJSON(t, reg.Token, "POST", "/access/host",
		map[string]string{"host": "github", "purpose": "read repos"}, &hostRes, http.StatusOK)
	require.Equal(t, "pending", hostRes.Status)

	// get-access → 200 (no pending long-poll here since request logging is enabled).
	var getRes mcpsvc.AccessResult
	e.doJSON(t, reg.Token, "GET", "/access/github", nil, &getRes, http.StatusOK)
	require.NotEmpty(t, getRes.Status)

	// kubeconfig for a non-cluster upstream → 400 (service error surfaced as JSON).
	e.doJSON(t, reg.Token, "GET", "/kubeconfig/github", nil, nil, http.StatusBadRequest)
}

func TestLockedReturns409(t *testing.T) {
	e := newEnv(t)
	var reg struct{ ID, Token string }
	e.doJSON(t, "", "POST", "/register", map[string]string{"name": "proj"}, &reg, http.StatusOK)

	e.vault.Lock() // Lock() returns no error

	// A locked vault fails the list/access routes with a clear 409 JSON message.
	var errBody struct {
		Error string `json:"error"`
	}
	e.doJSON(t, reg.Token, "GET", "/upstreams", nil, &errBody, http.StatusConflict)
	require.Contains(t, errBody.Error, "vault locked")
}
