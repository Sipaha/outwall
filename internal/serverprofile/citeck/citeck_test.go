package citeck

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/Sipaha/outwall/internal/serverprofile"
	"github.com/stretchr/testify/require"
)

func rule(params string) serverprofile.Rule {
	return serverprofile.Rule{ID: "r", Outcome: serverprofile.Allow, Params: json.RawMessage(params)}
}

func mustClassify(t *testing.T, path, body string) serverprofile.Operation {
	op, ok, err := classify(serverprofile.Request{Method: "POST", Path: path, Query: url.Values{}, Body: []byte(body)})
	require.NoError(t, err)
	require.True(t, ok)
	return op
}

func TestMatchReadAllowedByWorkspaceRule(t *testing.T) {
	op := mustClassify(t, "/api/records/query", `{"query":{"sourceId":"emodel/type","workspaces":["w1"],"query":{}}}`)
	r := rule(`{"op":"read","source_id":"emodel/*","workspace":"w1"}`)
	require.True(t, ruleMatches(r, op), "rule should match")
	ar, err := New().Authorize(serverprofile.AuthInput{Op: op, Any: []serverprofile.Rule{r}})
	require.NoError(t, err)
	require.Equal(t, serverprofile.Allow, ar.Outcome)
}

func TestMatchReadAllWorkspacesRejectedByConcreteRule(t *testing.T) {
	op := mustClassify(t, "/api/records/query", `{"query":{"sourceId":"emodel/type","query":{}}}`) // scopeAll
	r := rule(`{"op":"read","source_id":"emodel/type","workspace":"w1"}`)
	require.False(t, ruleMatches(r, op), "a concrete-workspace rule must not match an all-workspaces read")
}

func TestMatchReadAllWorkspacesAllowedByWildcard(t *testing.T) {
	op := mustClassify(t, "/api/records/query", `{"query":{"sourceId":"emodel/type","query":{}}}`)
	r := rule(`{"op":"read","source_id":"emodel/type","workspace":"*"}`)
	require.True(t, ruleMatches(r, op))
}

func TestMatchWriteOpMismatch(t *testing.T) {
	op := mustClassify(t, "/api/records/query", `{"query":{"sourceId":"emodel/type","workspaces":["w1"],"query":{}}}`)
	r := rule(`{"op":"write","source_id":"*","workspace":"*"}`)
	require.False(t, ruleMatches(r, op), "a write rule must not match a read")
}

func TestMatchDeleteSourceIdOnly(t *testing.T) {
	op := mustClassify(t, "/api/records/delete", `{"records":["emodel/type@abc"]}`) // scopeUnknown
	// A wildcard-workspace write rule matches (workspace not enforceable for delete).
	r1 := rule(`{"op":"write","source_id":"emodel/type","workspace":"*"}`)
	require.True(t, ruleMatches(r1, op))
	// A concrete-workspace rule cannot be proven → does not match.
	r2 := rule(`{"op":"write","source_id":"emodel/type","workspace":"w1"}`)
	require.False(t, ruleMatches(r2, op))
}

func TestMatchGetAttsReadAllowedByConcreteWorkspace(t *testing.T) {
	// get-atts (read by exact ref) carries scopeUnknown: the workspace is not derivable from the
	// body. A concrete-workspace READ grant still authorizes it — the ref is exact and read is
	// low-risk, so only the source gate applies (see ADR-0043).
	op := mustClassify(t, "/api/records/query", `{"records":["emodel/type@abc"]}`) // scopeUnknown, kind read
	r := rule(`{"op":"read","source_id":"emodel/type","workspace":"w1"}`)
	require.True(t, ruleMatches(r, op), "a concrete-workspace read rule must authorize a get-atts by ref")
	ar, err := New().Authorize(serverprofile.AuthInput{Op: op, Any: []serverprofile.Rule{r}})
	require.NoError(t, err)
	require.Equal(t, serverprofile.Allow, ar.Outcome)
	// The source gate still applies: a different source is not authorized.
	rOther := rule(`{"op":"read","source_id":"emodel/other","workspace":"w1"}`)
	require.False(t, ruleMatches(rOther, op), "the source gate still applies to a get-atts read")
}

func TestMatchGetAttsWriteStillDeniedByConcreteWorkspace(t *testing.T) {
	// A write by ref (mutate/delete) also carries scopeUnknown, but the workspace-uncertainty
	// relaxation is read-only: a concrete-workspace WRITE grant must NOT authorize it.
	op := mustClassify(t, "/api/records/delete", `{"records":["emodel/type@abc"]}`) // scopeUnknown, kind write
	r := rule(`{"op":"write","source_id":"emodel/type","workspace":"w1"}`)
	require.False(t, ruleMatches(r, op), "a concrete-workspace write rule must not authorize a delete by ref")
}

func TestMatchBatchEveryResourceMustPass(t *testing.T) {
	op := mustClassify(t, "/api/records/delete", `{"records":["emodel/type@a","emodel/secret@b"]}`)
	r := rule(`{"op":"write","source_id":"emodel/type","workspace":"*"}`)
	require.False(t, ruleMatches(r, op), "a rule covering only one sourceId must not allow a cross-source batch")
}

func TestRegistered(t *testing.T) {
	_, ok := serverprofile.Get("citeck")
	require.True(t, ok)
}
