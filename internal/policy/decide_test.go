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
