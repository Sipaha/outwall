package citeck

import (
	"net/url"
	"testing"

	"github.com/Sipaha/outwall/internal/serverprofile"
	"github.com/stretchr/testify/require"
)

func req(path, body string) serverprofile.Request {
	return serverprofile.Request{Method: "POST", Path: path, Query: url.Values{}, Body: []byte(body)}
}

func TestRefSource(t *testing.T) {
	cases := []struct {
		ref, source, local string
	}{
		{"emodel/type@contract", "emodel/type", "contract"},
		{"type@contract", "type", "contract"},
		{"emodel/type@", "emodel/type", ""}, // create
		{"contract", "", "contract"},        // no '@' → whole thing is localId, empty source
		{"", "", ""},
	}
	for _, c := range cases {
		src, loc := refSource(c.ref)
		if src != c.source || loc != c.local {
			t.Fatalf("refSource(%q) = (%q,%q), want (%q,%q)", c.ref, src, loc, c.source, c.local)
		}
	}
}

func TestClassifyQueryWithWorkspace(t *testing.T) {
	op, ok, err := classify(req("/api/records/query",
		`{"query":{"sourceId":"emodel/type","workspaces":["w1"],"query":{}}}`))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "read", op.Kind)
	require.Equal(t, []serverprofile.ResourceScope{{Resource: "emodel/type", Scope: "w1"}}, op.Resources)
}

func TestClassifyQueryEmptyWorkspacesIsAll(t *testing.T) {
	op, ok, _ := classify(req("/api/records/query", `{"query":{"sourceId":"emodel/type","query":{}}}`))
	require.True(t, ok)
	require.Equal(t, []serverprofile.ResourceScope{{Resource: "emodel/type", Scope: scopeAll}}, op.Resources)
}

func TestClassifyQueryEcosTypeAlsoExtracted(t *testing.T) {
	op, ok, _ := classify(req("/api/records/query", `{"query":{"ecosType":"emodel/type@contract","query":{}}}`))
	require.True(t, ok)
	require.Equal(t, "read", op.Kind)
	require.Contains(t, op.Resources, serverprofile.ResourceScope{Resource: "emodel/type@contract", Scope: scopeAll})
}

func TestClassifyMutateCreateUsesWorkspaceAttr(t *testing.T) {
	op, ok, _ := classify(req("/api/records/mutate",
		`{"records":[{"id":"emodel/type@","attributes":{"_workspace":"w2","name":"x"}}]}`))
	require.True(t, ok)
	require.Equal(t, "write", op.Kind)
	require.Equal(t, []serverprofile.ResourceScope{{Resource: "emodel/type", Scope: "w2"}}, op.Resources)
}

func TestClassifyMutateUpdateWorkspaceUnknown(t *testing.T) {
	op, ok, _ := classify(req("/api/records/mutate",
		`{"records":[{"id":"emodel/type@abc","attributes":{"name":"x"}}]}`))
	require.True(t, ok)
	require.Equal(t, []serverprofile.ResourceScope{{Resource: "emodel/type", Scope: scopeUnknown}}, op.Resources)
}

func TestClassifyDelete(t *testing.T) {
	op, ok, _ := classify(req("/api/records/delete", `{"records":["emodel/type@abc","emodel/other@def"]}`))
	require.True(t, ok)
	require.Equal(t, "write", op.Kind)
	require.Equal(t, []serverprofile.ResourceScope{
		{Resource: "emodel/type", Scope: scopeUnknown},
		{Resource: "emodel/other", Scope: scopeUnknown},
	}, op.Resources)
}

func TestClassifyNonRecordsNotHandled(t *testing.T) {
	_, ok, err := classify(req("/gateway/observer/events", ""))
	require.NoError(t, err)
	require.False(t, ok)
}

func TestClassifyGatewayPrefixHandled(t *testing.T) {
	_, ok, _ := classify(req("/gateway/emodel/api/records/query", `{"query":{"sourceId":"emodel/type","query":{}}}`))
	require.True(t, ok)
}
