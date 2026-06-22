package serverprofile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeProfile struct{ name string }

func (f fakeProfile) Name() string                                { return f.name }
func (f fakeProfile) Classify(Request) (Operation, bool, error)   { return Operation{}, false, nil }
func (f fakeProfile) Match(Rule, Operation) (string, bool, error) { return Allow, true, nil }
func (f fakeProfile) RuleSchema() RuleSchema                      { return RuleSchema{Profile: f.name} }

func TestRegisterAndGet(t *testing.T) {
	Register("fake-x", fakeProfile{name: "fake-x"})
	p, ok := Get("fake-x")
	require.True(t, ok)
	require.Equal(t, "fake-x", p.Name())

	_, ok = Get("nope")
	require.False(t, ok)
}
