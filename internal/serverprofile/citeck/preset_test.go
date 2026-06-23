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
