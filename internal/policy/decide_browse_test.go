package policy

import (
	"net/url"
	"testing"

	"github.com/Sipaha/outwall/internal/serverprofile"
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

// browseOnlyProf is a minimal fake profile whose Classify always returns handled=false, simulating
// a profile-aware upstream where the profile does not recognise this particular request (e.g. a
// Citeck UI page GET that hits the browse rule instead of the Records profile path).
type browseOnlyProf struct{}

func (browseOnlyProf) Name() string { return "browseonly" }
func (browseOnlyProf) Classify(_ serverprofile.Request) (serverprofile.Operation, bool, error) {
	return serverprofile.Operation{}, false, nil // never claims any request
}
func (browseOnlyProf) Match(_ serverprofile.Rule, _ serverprofile.Operation) (string, bool, error) {
	return "", false, nil
}
func (browseOnlyProf) RuleSchema() serverprofile.RuleSchema {
	return serverprofile.RuleSchema{Profile: "browseonly"}
}
func (browseOnlyProf) Presets() []serverprofile.Preset { return nil }

// TestDecideBrowseRuleAllowsWhenProfileDoesNotClaimRequest asserts that a browse rule (GET,HEAD /**)
// allows a GET on a profile-aware upstream when the profile's Classify returns handled=false (the
// citeck-UI-page case: the request is a browser page load, not a Records API call).
func TestDecideBrowseRuleAllowsWhenProfileDoesNotClaimRequest(t *testing.T) {
	serverprofile.Register("browseonly", browseOnlyProf{})
	reg := newReg(t)
	// A browse rule covering all GET,HEAD traffic on this upstream.
	mk(t, reg, Rule{UpstreamID: "u3", Outcome: Allow, BrowseMethods: "GET,HEAD", BrowsePath: "/**"})
	// A profile rule that would deny if the profile path were taken (it won't be, since Classify
	// returns handled=false, so the raw-http/browse path is used instead).
	mk(t, reg, Rule{UpstreamID: "u3", Outcome: Deny, Profile: "browseonly"})

	// Classify returns handled=false → browse rule fires → Allow.
	d, err := reg.Decide(Input{
		UpstreamID: "u3", Profile: "browseonly",
		Method: "GET", Path: "/share/page/repository", Query: url.Values{},
	})
	require.NoError(t, err)
	require.Equal(t, Allow, d.Outcome, "browse rule must allow when profile.Classify returns handled=false")

	// HEAD also matches the browse rule.
	d, err = reg.Decide(Input{
		UpstreamID: "u3", Profile: "browseonly",
		Method: "HEAD", Path: "/share/page/repository", Query: url.Values{},
	})
	require.NoError(t, err)
	require.Equal(t, Allow, d.Outcome, "browse rule must allow HEAD when profile.Classify returns handled=false")

	// POST is not in the browse method set → no match → default-deny (profile does not claim it either).
	d, err = reg.Decide(Input{
		UpstreamID: "u3", Profile: "browseonly",
		Method: "POST", Path: "/share/page/repository", Query: url.Values{},
	})
	require.NoError(t, err)
	require.Equal(t, Deny, d.Outcome, "POST not in browse method set must be denied")
}
