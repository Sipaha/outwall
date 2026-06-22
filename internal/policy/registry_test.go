package policy

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/store"
)

func newReg(t *testing.T) *Registry {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "r.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return NewRegistry(s)
}

func TestRuleCRUD(t *testing.T) {
	reg := newReg(t)
	r, err := reg.Create(Rule{
		UpstreamID:      "u1",
		OpMethod:        "GET",
		OpPathTemplate:  "/repos/{repo:text}",
		Outcome:         Allow,
		RateLimitPerMin: 60,
	})
	require.NoError(t, err)
	require.NotEmpty(t, r.ID)

	_, err = reg.Create(Rule{UpstreamID: "u1", Outcome: "bogus"})
	require.Error(t, err) // invalid outcome rejected

	got, err := reg.ForUpstream("u1")
	require.NoError(t, err)
	require.Len(t, got, 1)

	require.NoError(t, reg.Delete(r.ID))
	got, err = reg.ForUpstream("u1")
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestOperationRuleRoundTrip(t *testing.T) {
	reg := newReg(t)
	created, err := reg.Create(Rule{
		UpstreamID:      "u1",
		OpMethod:        "GET",
		OpPathTemplate:  "/api/v4/projects/{project_path:text}/pipelines",
		OpQueryTemplate: map[string]string{"updated_after": "{since:date}"},
		OpValuePolicies: map[string]ValuePolicy{
			"project_path": {Type: "text", Mode: "set", Values: []string{"a"}},
			"since":        {Type: "date", Mode: "any"},
		},
		Outcome: Allow,
	})
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)

	rules, err := reg.ForUpstream("u1")
	require.NoError(t, err)
	require.Len(t, rules, 1)
	got := rules[0]
	require.Equal(t, "GET", got.OpMethod)
	require.Equal(t, "/api/v4/projects/{project_path:text}/pipelines", got.OpPathTemplate)
	require.Equal(t, map[string]string{"updated_after": "{since:date}"}, got.OpQueryTemplate)
	require.Equal(t, ValuePolicy{Type: "text", Mode: "set", Values: []string{"a"}}, got.OpValuePolicies["project_path"])
	require.Equal(t, "any", got.OpValuePolicies["since"].Mode)

	// AddAllowedValue extends the text set; reload reflects [a, b].
	require.NoError(t, reg.AddAllowedValue(created.ID, "project_path", "b"))
	rules, err = reg.ForUpstream("u1")
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, rules[0].OpValuePolicies["project_path"].Values)

	// Adding a value already present is a no-op.
	require.NoError(t, reg.AddAllowedValue(created.ID, "project_path", "a"))
	rules, err = reg.ForUpstream("u1")
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, rules[0].OpValuePolicies["project_path"].Values)
}

func TestSetVariablePolicy(t *testing.T) {
	reg := newReg(t)
	created, err := reg.Create(Rule{
		UpstreamID:     "u1",
		OpMethod:       "GET",
		OpPathTemplate: "/api/v4/projects/{project_path:text}/pipelines",
		OpValuePolicies: map[string]ValuePolicy{
			"project_path": {Type: "text", Mode: "set", Values: []string{"a", "b"}},
		},
		Outcome: Allow,
	})
	require.NoError(t, err)

	// Replace the set (the Operations "remove a value" path: client posts the trimmed set).
	require.NoError(t, reg.SetVariablePolicy(created.ID, "project_path",
		ValuePolicy{Mode: "set", Values: []string{"a", "a", "", "c"}}))
	rules, err := reg.ForUpstream("u1")
	require.NoError(t, err)
	// Deduped + empties dropped; declared Type preserved even though the post omitted it.
	require.Equal(t, ValuePolicy{Type: "text", Mode: "set", Values: []string{"a", "c"}},
		rules[0].OpValuePolicies["project_path"])

	// Toggle to "any" drops the set.
	require.NoError(t, reg.SetVariablePolicy(created.ID, "project_path", ValuePolicy{Mode: "any"}))
	rules, err = reg.ForUpstream("u1")
	require.NoError(t, err)
	require.Equal(t, "any", rules[0].OpValuePolicies["project_path"].Mode)
	require.Nil(t, rules[0].OpValuePolicies["project_path"].Values)

	// An unknown variable is rejected — no silent widening.
	require.Error(t, reg.SetVariablePolicy(created.ID, "nope", ValuePolicy{Mode: "any"}))
}

func TestProfileRuleRoundTrip(t *testing.T) {
	reg := newReg(t)
	created, err := reg.Create(Rule{
		UpstreamID:    "up1",
		Outcome:       Allow,
		Profile:       "citeck",
		ProfileParams: json.RawMessage(`{"op":"read","source_id":"emodel/type","workspace":"*"}`),
	})
	require.NoError(t, err)

	got, err := reg.ForUpstream("up1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "citeck", got[0].Profile)
	require.JSONEq(t, `{"op":"read","source_id":"emodel/type","workspace":"*"}`, string(got[0].ProfileParams))
	require.Equal(t, created.ID, got[0].ID)
}
