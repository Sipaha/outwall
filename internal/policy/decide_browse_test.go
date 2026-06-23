package policy

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecideBrowseRuleAllowsGetAnyPath(t *testing.T) {
	reg := newReg(t)
	mk(t, reg, Rule{UpstreamID: "u1", Outcome: Allow, BrowseMethods: "GET,HEAD", BrowsePath: "/**"})

	// GET on any path → allow.
	d, err := reg.Decide(Input{UpstreamID: "u1", Method: "GET", Path: "/static/app.js", Query: url.Values{}})
	require.NoError(t, err)
	require.Equal(t, Allow, d.Outcome)

	// POST is not in the method set → no browse match → default-deny.
	d, err = reg.Decide(Input{UpstreamID: "u1", Method: "POST", Path: "/x", Query: url.Values{}})
	require.NoError(t, err)
	require.Equal(t, Deny, d.Outcome)
}

func TestDecideBrowseAndOperationCoexist(t *testing.T) {
	reg := newReg(t)
	mk(t, reg, Rule{UpstreamID: "u2", Outcome: Allow, BrowseMethods: "GET", BrowsePath: "/**"})
	mk(t, reg, Rule{UpstreamID: "u2", Outcome: Allow, OpMethod: "POST", OpPathTemplate: "/api/echo",
		OpValuePolicies: map[string]ValuePolicy{}})
	// browse covers a GET asset…
	d, _ := reg.Decide(Input{UpstreamID: "u2", Method: "GET", Path: "/page", Query: url.Values{}})
	require.Equal(t, Allow, d.Outcome)
	// …and the operation rule covers the POST API.
	d, _ = reg.Decide(Input{UpstreamID: "u2", Method: "POST", Path: "/api/echo", Query: url.Values{}, Body: []byte("{}")})
	require.Equal(t, Allow, d.Outcome)
}
