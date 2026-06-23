package mcpsvc

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/access"
	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/approval"
	"github.com/Sipaha/outwall/internal/events"
	"github.com/Sipaha/outwall/internal/optemplate"
	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
	"github.com/Sipaha/outwall/internal/upstream"

	_ "github.com/Sipaha/outwall/internal/serverprofile/citeck" // register the citeck profile
)

// testDeps holds all registries + the queue produced by newTestService.
type testDeps struct {
	agents    *agent.Registry
	upstreams *upstream.Registry
	pol       *policy.Registry
	queue     *approval.Queue
}

// mustUpstream creates a profiled upstream (kind + profile) and returns it.
func (d *testDeps) mustUpstream(t *testing.T, name, baseURL, kind, profile string) *upstream.Upstream {
	t.Helper()
	up, err := d.upstreams.CreateProfiled(name, baseURL, kind, profile, upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)
	return up
}

// mustAgent registers an agent and returns its ID.
func (d *testDeps) mustAgent(t *testing.T, name string) string {
	t.Helper()
	a, _, err := d.agents.Register(name)
	require.NoError(t, err)
	return a.ID
}

// newTestService constructs a Service with a fast-timeout queue, mirroring buildWithQueue.
func newTestService(t *testing.T) (*Service, *testDeps) {
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
	svc := New(ag, up, pol, acc)
	q := approval.NewQueueWithTimeout(2 * time.Second)
	svc.SetApprovals(q)
	deps := &testDeps{agents: ag, upstreams: up, pol: pol, queue: q}
	return svc, deps
}

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

// buildWithQueue is build plus a fast-timeout approval queue wired into the service, returned so a
// test can inspect the parked pendings.
func buildWithQueue(t *testing.T) (*Service, *agent.Registry, *upstream.Registry, *policy.Registry, *approval.Queue) {
	svc, ag, up, pol := build(t)
	q := approval.NewQueueWithTimeout(2 * time.Second)
	svc.SetApprovals(q)
	return svc, ag, up, pol, q
}

// waitPending polls the queue until at least one pending appears (the service enqueues from a
// background goroutine so Submit can park without blocking the MCP call), or fails after a bound.
func waitPending(t *testing.T, q *approval.Queue) approval.Pending {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ps := q.List(); len(ps) > 0 {
			return ps[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no pending approval enqueued")
	return approval.Pending{}
}

func mustKey(t *testing.T, method, path string, query map[string]string) string {
	t.Helper()
	tmpl, err := optemplate.Parse(method, path, query)
	require.NoError(t, err)
	return tmpl.Key()
}

func TestRequestHostAccessEnqueuesHostApproval(t *testing.T) {
	svc, ag, up, _, q := buildWithQueue(t)
	a, _, _ := ag.Register("claude")

	res, err := svc.RequestHostAccess(a.ID, "gitlab.example", "check CI state")
	require.NoError(t, err)
	require.Equal(t, "pending", res.Status)

	// The host was lazily created and a host approval is parked carrying it + the purpose.
	hostUp, err := up.GetByName("gitlab.example")
	require.NoError(t, err)
	p := waitPending(t, q)
	require.Equal(t, approval.KindHostAccess, p.Kind)
	require.Equal(t, "gitlab.example", p.Host)
	require.Equal(t, hostUp.ID, p.UpstreamID)
	require.Equal(t, "check CI state", p.Purpose)
}

func TestRequestAccessEnqueuesOperationApproval(t *testing.T) {
	svc, ag, up, _, q := buildWithQueue(t)
	a, _, _ := ag.Register("claude")
	// The host must exist (tier-1 host access happens first).
	_, _, err := up.GetOrCreateByHost("gitlab.example")
	require.NoError(t, err)

	res, err := svc.RequestAccess(a.ID, RequestAccessInput{
		Host:          "gitlab.example",
		Method:        "GET",
		PathTemplate:  "/api/v4/projects/{project_path:text}/pipelines",
		QueryTemplate: map[string]string{"updated_after": "{since:date}"},
		Variables:     []Variable{{Name: "project_path", Type: "text"}, {Name: "since", Type: "date"}},
		Values:        map[string]string{"project_path": "infra/helm"},
		Purpose:       "check CI state",
	})
	require.NoError(t, err)
	require.Equal(t, "pending", res.Status)

	p := waitPending(t, q)
	require.Equal(t, approval.KindOperation, p.Kind)
	require.Equal(t, "GET", p.OpMethod)
	require.Equal(t, "/api/v4/projects/{project_path:text}/pipelines", p.OpPathTemplate)
	require.Equal(t, "infra/helm", p.OpValues["project_path"])
	require.Equal(t, "check CI state", p.Purpose)

	// The pending's shape reparses to a valid template Key (the H1 rule identity).
	require.NotEmpty(t, mustKey(t, p.OpMethod, p.OpPathTemplate, p.OpQueryTemplate))

	// A malformed template is a tool error, not a pending.
	_, err = svc.RequestAccess(a.ID, RequestAccessInput{
		Host:         "gitlab.example",
		Method:       "GET",
		PathTemplate: "/api/v4/projects/{bad", // unterminated placeholder
		Variables:    []Variable{{Name: "bad", Type: "text"}},
		Purpose:      "x",
	})
	require.Error(t, err)
}

func TestGetAccessSurfacesDenyReason(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "m.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	v := secret.NewVault(s)
	require.NoError(t, v.Init("pw"))
	ag := agent.NewRegistry(s)
	up := upstream.NewRegistry(s, v)
	pol := policy.NewRegistry(s)
	acc := access.NewRegistry(s)
	svc := New(ag, up, pol, acc)

	a, _, err := ag.Register("claude")
	require.NoError(t, err)
	u, err := up.Create("api.github.com", "https://api.github.com", upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)
	_, err = acc.Create(a.ID, u.ID, "read")
	require.NoError(t, err)

	// Before any decision: not denied (no granting rule → pending/needs-request).
	res, err := svc.GetAccess(a.ID, "api.github.com")
	require.NoError(t, err)
	require.NotEqual(t, "denied", res.Status)

	// Operator denies the latest request with a reason → get_access surfaces it.
	ok, err := acc.DenyLatest(a.ID, u.ID, "prod is off limits")
	require.NoError(t, err)
	require.True(t, ok)

	res, err = svc.GetAccess(a.ID, "api.github.com")
	require.NoError(t, err)
	require.Equal(t, "denied", res.Status)
	require.Contains(t, res.Memo, "prod is off limits")
}

func TestRequestHostAccessAndStatusFlow(t *testing.T) {
	svc, ag, up, pol, _ := buildWithQueue(t)
	a, _, _ := ag.Register("claude")

	// No rule yet → pending, the host is lazily created, and an access-request is logged.
	res, err := svc.RequestHostAccess(a.ID, "api.github.com", "triage issues")
	require.NoError(t, err)
	require.Equal(t, "pending", res.Status)
	u, err := up.GetByName("api.github.com")
	require.NoError(t, err)

	// Operator grants via an allow rule → get_access reports granted with base path.
	_, err = pol.Create(policy.Rule{UpstreamID: u.ID, OpMethod: "GET", OpPathTemplate: "/repos/{repo:text}", Outcome: policy.Allow})
	require.NoError(t, err)
	res, _ = svc.GetAccess(a.ID, "api.github.com")
	require.Equal(t, "granted", res.Status)
	require.Equal(t, "/api.github.com", res.BasePath)

	// Already open → request_host_access short-circuits to granted (no host card).
	res, err = svc.RequestHostAccess(a.ID, "api.github.com", "again")
	require.NoError(t, err)
	require.Equal(t, "granted", res.Status)

	// list_upstreams reflects open status.
	list, _ := svc.ListUpstreams(a.ID)
	require.Len(t, list, 1)
	require.Equal(t, "open", list[0].Status)

	// whoami
	id, _ := svc.WhoAmI(a.ID)
	require.Equal(t, a.ID, id.AgentID)
	require.Contains(t, id.Accesses, "api.github.com")

	// agent-specific deny overrides → denied.
	_, err = pol.Create(policy.Rule{SubjectAgentID: a.ID, UpstreamID: u.ID, OpMethod: "GET", OpPathTemplate: "/repos/{repo:text}", Outcome: policy.Deny})
	require.NoError(t, err)
	res, _ = svc.GetAccess(a.ID, "api.github.com")
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

// TestGetAccessIncludesBrowseURL asserts that get_access returns a browse_url for http upstreams
// when a browse domain is configured, and returns empty for k8s upstreams.
func TestGetAccessIncludesBrowseURL(t *testing.T) {
	svc, ag, up, pol := build(t)
	a, _, _ := ag.Register("claude")
	svc.SetKubeconfigParams("https://127.0.0.1:8099", "ca")
	svc.SetBrowseDomain("outwall.localhost")

	// HTTP upstream "be" with an allow rule.
	beUp, err := up.Create("be", "https://be.example", upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)
	_, err = pol.Create(policy.Rule{UpstreamID: beUp.ID, OpMethod: "GET", OpPathTemplate: "/", Outcome: policy.Allow})
	require.NoError(t, err)

	res, err := svc.GetAccess(a.ID, "be")
	require.NoError(t, err)
	require.Equal(t, "granted", res.Status)
	require.Equal(t, "https://be.outwall.localhost:8099", res.BrowseURL)

	// K8s upstream yields no browse_url.
	cl, err := up.CreateKind("prod-cluster", "https://api.k8s:6443", upstream.KindK8s,
		upstream.AuthConfig{Type: "none", K8sAuth: "token", Token: "cluster-secret"})
	require.NoError(t, err)
	_, err = pol.Create(policy.Rule{UpstreamID: cl.ID, Namespace: "ns", Resource: "pods", Verb: "get", Outcome: policy.Allow})
	require.NoError(t, err)

	res2, err := svc.GetAccess(a.ID, "prod-cluster")
	require.NoError(t, err)
	require.Equal(t, "granted", res2.Status)
	require.Equal(t, "", res2.BrowseURL)
}

func TestRequestK8sAccessEnqueuesK8sApproval(t *testing.T) {
	svc, ag, up, _, q := buildWithQueue(t)
	a, _, _ := ag.Register("claude")
	cl, err := up.CreateKind("prod-cluster", "https://api.k8s:6443", upstream.KindK8s,
		upstream.AuthConfig{Type: "none", K8sAuth: "token", Token: "cluster-secret"})
	require.NoError(t, err)

	res, err := svc.RequestK8sAccess(a.ID, "prod-cluster", "enterprise-ecos24", []K8sAccessSpec{{Resource: "pods/log", Verbs: []string{"get"}}}, "read ecos-model logs")
	require.NoError(t, err)
	require.Equal(t, "pending", res.Status)
	require.Equal(t, "/prod-cluster", res.BasePath)

	p := waitPending(t, q)
	require.Equal(t, approval.KindK8sAccess, p.Kind)
	require.Equal(t, cl.ID, p.UpstreamID)
	require.Equal(t, "prod-cluster", p.Host)
	require.Equal(t, "enterprise-ecos24", p.Namespace)
	require.Equal(t, "pods/log", p.Resource)
	require.Equal(t, "get", p.Verb)
	require.Equal(t, "read ecos-model logs", p.Purpose)
	require.Equal(t, []approval.K8sGrant{{Namespace: "enterprise-ecos24", Resource: "pods/log", Verb: "get"}}, p.K8sGrants)
}

// TestRequestK8sAccessMultiGrant: one call with several resources/verbs raises ONE card carrying all
// the tuples; a re-request after some are granted only asks for what is still missing.
func TestRequestK8sAccessMultiGrant(t *testing.T) {
	svc, ag, up, pol, q := buildWithQueue(t)
	a, _, _ := ag.Register("claude")
	cl, err := up.CreateKind("prod-cluster", "https://api.k8s:6443", upstream.KindK8s,
		upstream.AuthConfig{Type: "none", K8sAuth: "token", Token: "secret"})
	require.NoError(t, err)

	res, err := svc.RequestK8sAccess(a.ID, "prod-cluster", "enterprise-ecos24", []K8sAccessSpec{
		{Resource: "pods", Verbs: []string{"get", "list"}},
		{Resource: "pods/log", Verbs: []string{"get"}},
	}, "read ecos-model logs")
	require.NoError(t, err)
	require.Equal(t, "pending", res.Status)

	p := waitPending(t, q)
	require.Equal(t, approval.KindK8sAccess, p.Kind)
	require.ElementsMatch(t, []approval.K8sGrant{
		{Namespace: "enterprise-ecos24", Resource: "pods", Verb: "get"},
		{Namespace: "enterprise-ecos24", Resource: "pods", Verb: "list"},
		{Namespace: "enterprise-ecos24", Resource: "pods/log", Verb: "get"},
	}, p.K8sGrants)
	require.Len(t, q.List(), 1, "one call → one card for all tuples")

	// Grant pods/get + pods/list (leave pods/log/get ungranted), then re-request the same set:
	// only the still-missing tuple should be asked for.
	for _, v := range []string{"get", "list"} {
		_, err = pol.Create(policy.Rule{SubjectAgentID: a.ID, UpstreamID: cl.ID, Outcome: policy.Allow,
			Namespace: "enterprise-ecos24", Resource: "pods", Verb: v})
		require.NoError(t, err)
	}
	res2, err := svc.RequestK8sAccess(a.ID, "prod-cluster", "enterprise-ecos24", []K8sAccessSpec{
		{Resource: "pods", Verbs: []string{"get", "list"}},
		{Resource: "pods/log", Verbs: []string{"get"}},
	}, "read ecos-model logs")
	require.NoError(t, err)
	require.Equal(t, "pending", res2.Status)
	// The new card (enqueued asynchronously) carries ONLY pods/log/get — pods get+list are already
	// covered by rules, so they are not re-requested.
	var newCard approval.Pending
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, pp := range q.List() {
			if len(pp.K8sGrants) == 1 {
				newCard = pp
			}
		}
		if len(newCard.K8sGrants) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.Equal(t, []approval.K8sGrant{{Namespace: "enterprise-ecos24", Resource: "pods/log", Verb: "get"}}, newCard.K8sGrants)
}

func TestRequestK8sAccessDedupesPending(t *testing.T) {
	svc, ag, up, _, q := buildWithQueue(t)
	a, _, _ := ag.Register("claude")
	_, err := up.CreateKind("prod-cluster", "https://api.k8s:6443", upstream.KindK8s,
		upstream.AuthConfig{Type: "none", K8sAuth: "token", Token: "secret"})
	require.NoError(t, err)

	_, err = svc.RequestK8sAccess(a.ID, "prod-cluster", "enterprise-ecos24", []K8sAccessSpec{{Resource: "pods/log", Verbs: []string{"get"}}}, "logs")
	require.NoError(t, err)
	waitPending(t, q) // the first request is parked

	// A second identical request must NOT raise a second card or log a second intent row.
	res, err := svc.RequestK8sAccess(a.ID, "prod-cluster", "enterprise-ecos24", []K8sAccessSpec{{Resource: "pods/log", Verbs: []string{"get"}}}, "logs")
	require.NoError(t, err)
	require.Equal(t, "pending", res.Status)

	require.Len(t, q.List(), 1, "duplicate request must not enqueue a second approval")
	reqs, err := svc.access.List()
	require.NoError(t, err)
	require.Len(t, reqs, 1, "duplicate request must not log a second access-request row")
}

// TestGetAccessLongPollReturnsOnGrant verifies get_access blocks on a pending request and returns
// promptly (well under getAccessWait) once the operator grants — woken by the approval event.
func TestGetAccessLongPollReturnsOnGrant(t *testing.T) {
	svc, ag, up, pol, q := buildWithQueue(t)
	bus := events.NewBus()
	svc.SetEvents(bus.Subscribe)
	a, _, _ := ag.Register("claude")
	cl, err := up.CreateKind("prod-cluster", "https://api.k8s:6443", upstream.KindK8s,
		upstream.AuthConfig{Type: "none", K8sAuth: "token", Token: "secret"})
	require.NoError(t, err)

	_, err = svc.RequestK8sAccess(a.ID, "prod-cluster", "enterprise-ecos24", []K8sAccessSpec{{Resource: "pods/log", Verbs: []string{"get"}}}, "logs")
	require.NoError(t, err)
	waitPending(t, q)

	// Operator grants shortly after: create the rule, then publish the resolution event.
	go func() {
		time.Sleep(80 * time.Millisecond)
		_, _ = pol.Create(policy.Rule{SubjectAgentID: a.ID, UpstreamID: cl.ID, Outcome: policy.Allow,
			Namespace: "enterprise-ecos24", Resource: "pods/log", Verb: "get"})
		bus.Publish("approval.resolved", map[string]any{"approved": true})
	}()

	start := time.Now()
	res, err := svc.GetAccess(a.ID, "prod-cluster")
	require.NoError(t, err)
	require.Equal(t, "granted", res.Status)
	require.Less(t, time.Since(start), 5*time.Second, "should return on the event, not after the full wait")
}

func TestRequestK8sAccessRejectsNonK8s(t *testing.T) {
	svc, ag, up, _, _ := buildWithQueue(t)
	a, _, _ := ag.Register("claude")
	_, err := up.Create("api.github.com", "https://api.github.com", upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)

	_, err = svc.RequestK8sAccess(a.ID, "api.github.com", "ns", []K8sAccessSpec{{Resource: "pods", Verbs: []string{"get"}}}, "x")
	require.Error(t, err)
}

func TestRequestHostAccessOnK8sReturnsGuidanceNoCard(t *testing.T) {
	svc, ag, up, _, q := buildWithQueue(t)
	a, _, _ := ag.Register("claude")
	_, err := up.CreateKind("prod-cluster", "https://api.k8s:6443", upstream.KindK8s,
		upstream.AuthConfig{Type: "none", K8sAuth: "token", Token: "cluster-secret"})
	require.NoError(t, err)

	res, err := svc.RequestHostAccess(a.ID, "prod-cluster", "read logs")
	require.NoError(t, err)
	require.Equal(t, "granted", res.Status)
	require.Contains(t, res.Memo, "request_k8s_access")

	// No host card is parked for a k8s cluster.
	require.Empty(t, q.List())
	// No access-request row was logged either (nothing for the operator to act on).
	reqs, err := svc.access.List()
	require.NoError(t, err)
	require.Empty(t, reqs)
}

func TestRequestHostAccessOnCredentialedHTTPHostReturnsGuidanceNoCard(t *testing.T) {
	svc, ag, up, _, q := buildWithQueue(t)
	a, _, _ := ag.Register("claude")
	_, err := up.Create("api.github.com", "https://api.github.com",
		upstream.AuthConfig{Type: "static", Header: "Authorization", Token: "tok"})
	require.NoError(t, err)

	res, err := svc.RequestHostAccess(a.ID, "api.github.com", "triage")
	require.NoError(t, err)
	require.Equal(t, "granted", res.Status)
	require.Contains(t, res.Memo, "request_access")
	require.Empty(t, q.List())
}

func TestRequestPresetEnqueuesAndValidates(t *testing.T) {
	svc, deps := newTestService(t)
	up := deps.mustUpstream(t, "cite.test", "https://cite.test", "http", "citeck")
	agentID := deps.mustAgent(t, "a1")

	// Unknown preset → error, no card.
	_, err := svc.RequestPreset(agentID, RequestPresetInput{Host: "cite.test", PresetID: "nope", Purpose: "x"})
	require.Error(t, err)

	// "*" workspace is not allowed for the citeck presets → error.
	_, err = svc.RequestPreset(agentID, RequestPresetInput{
		Host: "cite.test", PresetID: "citeck-readonly",
		Bindings: map[string]string{"sourceId": "*", "workspace": "*"}, Purpose: "x"})
	require.Error(t, err)

	// Valid → pending + one KindPreset card carrying the preset id + bindings.
	res, err := svc.RequestPreset(agentID, RequestPresetInput{
		Host: "cite.test", PresetID: "citeck-readonly",
		Bindings: map[string]string{"sourceId": "*", "workspace": "proj-x"}, Purpose: "read prod"})
	require.NoError(t, err)
	require.Equal(t, "pending", res.Status)
	var card approval.Pending
	require.Eventually(t, func() bool {
		for _, p := range deps.queue.List() {
			if p.Kind == approval.KindPreset {
				card = p
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, "citeck-readonly", card.PresetID)
	require.Equal(t, "proj-x", card.Bindings["workspace"])
	require.Equal(t, agentID, card.AgentID)
	require.Equal(t, up.ID, card.UpstreamID)
}

func TestListUpstreamsCarriesPresets(t *testing.T) {
	svc, deps := newTestService(t)
	deps.mustUpstream(t, "cite.test", "https://cite.test", "http", "citeck")
	agentID := deps.mustAgent(t, "a1")
	infos, err := svc.ListUpstreams(agentID)
	require.NoError(t, err)
	require.Len(t, infos, 1)
	require.Equal(t, "citeck", infos[0].Profile)
	ids := map[string]bool{}
	for _, p := range infos[0].Presets {
		ids[p.ID] = true
	}
	require.True(t, ids["browse-get"])
	require.True(t, ids["citeck-readonly"])
	require.True(t, ids["citeck-readwrite"])
}
