package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
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

// TestAgentCallSelfHealsStaleToken proves the CLI recovers from a persisted token the daemon no
// longer recognises (e.g. after a daemon DB reset). The first registered token is rejected with the
// stale-token gate; doAgent must drop it, re-register once, and retry — succeeding on the fresh token
// — rather than surfacing "missing or invalid agent token" with no recovery.
func TestAgentCallSelfHealsStaleToken(t *testing.T) {
	// Isolate DataDir (agentid token file) under a temp HOME.
	t.Setenv("HOME", t.TempDir())

	var registerCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /register", func(w http.ResponseWriter, _ *http.Request) {
		n := registerCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "agent-1", "token": fmt.Sprintf("tok-%d", n)})
	})
	mux.HandleFunc("GET /whoami", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Only the second-minted token is accepted; the first is treated as stale.
		if r.Header.Get("Authorization") != "Bearer tok-2" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing or invalid agent token"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"agent_id": "agent-1", "name": "n", "status": "ok"})
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
	root.SetArgs([]string{"--agent-socket", sock, "whoami"})
	require.NoError(t, root.Execute())

	require.Equal(t, int32(2), registerCalls.Load(), "should re-register exactly once after the stale-token rejection")
	require.Contains(t, out.String(), "agent-1")
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
