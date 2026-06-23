package serverprofile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateBindings(t *testing.T) {
	slots := []PresetSlot{
		{Key: "sourceId", Type: "text", AllowAny: true, Required: true},
		{Key: "workspace", Type: "text", AllowAny: false, Required: true},
		{Key: "mode", Type: "enum", Options: []string{"a", "b"}, Required: false},
	}
	require.NoError(t, ValidateBindings(slots, Bindings{"sourceId": "*", "workspace": "proj-x"}))
	require.Error(t, ValidateBindings(slots, Bindings{"workspace": "proj-x"}))                           // sourceId required
	require.Error(t, ValidateBindings(slots, Bindings{"sourceId": "*", "workspace": "*"}))               // "*" not allowed
	require.Error(t, ValidateBindings(slots, Bindings{"sourceId": "x", "workspace": "p", "mode": "z"}))  // enum
	require.Error(t, ValidateBindings(slots, Bindings{"sourceId": "*", "workspace": "p", "bogus": "1"})) // unknown slot
}

func TestCoreHTTPBrowsePreset(t *testing.T) {
	ps := CoreHTTPPresets()
	require.Len(t, ps, 1)
	require.Equal(t, "browse-get", ps[0].ID)
	tmpls, err := ps[0].Build(Bindings{})
	require.NoError(t, err)
	require.Len(t, tmpls, 1)
	require.Equal(t, "GET,HEAD", tmpls[0].BrowseMethods)
	require.Equal(t, "/**", tmpls[0].BrowsePath)
	require.Equal(t, Allow, tmpls[0].Outcome)
}

func TestAvailablePresetsComposition(t *testing.T) {
	require.Len(t, AvailablePresets(true, "raw-http"), 1) // core only
	require.Empty(t, AvailablePresets(false, "raw-http")) // k8s-like: no core, no profile
	_, ok := FindPreset(true, "raw-http", "browse-get")
	require.True(t, ok)
	_, ok = FindPreset(true, "raw-http", "nope")
	require.False(t, ok)
}
