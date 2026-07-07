package serverprofile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeProfile struct{ name string }

func (f fakeProfile) Name() string                              { return f.name }
func (f fakeProfile) Classify(Request) (Operation, bool, error) { return Operation{}, false, nil }
func (f fakeProfile) Authorize(AuthInput) (AuthResult, error)   { return AuthResult{Outcome: Allow}, nil }
func (f fakeProfile) RuleSchema() RuleSchema                    { return RuleSchema{Profile: f.name} }
func (f fakeProfile) Presets() []Preset                         { return nil }

func TestRegisterAndGet(t *testing.T) {
	Register("fake-x", fakeProfile{name: "fake-x"})
	p, ok := Get("fake-x")
	require.True(t, ok)
	require.Equal(t, "fake-x", p.Name())

	_, ok = Get("nope")
	require.False(t, ok)
}

// fakeAdvisor is a profile that implements the optional PresetAdvisor capability.
type fakeAdvisor struct{ fakeProfile }

func (fakeAdvisor) PresetHint(presetID string) string {
	if presetID == "browse-get" {
		return "use the better preset"
	}
	return ""
}

func TestPresetHint(t *testing.T) {
	Register("adv-x", fakeAdvisor{fakeProfile{name: "adv-x"}})
	Register("plain-x", fakeProfile{name: "plain-x"})

	// A profile implementing PresetAdvisor returns its advice for the matching preset...
	require.Equal(t, "use the better preset", PresetHint("adv-x", "browse-get"))
	// ...and "" for a preset it has no advice on.
	require.Equal(t, "", PresetHint("adv-x", "something-else"))
	// A profile that doesn't implement PresetAdvisor → "".
	require.Equal(t, "", PresetHint("plain-x", "browse-get"))
	// An unregistered profile → "".
	require.Equal(t, "", PresetHint("nope", "browse-get"))
}
