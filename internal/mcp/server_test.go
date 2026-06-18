package mcp_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Sipaha/outwall/internal/access"
	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/approval"
	owmcp "github.com/Sipaha/outwall/internal/mcp"
	"github.com/Sipaha/outwall/internal/mcpsvc"
	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
	"github.com/Sipaha/outwall/internal/upstream"
)

func TestMCPWhoamiAndListUpstreams(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "i.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	v := secret.NewVault(s)
	require.NoError(t, v.Init("pw"))
	ag := agent.NewRegistry(s)
	up := upstream.NewRegistry(s, v)
	pol := policy.NewRegistry(s)
	acc := access.NewRegistry(s)
	_, err = up.Create("github", "https://api.github.com", upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)

	svc := mcpsvc.New(ag, up, pol, acc)
	svc.SetApprovals(approval.NewQueueWithTimeout(2 * time.Second))
	h, err := owmcp.NewHandler(owmcp.Deps{
		Svc: svc, Agents: ag, Locked: v.Locked,
	})
	require.NoError(t, err)
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-agent", Version: "0"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	// whoami → returns an agent id + token (auto-registered on first contact).
	who, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "whoami"})
	require.NoError(t, err)
	require.False(t, who.IsError, "%+v", who)
	whoOut, ok := who.StructuredContent.(map[string]any)
	require.True(t, ok, "%+v", who.StructuredContent)
	require.NotEmpty(t, whoOut["agent_id"])
	require.NotEmpty(t, whoOut["token"])

	// list_upstreams → contains github, status needs-request (no rule yet).
	got, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "list_upstreams"})
	require.NoError(t, err)
	require.False(t, got.IsError, "%+v", got)
	require.NotEmpty(t, got.Content)
	listOut, ok := got.StructuredContent.(map[string]any)
	require.True(t, ok)
	upstreams, ok := listOut["upstreams"].([]any)
	require.True(t, ok, "%+v", listOut)
	require.Len(t, upstreams, 1)
	first := upstreams[0].(map[string]any)
	require.Equal(t, "github", first["name"])
	require.Equal(t, "needs-request", first["status"])

	// request_host_access with empty purpose → tool error.
	bad, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "request_host_access", Arguments: map[string]any{"host": "github"},
	})
	require.NoError(t, err)
	require.True(t, bad.IsError, "%+v", bad)

	// request_host_access with a purpose → pending and an intent is logged.
	ra, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "request_host_access", Arguments: map[string]any{"host": "github", "purpose": "triage"},
	})
	require.NoError(t, err)
	require.False(t, ra.IsError, "%+v", ra)
	raOut := ra.StructuredContent.(map[string]any)
	require.Equal(t, "pending", raOut["status"])

	reqs, err := acc.List()
	require.NoError(t, err)
	require.Len(t, reqs, 1)
	require.Equal(t, "triage", reqs[0].Purpose)

	// request_access (operation) with a malformed template → tool error, no pending.
	opBad, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "request_access", Arguments: map[string]any{
			"host": "github", "method": "GET", "path_template": "/repos/{bad", "purpose": "x",
		},
	})
	require.NoError(t, err)
	require.True(t, opBad.IsError, "%+v", opBad)

	// an agent was registered.
	agents, _ := ag.List()
	require.GreaterOrEqual(t, len(agents), 1)
}
