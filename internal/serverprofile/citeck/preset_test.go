package citeck

import (
	"encoding/json"
	"testing"

	"github.com/Sipaha/outwall/internal/serverprofile"
	"github.com/stretchr/testify/require"
)

func TestCiteckPresetsBuild(t *testing.T) {
	ps := New().Presets()
	byID := map[string]serverprofile.Preset{}
	for _, p := range ps {
		byID[p.ID] = p
	}
	ro, ok := byID["citeck-readonly"]
	require.True(t, ok)
	rw, ok := byID["citeck-readwrite"]
	require.True(t, ok)

	b := serverprofile.Bindings{"sourceId": "*", "workspace": "proj-x"}

	roTmpls, err := ro.Build(b)
	require.NoError(t, err)
	require.Len(t, roTmpls, 2) // browse + read
	require.Equal(t, "GET,HEAD", roTmpls[0].BrowseMethods)
	require.Equal(t, "citeck", roTmpls[1].Profile)
	var rp ruleParams
	require.NoError(t, json.Unmarshal(roTmpls[1].ProfileParams, &rp))
	require.Equal(t, "read", rp.Op)
	require.Equal(t, "*", rp.SourceID)
	require.Equal(t, "proj-x", rp.Workspace)

	rwTmpls, err := rw.Build(b)
	require.NoError(t, err)
	require.Len(t, rwTmpls, 3) // browse + read + write
	var wp ruleParams
	require.NoError(t, json.Unmarshal(rwTmpls[2].ProfileParams, &wp))
	require.Equal(t, "write", wp.Op)
}

func TestWorkspaceSlotAllowsStar(t *testing.T) {
	for _, id := range []string{"citeck-readonly", "citeck-readwrite"} {
		p, ok := findPreset(t, id)
		require.True(t, ok, "preset %s present", id)
		// "*" must now be a valid binding for both sourceId and workspace.
		err := serverprofile.ValidateBindings(p.Slots, serverprofile.Bindings{"sourceId": "*", "workspace": "*"})
		require.NoError(t, err, "preset %s should accept workspace=*", id)
	}
}

// findPreset returns the citeck preset with the given id.
func findPreset(t *testing.T, id string) (serverprofile.Preset, bool) {
	t.Helper()
	for _, p := range New().Presets() {
		if p.ID == id {
			return p, true
		}
	}
	return serverprofile.Preset{}, false
}

func TestPresetHintSteersBrowseGetToReadonly(t *testing.T) {
	p := New()
	adv, ok := p.(serverprofile.PresetAdvisor)
	require.True(t, ok, "citeck profile must implement PresetAdvisor")
	require.Contains(t, adv.PresetHint("browse-get"), "citeck-readonly")
	require.Empty(t, adv.PresetHint("citeck-readonly"))
	require.Empty(t, adv.PresetHint("citeck-readwrite"))
}
