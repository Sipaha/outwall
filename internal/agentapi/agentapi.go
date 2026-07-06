// Package agentapi is the plain net/http HTTP/JSON adapter that exposes mcpsvc.Service over the
// agent unix socket. It mirrors the retired internal/mcp adapter but authenticates each request by
// the agent's bearer token (agent.Registry.Authenticate) instead of an MCP SDK session — there is no
// session cache. It is unprivileged by construction: it cannot express approve/grant/unlock.
package agentapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/mcpsvc"
)

// Deps are the collaborators the agent adapter needs.
type Deps struct {
	Svc    *mcpsvc.Service
	Agents *agent.Registry
	// Locked reports whether the vault is locked. When nil, the vault is treated as unlocked.
	Locked func() bool
}

type server struct {
	deps Deps
}

// NewHandler builds the agent-plane HTTP handler (an *http.ServeMux) served over the agent socket.
func NewHandler(deps Deps) http.Handler {
	s := &server{deps: deps}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /register", s.hRegister)
	mux.HandleFunc("GET /upstreams", s.hListUpstreams)
	mux.HandleFunc("GET /whoami", s.hWhoAmI)
	mux.HandleFunc("POST /access/host", s.hRequestHostAccess)
	mux.HandleFunc("POST /access/op", s.hRequestAccess)
	mux.HandleFunc("POST /access/k8s", s.hRequestK8sAccess)
	mux.HandleFunc("POST /access/preset", s.hRequestPreset)
	mux.HandleFunc("GET /access/{upstream}", s.hGetAccess)
	mux.HandleFunc("GET /kubeconfig/{cluster}", s.hKubeconfig)
	return mux
}

const lockedMsg = "vault locked — ask the operator to unlock outwall before requesting access"

var errNoToken = errors.New("missing or invalid agent token")

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// locked reports whether the vault is locked (treated as unlocked when no probe was provided).
func (s *server) locked() bool {
	return s.deps.Locked != nil && s.deps.Locked()
}

// agentID resolves the calling agent from the Authorization: Bearer <token> header. It returns the
// agent id and the presented token, or errNoToken (401) when the header is missing or the token is
// unknown. Unlike the retired MCP adapter there is no session cache — every call authenticates the
// presented token.
func (s *server) agentID(r *http.Request) (string, string, error) {
	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, "Bearer ") {
		return "", "", errNoToken
	}
	token := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	if token == "" {
		return "", "", errNoToken
	}
	a, err := s.deps.Agents.Authenticate(token)
	if err != nil {
		return "", "", errNoToken
	}
	return a.ID, token, nil
}

// whoamiOut mirrors the retired mcp.whoamiOut: the identity plus the presented bearer token (the
// agent needs it for the data plane; the registry stores only its hash).
type whoamiOut struct {
	mcpsvc.Identity
	Token string `json:"token"`
}

// hRegister is the unprivileged self-registration endpoint (default-deny agent). It mints a token
// once; the CLI persists it per project (internal/agentid).
func (s *server) hRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "agent"
	}
	a, token, err := s.deps.Agents.Register(name)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": a.ID, "token": token})
}

func (s *server) hWhoAmI(w http.ResponseWriter, r *http.Request) {
	agentID, token, err := s.agentID(r)
	if err != nil {
		httpErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	ident, err := s.deps.Svc.WhoAmI(agentID)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, whoamiOut{Identity: ident, Token: token})
}

// Implemented in Task 4.
func (s *server) hListUpstreams(w http.ResponseWriter, r *http.Request) {
	httpErr(w, http.StatusNotImplemented, "not implemented")
}
func (s *server) hRequestHostAccess(w http.ResponseWriter, r *http.Request) {
	httpErr(w, http.StatusNotImplemented, "not implemented")
}
func (s *server) hRequestAccess(w http.ResponseWriter, r *http.Request) {
	httpErr(w, http.StatusNotImplemented, "not implemented")
}
func (s *server) hRequestK8sAccess(w http.ResponseWriter, r *http.Request) {
	httpErr(w, http.StatusNotImplemented, "not implemented")
}
func (s *server) hRequestPreset(w http.ResponseWriter, r *http.Request) {
	httpErr(w, http.StatusNotImplemented, "not implemented")
}
func (s *server) hGetAccess(w http.ResponseWriter, r *http.Request) {
	httpErr(w, http.StatusNotImplemented, "not implemented")
}
func (s *server) hKubeconfig(w http.ResponseWriter, r *http.Request) {
	httpErr(w, http.StatusNotImplemented, "not implemented")
}
