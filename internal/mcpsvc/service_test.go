package mcpsvc

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/access"
	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
	"github.com/Sipaha/outwall/internal/upstream"
)

func build(t *testing.T) (*Service, *agent.Registry, *upstream.Registry, *policy.Registry) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "m.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	v := secret.NewVault(s)
	require.NoError(t, v.Init("pw"))
	ag := agent.NewRegistry(s)
	up := upstream.NewRegistry(s, v)
	pol := policy.NewRegistry(s)
	acc := access.NewRegistry(s)
	return New(ag, up, pol, acc), ag, up, pol
}

func TestRequestAccessFlow(t *testing.T) {
	svc, ag, up, pol := build(t)
	a, _, _ := ag.Register("claude")
	u, _ := up.Create("github", "https://api.github.com", upstream.AuthConfig{Type: "none"})

	// No rule yet → pending-approval, and an access-request is logged.
	res, err := svc.RequestAccess(a.ID, "github", "triage issues")
	require.NoError(t, err)
	require.Equal(t, "pending-approval", res.Status)

	// Resolving by HOST also works.
	res, _ = svc.RequestAccess(a.ID, "api.github.com", "via host")
	require.Equal(t, "pending-approval", res.Status)

	// Unknown upstream → denied, no record.
	res, _ = svc.RequestAccess(a.ID, "nope.example.com", "x")
	require.Equal(t, "denied", res.Status)

	// Operator grants via an allow rule → granted with base path.
	_, err = pol.Create(policy.Rule{UpstreamID: u.ID, Method: "*", PathGlob: "/**", Outcome: policy.Allow})
	require.NoError(t, err)
	res, _ = svc.GetAccess(a.ID, "github")
	require.Equal(t, "granted", res.Status)
	require.Equal(t, "/github", res.BasePath)

	// list_upstreams reflects open status.
	list, _ := svc.ListUpstreams(a.ID)
	require.Len(t, list, 1)
	require.Equal(t, "open", list[0].Status)

	// whoami
	id, _ := svc.WhoAmI(a.ID)
	require.Equal(t, a.ID, id.AgentID)
	require.Contains(t, id.Accesses, "github")

	// agent-specific deny overrides → denied.
	_, err = pol.Create(policy.Rule{SubjectAgentID: a.ID, UpstreamID: u.ID, Method: "*", PathGlob: "/**", Outcome: policy.Deny})
	require.NoError(t, err)
	res, _ = svc.GetAccess(a.ID, "github")
	require.Equal(t, "denied", res.Status)
}
