package cli

import (
	"bytes"
	"encoding/json"
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

func TestListUpstreamsCommand(t *testing.T) {
	// Isolate DataDir (agentid token file) under a temp HOME.
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
	_, err = up.Create("github", "https://api.github.com", upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)
	svc := mcpsvc.New(ag, up, pol, acc)
	svc.SetApprovals(approval.NewQueue())

	sock := filepath.Join(t.TempDir(), "agent.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	srv := &http.Server{Handler: agentapi.NewHandler(agentapi.Deps{Svc: svc, Agents: ag, Locked: v.Locked})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--agent-socket", sock, "list-upstreams"})
	require.NoError(t, root.Execute())
	require.Contains(t, out.String(), "github")
}

// TestRequestAccessCommandSendsVariables proves that --var flags on `request-access` reach the
// POST /access/op request body as the typed "variables" the policy engine needs to scope values
// (without them, an approved rule with a {name:type} path placeholder is left unconstrained).
func TestRequestAccessCommandSendsVariables(t *testing.T) {
	// Isolate DataDir (agentid token file) under a temp HOME.
	t.Setenv("HOME", t.TempDir())

	var captured struct {
		Variables []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"variables"`
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /register", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "agent-1", "token": "tok-1"})
	})
	mux.HandleFunc("POST /access/op", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	sock := filepath.Join(t.TempDir(), "agent.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{
		"--agent-socket", sock, "request-access", "example",
		"--path", "/api/v4/projects/{project_path:text}/pipelines",
		"--var", "project_path:text",
		"--purpose", "x",
	})
	require.NoError(t, root.Execute())

	require.Len(t, captured.Variables, 1)
	require.Equal(t, "project_path", captured.Variables[0].Name)
	require.Equal(t, "text", captured.Variables[0].Type)
}
