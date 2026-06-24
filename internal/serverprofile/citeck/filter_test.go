package citeck

import (
	"encoding/json"
	"testing"

	"github.com/Sipaha/outwall/internal/serverprofile"
	"github.com/stretchr/testify/require"
)

func readRule(id, source, ws string) serverprofile.Rule {
	p, _ := json.Marshal(ruleParams{Op: "read", SourceID: source, Workspace: ws})
	return serverprofile.Rule{ID: id, Outcome: serverprofile.Allow, Params: p}
}

// queryBody builds a /records/query body with the given sourceId and workspaces (nil = absent).
func queryBody(sourceID string, workspaces []string) []byte {
	q := map[string]any{"sourceId": sourceID}
	if workspaces != nil {
		q["workspaces"] = workspaces
	}
	b, _ := json.Marshal(map[string]any{"query": q})
	return b
}

func authorizeQuery(t *testing.T, sourceID string, workspaces []string, browser bool, agent ...serverprofile.Rule) serverprofile.AuthResult {
	t.Helper()
	body := queryBody(sourceID, workspaces)
	op, handled, err := classify(serverprofile.Request{Method: "POST", Path: "/api/records/query", Body: body})
	require.NoError(t, err)
	require.True(t, handled)
	ar, err := New().Authorize(serverprofile.AuthInput{Op: op, Body: body, Browser: browser, Agent: agent})
	require.NoError(t, err)
	return ar
}

func TestFilter_AllAllowed_ForwardUnchanged(t *testing.T) {
	ar := authorizeQuery(t, "emodel/person", []string{"a", "b"}, true,
		readRule("r1", "*", "a"), readRule("r2", "*", "b"))
	require.Equal(t, serverprofile.Allow, ar.Outcome)
	require.Nil(t, ar.RewriteBody)
	require.Nil(t, ar.Response)
}

func TestFilter_PartialAllowed_Browser_Rewrites(t *testing.T) {
	ar := authorizeQuery(t, "emodel/person", []string{"a", "b", "c"}, true, readRule("r1", "*", "b"))
	require.Equal(t, serverprofile.Allow, ar.Outcome)
	require.NotNil(t, ar.RewriteBody)
	require.Equal(t, []string{"b"}, workspacesOf(t, ar.RewriteBody))
}

func TestFilter_PartialAllowed_API_Denies(t *testing.T) {
	ar := authorizeQuery(t, "emodel/person", []string{"a", "b"}, false, readRule("r1", "*", "b"))
	require.Equal(t, serverprofile.Deny, ar.Outcome)
	require.Nil(t, ar.RewriteBody)
}

func TestFilter_NoneAllowed_Browser_EmptyResult(t *testing.T) {
	ar := authorizeQuery(t, "emodel/person", []string{"a", "b"}, true, readRule("r1", "*", "z"))
	require.Equal(t, serverprofile.Allow, ar.Outcome)
	require.NotNil(t, ar.Response)
	require.JSONEq(t, `{"records":[],"hasMore":false,"totalCount":0,"messages":[],"version":1}`, string(ar.Response))
}

func TestFilter_NoneAllowed_API_Denies(t *testing.T) {
	ar := authorizeQuery(t, "emodel/person", []string{"a", "b"}, false, readRule("r1", "*", "z"))
	require.Equal(t, serverprofile.Deny, ar.Outcome)
}

func TestFilter_AbsentWorkspaces_ConcreteGrants_Injects(t *testing.T) {
	ar := authorizeQuery(t, "emodel/person", nil, false,
		readRule("r1", "*", "a"), readRule("r2", "*", "b"))
	require.Equal(t, serverprofile.Allow, ar.Outcome)
	require.NotNil(t, ar.RewriteBody)
	require.Equal(t, []string{"a", "b"}, workspacesOf(t, ar.RewriteBody)) // sorted
}

func TestFilter_AbsentWorkspaces_StarGrant_ForwardUnchanged(t *testing.T) {
	ar := authorizeQuery(t, "emodel/person", nil, false, readRule("r1", "*", "*"))
	require.Equal(t, serverprofile.Allow, ar.Outcome)
	require.Nil(t, ar.RewriteBody)
	require.Nil(t, ar.Response)
}

func TestFilter_AbsentWorkspaces_NoGrants_API_Denies(t *testing.T) {
	ar := authorizeQuery(t, "emodel/person", nil, false) // no rules
	require.Equal(t, serverprofile.Deny, ar.Outcome)
}

func TestFilter_AbsentWorkspaces_NoGrants_Browser_EmptyResult(t *testing.T) {
	ar := authorizeQuery(t, "emodel/person", nil, true)
	require.Equal(t, serverprofile.Allow, ar.Outcome)
	require.NotNil(t, ar.Response)
}

func TestFilter_GlobGrant_MatchesExplicit_NotInjected(t *testing.T) {
	// "user$*" authorizes an explicit "user$pavel" but is not enumerable for injection.
	ar := authorizeQuery(t, "emodel/person", []string{"user$pavel"}, true, readRule("r1", "*", "user$*"))
	require.Equal(t, serverprofile.Allow, ar.Outcome)
	require.Nil(t, ar.RewriteBody) // all requested allowed → forward unchanged

	inj := authorizeQuery(t, "emodel/person", nil, true, readRule("r1", "*", "user$*"))
	require.Equal(t, serverprofile.Allow, inj.Outcome)
	require.NotNil(t, inj.Response) // nothing concrete to inject, browser → empty
}

func TestFilter_RewritePreservesSiblingFields(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"query":      map[string]any{"sourceId": "emodel/person", "language": "predicate", "workspaces": []string{"a", "b"}},
		"attributes": map[string]any{"disp": "?disp"},
	})
	op, _, _ := classify(serverprofile.Request{Method: "POST", Path: "/api/records/query", Body: body})
	ar, err := New().Authorize(serverprofile.AuthInput{Op: op, Body: body, Browser: true, Agent: []serverprofile.Rule{readRule("r1", "*", "a")}})
	require.NoError(t, err)
	require.NotNil(t, ar.RewriteBody)
	var got map[string]any
	require.NoError(t, json.Unmarshal(ar.RewriteBody, &got))
	require.Equal(t, map[string]any{"disp": "?disp"}, got["attributes"])
	q := got["query"].(map[string]any)
	require.Equal(t, "predicate", q["language"])
	require.Equal(t, []any{"a"}, q["workspaces"])
}

func TestFilter_WriteUnchanged(t *testing.T) {
	// A mutate op is never filtered: legacy deny stays deny even in browser mode.
	body, _ := json.Marshal(map[string]any{"records": []map[string]any{{"id": "emodel/person@new", "attributes": map[string]any{"_workspace": "a"}}}})
	op, handled, _ := classify(serverprofile.Request{Method: "POST", Path: "/api/records/mutate", Body: body})
	require.True(t, handled)
	ar, err := New().Authorize(serverprofile.AuthInput{Op: op, Body: body, Browser: true}) // no rules
	require.NoError(t, err)
	require.Equal(t, serverprofile.Deny, ar.Outcome)
	require.Nil(t, ar.RewriteBody)
	require.Nil(t, ar.Response)
}

func TestFilter_AgentDenyOverridesAnyAllow(t *testing.T) {
	denyRule := func(id, source, ws string) serverprofile.Rule {
		p, _ := json.Marshal(ruleParams{Op: "read", SourceID: source, Workspace: ws})
		return serverprofile.Rule{ID: id, Outcome: serverprofile.Deny, Params: p}
	}
	body := queryBody("emodel/person", []string{"a"})
	op, _, _ := classify(serverprofile.Request{Method: "POST", Path: "/api/records/query", Body: body})
	ar, err := New().Authorize(serverprofile.AuthInput{
		Op: op, Body: body, Browser: false,
		Agent: []serverprofile.Rule{denyRule("d1", "*", "a")},
		Any:   []serverprofile.Rule{readRule("a1", "*", "a")},
	})
	require.NoError(t, err)
	require.Equal(t, serverprofile.Deny, ar.Outcome) // agent-tier deny wins → API deny
}

// workspacesOf extracts query.workspaces from a rewritten body for assertions.
func workspacesOf(t *testing.T, body []byte) []string {
	t.Helper()
	var m struct {
		Query struct {
			Workspaces []string `json:"workspaces"`
		} `json:"query"`
	}
	require.NoError(t, json.Unmarshal(body, &m))
	return m.Query.Workspaces
}
