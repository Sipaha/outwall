// Package mcp is the thin adapter that exposes the four control-plane tools over the official
// MCP go-sdk as a streamable-HTTP handler. It is the ONLY package that imports the go-sdk; all
// the domain logic lives in the SDK-free internal/mcpsvc.
//
// Identity model: session = agent presence. Each MCP session (keyed by the SDK's stable
// per-session ID) is bound to a get-or-created agent on first tool call. The agent's bearer
// token is minted once at registration and cached in an in-memory map (the registry stores only
// the hash), so whoami can return it. A reconnect is a new session and therefore a new agent
// record — a known limitation; see ADR-0003.
package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/mcpsvc"
)

// Deps are the collaborators the MCP adapter needs.
type Deps struct {
	Svc    *mcpsvc.Service
	Agents *agent.Registry
	Logger *slog.Logger
	// Locked reports whether the vault is locked. When nil, the vault is treated as unlocked.
	Locked func() bool
}

// identity is the cached per-session agent binding.
type identity struct {
	agentID string
	token   string
}

// server holds the adapter state shared across tool calls.
type server struct {
	deps     Deps
	mu       sync.Mutex
	sessions map[string]identity // sessionID → {agentID, token}
}

// tool I/O structs. The SDK infers JSON schemas from these.

type emptyIn struct{}

type listUpstreamsOut struct {
	Upstreams []mcpsvc.UpstreamInfo `json:"upstreams"`
}

type requestAccessIn struct {
	Host    string `json:"host" jsonschema:"the upstream name or host to request access to"`
	Purpose string `json:"purpose" jsonschema:"why the agent needs this access (required)"`
}

type getAccessIn struct {
	Upstream string `json:"upstream" jsonschema:"the upstream name to query"`
}

type whoamiOut struct {
	mcpsvc.Identity
	Token string `json:"token"`
}

type getKubeconfigIn struct {
	Cluster string `json:"cluster" jsonschema:"the k8s cluster name to emit a kubeconfig for"`
}

type getKubeconfigOut struct {
	Kubeconfig string `json:"kubeconfig"`
}

// NewHandler builds the streamable-HTTP MCP handler exposing the four control-plane tools.
func NewHandler(deps Deps) (http.Handler, error) {
	if deps.Svc == nil || deps.Agents == nil {
		return nil, fmt.Errorf("mcp: Svc and Agents are required")
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	srv := &server{deps: deps, sessions: map[string]identity{}}

	sdkServer := sdkmcp.NewServer(
		&sdkmcp.Implementation{Name: "outwall", Version: "0"},
		nil,
	)

	sdkmcp.AddTool(sdkServer,
		&sdkmcp.Tool{Name: "list_upstreams", Description: "List the upstreams outwall knows about and your access status for each."},
		srv.handleListUpstreams)
	sdkmcp.AddTool(sdkServer,
		&sdkmcp.Tool{Name: "request_access", Description: "Request access to an upstream (by name or host), stating your purpose. Logs the intent for the operator."},
		srv.handleRequestAccess)
	sdkmcp.AddTool(sdkServer,
		&sdkmcp.Tool{Name: "get_access", Description: "Report your current access status and base path for an upstream."},
		srv.handleGetAccess)
	sdkmcp.AddTool(sdkServer,
		&sdkmcp.Tool{Name: "whoami", Description: "Return your agent identity, data-plane bearer token, and current accesses."},
		srv.handleWhoAmI)
	sdkmcp.AddTool(sdkServer,
		&sdkmcp.Tool{Name: "get_kubeconfig", Description: "Return a kubeconfig (YAML) for a registered k8s cluster, using your own outwall token. The cluster's real credentials are never included."},
		srv.handleGetKubeconfig)

	return sdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *sdkmcp.Server { return sdkServer },
		nil,
	), nil
}

// locked reports whether the vault is locked (treated as unlocked when no probe was provided).
func (s *server) locked() bool {
	return s.deps.Locked != nil && s.deps.Locked()
}

// agentFor binds the session to a get-or-created agent, minting and caching its token once.
func (s *server) agentFor(req *sdkmcp.CallToolRequest) (identity, error) {
	sessionID := req.Session.ID()

	s.mu.Lock()
	if id, ok := s.sessions[sessionID]; ok {
		s.mu.Unlock()
		return id, nil
	}
	s.mu.Unlock()

	name := "mcp-agent"
	if ip := req.Session.InitializeParams(); ip != nil && ip.ClientInfo != nil && ip.ClientInfo.Name != "" {
		name = ip.ClientInfo.Name
	}
	name += "-" + shortSuffix(sessionID)

	a, token, err := s.deps.Agents.Register(name)
	if err != nil {
		return identity{}, fmt.Errorf("register agent: %w", err)
	}
	id := identity{agentID: a.ID, token: token}

	s.mu.Lock()
	// Another concurrent call for the same session may have raced us; keep the first binding.
	if existing, ok := s.sessions[sessionID]; ok {
		s.mu.Unlock()
		return existing, nil
	}
	s.sessions[sessionID] = id
	s.mu.Unlock()

	s.deps.Logger.Info("mcp agent registered", "agent_id", a.ID, "name", name)
	return id, nil
}

func shortSuffix(sessionID string) string {
	if len(sessionID) > 8 {
		return sessionID[:8]
	}
	if sessionID == "" {
		return "anon"
	}
	return sessionID
}

func toolError(msg string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		IsError: true,
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: msg}},
	}
}

const lockedMsg = "vault locked — ask the operator to unlock outwall before requesting access"

func (s *server) handleListUpstreams(ctx context.Context, req *sdkmcp.CallToolRequest, _ emptyIn) (*sdkmcp.CallToolResult, listUpstreamsOut, error) {
	id, err := s.agentFor(req)
	if err != nil {
		return nil, listUpstreamsOut{}, err
	}
	if s.locked() {
		return toolError(lockedMsg), listUpstreamsOut{}, nil
	}
	ups, err := s.deps.Svc.ListUpstreams(id.agentID)
	if err != nil {
		return nil, listUpstreamsOut{}, err
	}
	return nil, listUpstreamsOut{Upstreams: ups}, nil
}

func (s *server) handleRequestAccess(ctx context.Context, req *sdkmcp.CallToolRequest, in requestAccessIn) (*sdkmcp.CallToolResult, mcpsvc.AccessResult, error) {
	id, err := s.agentFor(req)
	if err != nil {
		return nil, mcpsvc.AccessResult{}, err
	}
	if strings.TrimSpace(in.Purpose) == "" {
		return toolError("purpose is required"), mcpsvc.AccessResult{}, nil
	}
	if s.locked() {
		return toolError(lockedMsg), mcpsvc.AccessResult{}, nil
	}
	res, err := s.deps.Svc.RequestAccess(id.agentID, in.Host, in.Purpose)
	if err != nil {
		return nil, mcpsvc.AccessResult{}, err
	}
	return nil, res, nil
}

func (s *server) handleGetAccess(ctx context.Context, req *sdkmcp.CallToolRequest, in getAccessIn) (*sdkmcp.CallToolResult, mcpsvc.AccessResult, error) {
	id, err := s.agentFor(req)
	if err != nil {
		return nil, mcpsvc.AccessResult{}, err
	}
	if s.locked() {
		return toolError(lockedMsg), mcpsvc.AccessResult{}, nil
	}
	res, err := s.deps.Svc.GetAccess(id.agentID, in.Upstream)
	if err != nil {
		return nil, mcpsvc.AccessResult{}, err
	}
	return nil, res, nil
}

func (s *server) handleWhoAmI(ctx context.Context, req *sdkmcp.CallToolRequest, _ emptyIn) (*sdkmcp.CallToolResult, whoamiOut, error) {
	id, err := s.agentFor(req)
	if err != nil {
		return nil, whoamiOut{}, err
	}
	ident, err := s.deps.Svc.WhoAmI(id.agentID)
	if err != nil {
		return nil, whoamiOut{}, err
	}
	return nil, whoamiOut{Identity: ident, Token: id.token}, nil
}

func (s *server) handleGetKubeconfig(ctx context.Context, req *sdkmcp.CallToolRequest, in getKubeconfigIn) (*sdkmcp.CallToolResult, getKubeconfigOut, error) {
	id, err := s.agentFor(req)
	if err != nil {
		return nil, getKubeconfigOut{}, err
	}
	if s.locked() {
		return toolError(lockedMsg), getKubeconfigOut{}, nil
	}
	if strings.TrimSpace(in.Cluster) == "" {
		return toolError("cluster is required"), getKubeconfigOut{}, nil
	}
	yamlBytes, err := s.deps.Svc.Kubeconfig(in.Cluster, id.token)
	if err != nil {
		return toolError(err.Error()), getKubeconfigOut{}, nil
	}
	return nil, getKubeconfigOut{Kubeconfig: string(yamlBytes)}, nil
}
