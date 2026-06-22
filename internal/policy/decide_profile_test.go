package policy

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/Sipaha/outwall/internal/serverprofile"
	"github.com/stretchr/testify/require"
)

// fakeProf is a minimal fake profile: handles paths starting with "/fake", read for GET, write
// otherwise; one resource {"r","s"}; matches when params {"src":<glob>} matches "r".
type fakeProf struct{}

func (fakeProf) Name() string { return "fake" }
func (fakeProf) Classify(r serverprofile.Request) (serverprofile.Operation, bool, error) {
	if r.Path != "/fake" {
		return serverprofile.Operation{}, false, nil
	}
	kind := "write"
	if r.Method == "GET" {
		kind = "read"
	}
	return serverprofile.Operation{Kind: kind, Resources: []serverprofile.ResourceScope{{Resource: "r", Scope: "s"}}}, true, nil
}
func (fakeProf) Match(rule serverprofile.Rule, op serverprofile.Operation) (string, bool, error) {
	var p struct {
		Src string `json:"src"`
	}
	_ = json.Unmarshal(rule.Params, &p)
	if p.Src == "r" || p.Src == "*" {
		return rule.Outcome, true, nil
	}
	return "", false, nil
}
func (fakeProf) RuleSchema() serverprofile.RuleSchema {
	return serverprofile.RuleSchema{Profile: "fake"}
}

func TestDecideProfileHandledAllow(t *testing.T) {
	serverprofile.Register("fake", fakeProf{})
	reg := newReg(t)
	_, err := reg.Create(Rule{UpstreamID: "up", Outcome: Allow, Profile: "fake", ProfileParams: json.RawMessage(`{"src":"r"}`)})
	require.NoError(t, err)

	d, err := reg.Decide(Input{AgentID: "", UpstreamID: "up", Profile: "fake", Method: "GET", Path: "/fake", Query: url.Values{}})
	require.NoError(t, err)
	require.Equal(t, Allow, d.Outcome)
}

func TestDecideProfileUnhandledFallsToRawHTTP(t *testing.T) {
	serverprofile.Register("fake", fakeProf{})
	reg := newReg(t)
	// A raw-http rule (no profile) allowing GET /other.
	_, err := reg.Create(Rule{UpstreamID: "up2", Outcome: Allow, OpMethod: "GET", OpPathTemplate: "/other"})
	require.NoError(t, err)
	// A profile rule on the same upstream must be ignored on the raw-http path.
	_, err = reg.Create(Rule{UpstreamID: "up2", Outcome: Deny, Profile: "fake", ProfileParams: json.RawMessage(`{"src":"*"}`)})
	require.NoError(t, err)

	d, err := reg.Decide(Input{UpstreamID: "up2", Profile: "fake", Method: "GET", Path: "/other", Query: url.Values{}})
	require.NoError(t, err)
	require.Equal(t, Allow, d.Outcome, "non-/fake path uses raw-http rules; the profile deny rule is skipped")
}
