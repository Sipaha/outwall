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
	p := New()
	op := mustClassify(t, "/api/records/query", `{"query":{"sourceId":"emodel/type","workspaces":["w1"],"query":{}}}`)
	out, matched, err := p.Match(rule(`{"op":"read","source_id":"emodel/*","workspace":"w1"}`), op)
	require.NoError(t, err)
	require.True(t, matched)
	require.Equal(t, serverprofile.Allow, out)
}

func TestMatchReadAllWorkspacesRejectedByConcreteRule(t *testing.T) {
	p := New()
	op := mustClassify(t, "/api/records/query", `{"query":{"sourceId":"emodel/type","query":{}}}`) // scopeAll
	_, matched, _ := p.Match(rule(`{"op":"read","source_id":"emodel/type","workspace":"w1"}`), op)
	require.False(t, matched, "a concrete-workspace rule must not match an all-workspaces read")
}

func TestMatchReadAllWorkspacesAllowedByWildcard(t *testing.T) {
	p := New()
	op := mustClassify(t, "/api/records/query", `{"query":{"sourceId":"emodel/type","query":{}}}`)
	_, matched, _ := p.Match(rule(`{"op":"read","source_id":"emodel/type","workspace":"*"}`), op)
	require.True(t, matched)
}

func TestMatchWriteOpMismatch(t *testing.T) {
	p := New()
	op := mustClassify(t, "/api/records/query", `{"query":{"sourceId":"emodel/type","workspaces":["w1"],"query":{}}}`)
	_, matched, _ := p.Match(rule(`{"op":"write","source_id":"*","workspace":"*"}`), op)
	require.False(t, matched, "a write rule must not match a read")
}

func TestMatchDeleteSourceIdOnly(t *testing.T) {
	p := New()
	op := mustClassify(t, "/api/records/delete", `{"records":["emodel/type@abc"]}`) // scopeUnknown
	// A wildcard-workspace write rule matches (workspace not enforceable for delete).
	_, matched, _ := p.Match(rule(`{"op":"write","source_id":"emodel/type","workspace":"*"}`), op)
	require.True(t, matched)
	// A concrete-workspace rule cannot be proven → does not match.
	_, matched2, _ := p.Match(rule(`{"op":"write","source_id":"emodel/type","workspace":"w1"}`), op)
	require.False(t, matched2)
}

func TestMatchBatchEveryResourceMustPass(t *testing.T) {
	p := New()
	op := mustClassify(t, "/api/records/delete", `{"records":["emodel/type@a","emodel/secret@b"]}`)
	_, matched, _ := p.Match(rule(`{"op":"write","source_id":"emodel/type","workspace":"*"}`), op)
	require.False(t, matched, "a rule covering only one sourceId must not allow a cross-source batch")
}

func TestRegistered(t *testing.T) {
	_, ok := serverprofile.Get("citeck")
	require.True(t, ok)
}
