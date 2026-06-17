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

func TestK8sClusterDiscoveryAndKubeconfig(t *testing.T) {
	svc, ag, up, pol := build(t)
	a, token, _ := ag.Register("claude")

	cl, err := up.CreateKind("prod-cluster", "https://api.k8s:6443", upstream.KindK8s,
		upstream.AuthConfig{Type: "none", K8sAuth: "token", Token: "cluster-secret"})
	require.NoError(t, err)
	_, err = pol.Create(policy.Rule{UpstreamID: cl.ID, Namespace: "prod", Resource: "pods", Verb: "list", Outcome: policy.Allow})
	require.NoError(t, err)

	// list_upstreams surfaces the cluster with kind=k8s.
	svc.SetKubeconfigParams("https://127.0.0.1:8080", "CA-PEM-BYTES")
	list, err := svc.ListUpstreams(a.ID)
	require.NoError(t, err)
	var found *UpstreamInfo
	for i := range list {
		if list[i].Name == "prod-cluster" {
			found = &list[i]
		}
	}
	require.NotNil(t, found)
	require.Equal(t, "k8s", found.Kind)
	require.Equal(t, "open", found.Status)

	// get_kubeconfig returns YAML carrying the calling agent's token.
	yamlBytes, err := svc.Kubeconfig("prod-cluster", token)
	require.NoError(t, err)
	require.Contains(t, string(yamlBytes), "token: "+token)
	require.Contains(t, string(yamlBytes), "https://127.0.0.1:8080/prod-cluster")

	// A non-k8s upstream cannot produce a kubeconfig.
	_, err = up.Create("plain", "https://api.example", upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)
	_, err = svc.Kubeconfig("plain", token)
	require.Error(t, err)
}
