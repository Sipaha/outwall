package policy

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

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

func TestBrowseRuleRoundTrip(t *testing.T) {
	reg := newReg(t)
	_, err := reg.Create(Rule{UpstreamID: "u1", Outcome: Allow, BrowseMethods: "GET,HEAD", BrowsePath: "/**"})
	require.NoError(t, err)
	got, err := reg.ForUpstream("u1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "GET,HEAD", got[0].BrowseMethods)
	require.Equal(t, "/**", got[0].BrowsePath)
}

func TestDeleteBySubjectAndSubjectUpstream(t *testing.T) {
	reg := newReg(t)

	_, err := reg.Create(Rule{SubjectAgentID: "a1", UpstreamID: "u1", Outcome: Allow, BrowseMethods: "GET", BrowsePath: "/**"})
	require.NoError(t, err)
	_, err = reg.Create(Rule{SubjectAgentID: "a1", UpstreamID: "u2", Outcome: Allow, BrowseMethods: "GET", BrowsePath: "/**"})
	require.NoError(t, err)
	_, err = reg.Create(Rule{SubjectAgentID: "a2", UpstreamID: "u1", Outcome: Allow, BrowseMethods: "GET", BrowsePath: "/**"})
	require.NoError(t, err)

	// DeleteBySubjectUpstream removes only the (a1, u1) rule.
	n, err := reg.DeleteBySubjectUpstream("a1", "u1")
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	remaining, err := reg.List()
	require.NoError(t, err)
	require.Len(t, remaining, 2)

	// DeleteBySubject removes every rule for a1 (just the u2 one left).
	n, err = reg.DeleteBySubject("a1")
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	remaining, err = reg.List()
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	require.Equal(t, "a2", remaining[0].SubjectAgentID)
}

func TestListOrdersNewestFirst(t *testing.T) {
	reg := newReg(t)

	r1, err := reg.Create(Rule{UpstreamID: "u1", Outcome: Allow, BrowseMethods: "GET", BrowsePath: "/a"})
	require.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	r2, err := reg.Create(Rule{UpstreamID: "u1", Outcome: Allow, BrowseMethods: "GET", BrowsePath: "/b"})
	require.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	r3, err := reg.Create(Rule{UpstreamID: "u1", Outcome: Allow, BrowseMethods: "GET", BrowsePath: "/c"})
	require.NoError(t, err)

	got, err := reg.List()
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, []string{r3.ID, r2.ID, r1.ID}, []string{got[0].ID, got[1].ID, got[2].ID})
}

func TestRuleExpiresAtRoundTrip(t *testing.T) {
	reg := newReg(t)
	exp := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Nanosecond)
	out, err := reg.Create(Rule{UpstreamID: "u1", Outcome: Allow, BrowsePath: "/**", ExpiresAt: exp})
	require.NoError(t, err)
	require.WithinDuration(t, exp, out.ExpiresAt, time.Millisecond)

	got, err := reg.ForUpstream("u1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.WithinDuration(t, exp, got[0].ExpiresAt, time.Millisecond)

	// Zero value persists as "" and reads back zero (never expires).
	out2, err := reg.Create(Rule{UpstreamID: "u2", Outcome: Allow, BrowsePath: "/**"})
	require.NoError(t, err)
	require.True(t, out2.ExpiresAt.IsZero())
	got2, err := reg.ForUpstream("u2")
	require.NoError(t, err)
	require.True(t, got2[0].ExpiresAt.IsZero())
}

func TestCreateManyAtomic(t *testing.T) {
	reg := newReg(t)

	// Happy path: two valid rules → both persisted, IDs assigned.
	out, err := reg.CreateMany([]Rule{
		{UpstreamID: "u1", Outcome: Allow, BrowseMethods: "GET,HEAD", BrowsePath: "/**"},
		{UpstreamID: "u1", Outcome: Allow, Profile: "p", ProfileParams: []byte(`{"x":1}`)},
	})
	require.NoError(t, err)
	require.Len(t, out, 2)
	require.NotEmpty(t, out[0].ID)
	require.NotEqual(t, out[0].ID, out[1].ID)
	got, err := reg.ForUpstream("u1")
	require.NoError(t, err)
	require.Len(t, got, 2)

	// Atomic rollback: a batch whose 2nd rule is invalid (bad outcome) writes NOTHING.
	_, err = reg.CreateMany([]Rule{
		{UpstreamID: "u2", Outcome: Allow, BrowseMethods: "GET", BrowsePath: "/**"},
		{UpstreamID: "u2", Outcome: "bogus"},
	})
	require.Error(t, err)
	got2, err := reg.ForUpstream("u2")
	require.NoError(t, err)
	require.Empty(t, got2) // the first rule must NOT have been committed
}
