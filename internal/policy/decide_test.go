package policy

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDecideHTTPOperation(t *testing.T) {
	reg := newReg(t)
	mk(t, reg, Rule{
		UpstreamID:      "u1",
		OpMethod:        "GET",
		OpPathTemplate:  "/api/v4/projects/{project_path:text}/pipelines",
		OpQueryTemplate: map[string]string{"updated_after": "{since:date}"},
		OpValuePolicies: map[string]ValuePolicy{
			"project_path": {Type: "text", Mode: "set", Values: []string{"infra/helm"}},
			"since":        {Type: "date", Mode: "any"},
		},
		Outcome: Allow,
	})

	in := func(path, query string) Input {
		q, err := url.ParseQuery(query)
		require.NoError(t, err)
		return Input{AgentID: "a1", UpstreamID: "u1", Method: "GET", Path: path, Query: q}
	}

	// allowed value in the set → allow, with vars extracted
	d, err := reg.Decide(in("/api/v4/projects/infra%2Fhelm/pipelines", "updated_after=2026-06-01"))
	require.NoError(t, err)
	require.Equal(t, Allow, d.Outcome)
	require.Equal(t, "infra/helm", d.Vars["project_path"])
	require.Equal(t, "2026-06-01", d.Vars["since"])
	require.Empty(t, d.NewValues)

	// a new text value → require-approval with the NewValues pair
	d, err = reg.Decide(in("/api/v4/projects/other/pipelines", "updated_after=2026-06-01"))
	require.NoError(t, err)
	require.Equal(t, RequireApproval, d.Outcome)
	require.Equal(t, []VarValue{{Var: "project_path", Value: "other"}}, d.NewValues)
	require.NotNil(t, d.Rule)

	// a path that does not match any template → deny
	d, err = reg.Decide(in("/api/v4/projects/infra%2Fhelm/builds", "updated_after=2026-06-01"))
	require.NoError(t, err)
	require.Equal(t, Deny, d.Outcome)

	// a date var that is not a date → no structural match → deny
	d, err = reg.Decide(in("/api/v4/projects/infra%2Fhelm/pipelines", "updated_after=notadate"))
	require.NoError(t, err)
	require.Equal(t, Deny, d.Outcome)
}

func TestDecideNumberAndEnum(t *testing.T) {
	reg := newReg(t)
	min, max := 1.0, 100.0
	mk(t, reg, Rule{
		UpstreamID:      "u1",
		OpMethod:        "GET",
		OpPathTemplate:  "/items/{id:number}",
		OpQueryTemplate: map[string]string{"sort": "{order:enum}", "limit": "{n:number}"},
		OpValuePolicies: map[string]ValuePolicy{
			"id":    {Type: "number", Mode: "any"},
			"order": {Type: "enum", Mode: "set", Values: []string{"asc", "desc"}},
			"n":     {Type: "number", Mode: "range", Min: &min, Max: &max},
		},
		Outcome: Allow,
	})

	in := func(path, query string) Input {
		q, err := url.ParseQuery(query)
		require.NoError(t, err)
		return Input{AgentID: "a1", UpstreamID: "u1", Method: "GET", Path: path, Query: q}
	}

	// enum in-set + number in-range → allow
	d, err := reg.Decide(in("/items/42", "sort=asc&limit=10"))
	require.NoError(t, err)
	require.Equal(t, Allow, d.Outcome)
	require.Equal(t, "42", d.Vars["id"])

	// enum out-of-set → HARD deny (no approval, no NewValues)
	d, err = reg.Decide(in("/items/42", "sort=sideways&limit=10"))
	require.NoError(t, err)
	require.Equal(t, Deny, d.Outcome)
	require.Empty(t, d.NewValues)

	// number out-of-range → hard deny
	d, err = reg.Decide(in("/items/42", "sort=asc&limit=9999"))
	require.NoError(t, err)
	require.Equal(t, Deny, d.Outcome)

	// non-numeric path segment for a number var → no structural match → default-deny
	d, err = reg.Decide(in("/items/abc", "sort=asc&limit=10"))
	require.NoError(t, err)
	require.Equal(t, Deny, d.Outcome)
	require.Nil(t, d.Rule)
}

func TestDecideHTTPBodyVariables(t *testing.T) {
	reg := newReg(t)
	mk(t, reg, Rule{
		UpstreamID:     "u1",
		OpMethod:       "POST",
		OpPathTemplate: "/widgets",
		OpBodyTemplate: map[string]string{"name": "{name:text}"},
		OpValuePolicies: map[string]ValuePolicy{
			"name": {Type: "text", Mode: "set", Values: []string{"alpha"}},
		},
		Outcome: Allow,
	})

	in := func(body string) Input {
		return Input{AgentID: "a1", UpstreamID: "u1", Method: "POST", Path: "/widgets",
			Query: url.Values{}, Body: []byte(body)}
	}

	// body var in the allowed set → allow, extracted
	d, err := reg.Decide(in(`{"name":"alpha"}`))
	require.NoError(t, err)
	require.Equal(t, Allow, d.Outcome)
	require.Equal(t, "alpha", d.Vars["name"])

	// a new body value → require-approval with the NewValues pair
	d, err = reg.Decide(in(`{"name":"beta"}`))
	require.NoError(t, err)
	require.Equal(t, RequireApproval, d.Outcome)
	require.Equal(t, []VarValue{{Var: "name", Value: "beta"}}, d.NewValues)

	// a body missing the declared field → structural non-match → default-deny
	d, err = reg.Decide(in(`{"other":"x"}`))
	require.NoError(t, err)
	require.Equal(t, Deny, d.Outcome)
	require.Nil(t, d.Rule)
}

func TestDecideHTTPTierPrecedence(t *testing.T) {
	reg := newReg(t)
	tmpl := func() (string, string, map[string]string, map[string]ValuePolicy) {
		return "GET", "/x/{id:text}", nil, map[string]ValuePolicy{"id": {Type: "text", Mode: "any"}}
	}
	m, p, q, vp := tmpl()
	// any-agent allow + agent-specific deny on the same matched template → deny wins.
	mk(t, reg, Rule{UpstreamID: "u1", OpMethod: m, OpPathTemplate: p, OpQueryTemplate: q, OpValuePolicies: vp, Outcome: Allow})
	mk(t, reg, Rule{SubjectAgentID: "a1", UpstreamID: "u1", OpMethod: m, OpPathTemplate: p, OpQueryTemplate: q, OpValuePolicies: vp, Outcome: Deny})

	d, err := reg.Decide(Input{AgentID: "a1", UpstreamID: "u1", Method: "GET", Path: "/x/1", Query: url.Values{}})
	require.NoError(t, err)
	require.Equal(t, Deny, d.Outcome)

	// a different agent rides the any-agent allow.
	d, _ = reg.Decide(Input{AgentID: "a2", UpstreamID: "u1", Method: "GET", Path: "/x/1", Query: url.Values{}})
	require.Equal(t, Allow, d.Outcome)
}

func TestDecideSkipsExpiredRule(t *testing.T) {
	reg := newReg(t)
	past := time.Now().UTC().Add(-time.Hour)
	mk(t, reg, Rule{UpstreamID: "u1", Outcome: Allow, BrowseMethods: "GET", BrowsePath: "/**", ExpiresAt: past})

	// Expired allow → default-deny.
	d, err := reg.Decide(Input{UpstreamID: "u1", Method: "GET", Path: "/x"})
	require.NoError(t, err)
	require.Equal(t, Deny, d.Outcome)

	// A live (future) rule still grants.
	mk(t, reg, Rule{UpstreamID: "u2", Outcome: Allow, BrowseMethods: "GET", BrowsePath: "/**", ExpiresAt: time.Now().UTC().Add(time.Hour)})
	d, err = reg.Decide(Input{UpstreamID: "u2", Method: "GET", Path: "/x"})
	require.NoError(t, err)
	require.Equal(t, Allow, d.Outcome)
}

func mk(t *testing.T, reg *Registry, r Rule) {
	t.Helper()
	_, err := reg.Create(r)
	require.NoError(t, err)
}

func TestDecidePrecedence(t *testing.T) {
	reg := newReg(t)
	in := func(path string) Input {
		return Input{AgentID: "a1", UpstreamID: "u1", Method: "GET", Path: path}
	}

	// default-deny when no rules
	d, err := reg.Decide(in("/x"))
	require.NoError(t, err)
	require.Equal(t, Deny, d.Outcome)
	require.Nil(t, d.Rule)

	// allow operation for any agent on GET /x/{id:text} with id trusted (any)
	mk(t, reg, Rule{UpstreamID: "u1", OpMethod: "GET", OpPathTemplate: "/x/{id:text}",
		OpValuePolicies: map[string]ValuePolicy{"id": {Type: "text", Mode: "any"}}, Outcome: Allow})
	d, _ = reg.Decide(in("/x/1"))
	require.Equal(t, Allow, d.Outcome)

	// agent-specific deny on the same template outranks the any-agent allow
	mk(t, reg, Rule{SubjectAgentID: "a1", UpstreamID: "u1", OpMethod: "GET", OpPathTemplate: "/x/{id:text}",
		OpValuePolicies: map[string]ValuePolicy{"id": {Type: "text", Mode: "any"}}, Outcome: Deny})
	d, _ = reg.Decide(in("/x/1"))
	require.Equal(t, Deny, d.Outcome)

	// a different agent still rides the any-agent allow
	d, _ = reg.Decide(Input{AgentID: "a2", UpstreamID: "u1", Method: "GET", Path: "/x/1"})
	require.Equal(t, Allow, d.Outcome)
}

func TestDecideK8s(t *testing.T) {
	in := func(ns, res, sub, verb string) Input {
		return Input{AgentID: "a1", UpstreamID: "c1", Kind: "k8s",
			Namespace: ns, Resource: res, Subresource: sub, Verb: verb}
	}

	// (a) (ns=prod, pods, list) allow matches exactly; not other verbs/ns/empty-ns.
	reg := newReg(t)
	mk(t, reg, Rule{UpstreamID: "c1", Namespace: "prod", Resource: "pods", Verb: "list", Outcome: Allow})
	d, err := reg.Decide(in("prod", "pods", "", "list"))
	require.NoError(t, err)
	require.Equal(t, Allow, d.Outcome)
	d, _ = reg.Decide(in("prod", "pods", "", "patch"))
	require.Equal(t, Deny, d.Outcome)
	d, _ = reg.Decide(in("staging", "pods", "", "list"))
	require.Equal(t, Deny, d.Outcome)
	d, _ = reg.Decide(in("", "pods", "", "list"))
	require.Equal(t, Deny, d.Outcome, "empty namespace must not match a concrete-namespace rule")

	// (b) (ns=prod, pods/log, get) matches a pods log subresource get.
	reg2 := newReg(t)
	mk(t, reg2, Rule{UpstreamID: "c1", Namespace: "prod", Resource: "pods/log", Verb: "get", Outcome: Allow})
	d, _ = reg2.Decide(in("prod", "pods", "log", "get"))
	require.Equal(t, Allow, d.Outcome)
	d, _ = reg2.Decide(in("prod", "pods", "", "get"))
	require.Equal(t, Deny, d.Outcome, "pods/log rule must not grant plain pods get")

	// (c) (ns=*, resource=*, verb=get) matches a cluster-scoped nodes get (ns="").
	reg3 := newReg(t)
	mk(t, reg3, Rule{UpstreamID: "c1", Namespace: "*", Resource: "*", Verb: "get", Outcome: Allow})
	d, _ = reg3.Decide(in("", "nodes", "", "get"))
	require.Equal(t, Allow, d.Outcome)

	// (d) precedence: agent-specific deny outranks any-subject allow on the same tuple.
	reg4 := newReg(t)
	mk(t, reg4, Rule{UpstreamID: "c1", Namespace: "prod", Resource: "pods", Verb: "list", Outcome: Allow})
	mk(t, reg4, Rule{SubjectAgentID: "a1", UpstreamID: "c1", Namespace: "prod", Resource: "pods", Verb: "list", Outcome: Deny})
	d, _ = reg4.Decide(in("prod", "pods", "", "list"))
	require.Equal(t, Deny, d.Outcome)

	// (e) default-deny when nothing matches.
	reg5 := newReg(t)
	mk(t, reg5, Rule{UpstreamID: "c1", Namespace: "prod", Resource: "pods", Verb: "list", Outcome: Allow})
	d, _ = reg5.Decide(in("prod", "deployments", "", "list"))
	require.Equal(t, Deny, d.Outcome)
}
