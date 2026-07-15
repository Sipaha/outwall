package cli

import (
	"bytes"
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/access"
	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/agentapi"
	"github.com/Sipaha/outwall/internal/approval"
	"github.com/Sipaha/outwall/internal/mcpsvc"
	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
	"github.com/Sipaha/outwall/internal/upstream"
)

// TestInstructionsCommandRendersLiveFacts drives `outwall instructions` against a real agent handler
// wired with EnvInfo and asserts the rendered playbook carries the live data-plane facts and the
// caller's identity (so a stale hand-written CLAUDE.md can be replaced by this generated output).
func TestInstructionsCommandRendersLiveFacts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	s, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	v := secret.NewVault(s)
	require.NoError(t, v.Init("pw"))
	ag := agent.NewRegistry(s)
	up := upstream.NewRegistry(s, v)
	pol := policy.NewRegistry(s)
	acc := access.NewRegistry(s)
	svc := mcpsvc.New(ag, up, pol, acc)
	svc.SetApprovals(approval.NewQueue())

	info := agentapi.EnvInfo{
		DataPlaneURL: "https://127.0.0.1:8099",
		BrowseDomain: "outwall.localhost",
		BrowsePort:   "8099",
		CookieName:   "outwall_token",
		CACertPath:   "/home/x/.spk/outwall/ca.crt",
	}
	sock := filepath.Join(t.TempDir(), "agent.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	srv := &http.Server{Handler: agentapi.NewHandler(agentapi.Deps{Svc: svc, Agents: ag, Locked: v.Locked, Info: info})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	// Rendered playbook.
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--agent-socket", sock, "instructions"})
	require.NoError(t, root.Execute())
	got := out.String()
	require.Contains(t, got, "https://127.0.0.1:8099/<upstream>/")
	require.Contains(t, got, "/home/x/.spk/outwall/ca.crt")
	require.Contains(t, got, "NO_PROXY=127.0.0.1,localhost")
	require.Contains(t, got, "outwall_token")                     // browser cookie section
	require.Contains(t, got, "<upstream>.outwall.localhost:8099") // per-upstream browser origin
	require.Contains(t, got, "Bearer ")                           // runnable example carries the token

	// JSON facts.
	root2 := NewRootCmd()
	var out2 bytes.Buffer
	root2.SetOut(&out2)
	root2.SetErr(&out2)
	root2.SetArgs([]string{"--agent-socket", sock, "instructions", "--json"})
	require.NoError(t, root2.Execute())
	require.Contains(t, out2.String(), `"data_plane_url": "https://127.0.0.1:8099"`)
}

// TestRenderInstructionsOmitsBrowseWhenDisabled proves the browser section is dropped when the
// daemon has no browse domain configured.
func TestRenderInstructionsOmitsBrowseWhenDisabled(t *testing.T) {
	o := instructionsOut{
		Info: agentapi.EnvInfo{
			DataPlaneURL: "https://127.0.0.1:8080",
			BrowseDomain: "", // browsing disabled
			CACertPath:   "/ca.crt",
		},
		Identity: mcpsvc.Identity{AgentID: "abc", Name: "proj", Status: "new"},
	}
	got := renderInstructions(o, "owa_tok", "/run/agent.sock")
	require.NotContains(t, got, "Browser / Playwright")
	require.NotContains(t, got, "cookie")
	require.Contains(t, got, "agent **proj** (`abc`)")
	require.Contains(t, got, "https://127.0.0.1:8080/<upstream>/")
}
