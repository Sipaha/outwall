package policy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func mk(t *testing.T, reg *Registry, r Rule) {
	t.Helper()
	_, err := reg.Create(r)
	require.NoError(t, err)
}

func TestDecidePrecedence(t *testing.T) {
	reg := newReg(t)

	// default-deny when no rules
	d, err := reg.Decide(Input{AgentID: "a1", UpstreamID: "u1", Method: "GET", Path: "/x"})
	require.NoError(t, err)
	require.Equal(t, Deny, d.Outcome)
	require.Nil(t, d.Rule)

	// global allow for any agent
	mk(t, reg, Rule{UpstreamID: "u1", Method: "*", PathGlob: "/**", Outcome: Allow})
	d, _ = reg.Decide(Input{AgentID: "a1", UpstreamID: "u1", Method: "GET", Path: "/x"})
	require.Equal(t, Allow, d.Outcome)

	// agent-specific deny outranks the global allow
	mk(t, reg, Rule{SubjectAgentID: "a1", UpstreamID: "u1", Method: "*", PathGlob: "/**", Outcome: Deny})
	d, _ = reg.Decide(Input{AgentID: "a1", UpstreamID: "u1", Method: "GET", Path: "/x"})
	require.Equal(t, Deny, d.Outcome)

	// a different agent still rides the global allow
	d, _ = reg.Decide(Input{AgentID: "a2", UpstreamID: "u1", Method: "GET", Path: "/x"})
	require.Equal(t, Allow, d.Outcome)

	// method + path narrowing: require-approval only on DELETE /danger/**
	reg2 := newReg(t)
	mk(t, reg2, Rule{UpstreamID: "u1", Method: "GET", PathGlob: "/**", Outcome: Allow})
	mk(t, reg2, Rule{UpstreamID: "u1", Method: "DELETE", PathGlob: "/danger/**", Outcome: RequireApproval})
	d, _ = reg2.Decide(Input{AgentID: "a1", UpstreamID: "u1", Method: "DELETE", Path: "/danger/x"})
	require.Equal(t, RequireApproval, d.Outcome)
	d, _ = reg2.Decide(Input{AgentID: "a1", UpstreamID: "u1", Method: "GET", Path: "/safe"})
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
