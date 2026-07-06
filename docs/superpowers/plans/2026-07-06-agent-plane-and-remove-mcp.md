# Agent Plane + Remove MCP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace outwall's MCP control plane with a direct **agent plane** — plain HTTP/JSON over a dedicated unix socket (`~/.spk/outwall/agent.sock`) plus a CLI face — and remove the MCP adapter and the go-sdk dependency entirely. This plan covers ONLY the spec's Agent-plane + Removing-MCP scope (requirements **R1–R4**). The operator-plane sealing (R5–R8: operator session, route split, Wails bindings) is a **separate plan** — do not touch `admin.go`, the vault, the desktop privileged verbs, or `web/` here beyond the mechanical MCP-removal edits listed below.

**Architecture:** A new package `internal/agentapi` is a thin net/http adapter over the existing SDK-free `*mcpsvc.Service` (exactly mirroring the retired `internal/mcp` adapter, but keyed on a `Authorization: Bearer <owa-token>` header authenticated via `agent.Registry.Authenticate` — no session cache). It is served over a `0600` unix socket by the daemon, alongside the existing admin socket, data plane, and UI listener. A new client-side package `internal/agentid` persists a **per-project** agent token (keyed by the git top-level realpath, else cwd realpath) under `~/.spk/outwall/agents/<sha256>.token`, minting it once under a `flock`. The CLI gains agent-facing subcommands (`list-upstreams`, `whoami`, `request-host-access`, `request-access`, `request-preset`, `request-k8s-access`, `get-access`, `get-kubeconfig`) that resolve the per-project token and call the agent socket. Every call is independent — start order and daemon restarts are irrelevant (the token hash is persisted; `Authenticate` is valid across restarts).

**Tech Stack:** Go 1.26, stdlib `net/http` (Go 1.22 method+wildcard routing), `syscall.Flock` (already used by `internal/desktop/instance_unix.go` — no new dependency), `spf13/cobra`, `log/slog`, `stretchr/testify`. Pure-Go SQLite (`modernc.org/sqlite`) via the existing `internal/store`.

## Global Constraints

- Module path is exactly `github.com/Sipaha/outwall` — verbatim, in every import.
- No `citeck` strings/imports in core (irrelevant here but keep it).
- No CGO in the server binary (`CGO_ENABLED=0`); the daemon builds with `make build`.
- No panics / `log.Fatal` in library code — return wrapped errors (`%w`). Panics only in main/tests.
- `log/slog` for logging; `stretchr/testify` for tests; `gofmt` tabs; `go vet` clean.
- Add new deps at latest; don't bump existing deps.
- Before committing each task: `make fmt && make vet && make test`.
- No `Co-Authored-By` in commits; never `git commit --amend`; new commits only.
- TDD: failing test → run it fails → minimal code → run it passes → commit. Frequent small commits.

## Design decisions pinned by this plan (later tasks depend on these names/types)

- `agentapi.Deps{ Svc *mcpsvc.Service; Agents *agent.Registry; Locked func() bool }`
- `agentapi.NewHandler(deps Deps) http.Handler` → returns an `*http.ServeMux`.
- `(*server).agentID(r *http.Request) (agentID, token string, err error)` — reads `Authorization: Bearer <token>`, calls `deps.Agents.Authenticate(token)`; 401 on missing/invalid.
- `agentid.TokenPath(cwd string) (string, error)` and `agentid.LoadOrRegister(cwd string, register func(name string) (id, token string, err error)) (token string, err error)`.
- `(*client.Client).DoAuth(token, method, path string, body, out any) error` (existing `Do` keeps working, no bearer).
- Agent-socket routes: `POST /register`, `GET /upstreams`, `GET /whoami`, `POST /access/host`, `POST /access/op`, `POST /access/k8s`, `POST /access/preset`, `GET /access/{upstream}`, `GET /kubeconfig/{cluster}`.

Verbatim `mcpsvc.Service` signatures the adapter MUST match:

```go
func (s *Service) ListUpstreams(agentID string) ([]UpstreamInfo, error)
func (s *Service) Kubeconfig(cluster, agentToken string) ([]byte, error)
func (s *Service) RequestHostAccess(agentID, host, purpose string) (AccessResult, error)
func (s *Service) RequestAccess(agentID string, in RequestAccessInput) (AccessResult, error)
func (s *Service) RequestK8sAccess(agentID, cluster, namespace string, specs []K8sAccessSpec, purpose string) (AccessResult, error)
func (s *Service) RequestPreset(agentID string, in RequestPresetInput) (AccessResult, error)
func (s *Service) GetAccess(agentID, upstreamName string) (AccessResult, error)
func (s *Service) WhoAmI(agentID string) (Identity, error)
type RequestAccessInput struct { Host, Method, PathTemplate string; QueryTemplate map[string]string; BodyTemplate map[string]string; Variables []Variable; Values map[string]string; Purpose string }
type Variable struct { Name string; Type string }
type K8sAccessSpec struct { Resource string; Verbs []string }
type RequestPresetInput struct { Host, PresetID string; Bindings map[string]string; Purpose string }
type UpstreamInfo struct { Name, BaseURL, Kind, Profile, Status string; Presets []serverprofile.Preset }
type AccessResult struct { Status, BasePath, Memo string; BrowseURL string }
type Identity struct { AgentID, Name, Status string; Accesses []string }
```

Verbatim `agent.Registry`:

```go
func (r *Registry) Register(name string) (*Agent, string, error) // token = "owa_"+base64url(32 rand); only SHA-256 hash persisted; valid across restarts
func (r *Registry) Authenticate(token string) (*Agent, error)    // returns agent.ErrUnknownToken
type Agent struct { ID, Name, Status string; CreatedAt time.Time }
```

---

## Task 1 — `internal/agentid`: `TokenPath` (project-keyed token path)

Rationale: a project is identified by the realpath of its git top-level (when cwd is inside a repo) else the realpath of cwd. A `cd` into a subdirectory of a repo keeps the same identity — one agent per project, not per directory. Token path = `<DataDir>/agents/<hex-sha256(projectKey)>.token`.

- [ ] Create the test file `internal/agentid/agentid_test.go` with the `TokenPath` tests:

```go
package agentid_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/agentid"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	require.NoError(t, cmd.Run())
}

func TestTokenPathGitTopLevel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := t.TempDir()
	runGit(t, repo, "init")
	sub := filepath.Join(repo, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	root, err := agentid.TokenPath(repo)
	require.NoError(t, err)
	child, err := agentid.TokenPath(sub)
	require.NoError(t, err)

	require.Equal(t, root, child, "a subdir of a repo maps to the same token path")
	require.True(t, strings.HasPrefix(root, filepath.Join(home, ".spk", "outwall", "agents")), root)
	require.True(t, strings.HasSuffix(root, ".token"), root)
}

func TestTokenPathNonRepoDiffers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d1 := t.TempDir()
	d2 := t.TempDir()

	p1, err := agentid.TokenPath(d1)
	require.NoError(t, err)
	p2, err := agentid.TokenPath(d2)
	require.NoError(t, err)
	require.NotEqual(t, p1, p2)
}
```

- [ ] Run it — must FAIL to compile (package does not exist yet):

```bash
go test ./internal/agentid/
# expected: build failure — no Go files / undefined: agentid.TokenPath
```

- [ ] Create `internal/agentid/agentid.go` with `projectKey`, `tokenPathForKey`, and `TokenPath`:

```go
// Package agentid resolves and persists the per-project agent token used by the outwall CLI.
//
// A project is identified by the realpath of its git top-level (when cwd is inside a repo) else the
// realpath of cwd. The token for a project lives at <DataDir>/agents/<hex-sha256(projectKey)>.token
// (0600). Using the git top-level means a `cd` into a subdirectory of a repo keeps the same
// identity — one agent per project, not per directory. The token is accountability-only: a same-user
// process can read any project's token, so this is NOT an isolation boundary (see ADR-0040).
package agentid

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Sipaha/outwall/internal/config"
)

// projectKey returns the stable identity key for cwd: the realpath of the git top-level when cwd is
// inside a repo, else the realpath (symlinks resolved) of cwd itself.
func projectKey(cwd string) (string, error) {
	cmd := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel")
	if out, err := cmd.Output(); err == nil {
		if top := strings.TrimSpace(string(out)); top != "" {
			real, rerr := filepath.EvalSymlinks(top)
			if rerr != nil {
				return "", fmt.Errorf("resolve git top-level: %w", rerr)
			}
			return real, nil
		}
	}
	real, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	return real, nil
}

// tokenPathForKey builds the token-file path for a resolved project key.
func tokenPathForKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(config.DataDir(), "agents", hex.EncodeToString(sum[:])+".token")
}

// TokenPath returns the token-file path for the project containing cwd.
func TokenPath(cwd string) (string, error) {
	key, err := projectKey(cwd)
	if err != nil {
		return "", err
	}
	return tokenPathForKey(key), nil
}
```

- [ ] Run it — must PASS:

```bash
go test ./internal/agentid/
# expected: ok  github.com/Sipaha/outwall/internal/agentid
```

- [ ] `make fmt && make vet && make test`, then commit:

```bash
git commit -am "feat(agentid): project-keyed agent token path (git top-level realpath)"
```

---

## Task 2 — `internal/agentid`: `LoadOrRegister` (flock-serialized mint-once)

`LoadOrRegister` takes an exclusive `flock` on `<tokenpath>.lock`, returns the persisted token if present, else calls `register(basename(projectKey))`, writes the token file `0600` atomically (temp file + rename), then releases the lock. Concurrent first-calls in a fresh project therefore mint exactly one agent (the winner writes, losers read). Uses `syscall.Flock` (consistent with `internal/desktop/instance_unix.go`; no new dependency).

- [ ] Append the concurrency test to `internal/agentid/agentid_test.go`:

```go
func TestLoadOrRegisterMintsOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir() // non-repo project

	var calls int32
	register := func(name string) (string, string, error) {
		n := atomic.AddInt32(&calls, 1)
		return "id-" + name, fmt.Sprintf("owa_tok_%d", n), nil
	}

	const N = 16
	var wg sync.WaitGroup
	tokens := make([]string, N)
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = agentid.LoadOrRegister(dir, register)
		}(i)
	}
	wg.Wait()

	for i := range errs {
		require.NoError(t, errs[i])
	}
	require.Equal(t, int32(1), atomic.LoadInt32(&calls), "register must be called exactly once")
	for i := 1; i < N; i++ {
		require.Equal(t, tokens[0], tokens[i])
	}

	// A later call reads the persisted token without registering again.
	tok, err := agentid.LoadOrRegister(dir, func(string) (string, string, error) {
		t.Fatal("register must not be called when a token exists")
		return "", "", nil
	})
	require.NoError(t, err)
	require.Equal(t, tokens[0], tok)
}
```

Add the new imports to the test file's import block: `"fmt"`, `"sync"`, `"sync/atomic"`.

- [ ] Run it — must FAIL (undefined: `agentid.LoadOrRegister`):

```bash
go test ./internal/agentid/
# expected: undefined: agentid.LoadOrRegister
```

- [ ] Add `LoadOrRegister` to `internal/agentid/agentid.go` (and add `"errors"`, `"os"`, `"syscall"` to its import block):

```go
// LoadOrRegister returns the per-project agent token, minting it once on first use. It serializes
// concurrent first-calls with an exclusive flock on <tokenpath>.lock so exactly one agent is
// registered: the winner calls register and writes the token atomically; losers block on the flock
// and then read the file. register receives the basename of the project key as the agent name.
func LoadOrRegister(cwd string, register func(name string) (id, token string, err error)) (string, error) {
	key, err := projectKey(cwd)
	if err != nil {
		return "", err
	}
	path := tokenPathForKey(key)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create agents dir: %w", err)
	}

	lockPath := path + ".lock"
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", fmt.Errorf("open lock: %w", err)
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return "", fmt.Errorf("flock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN) }()

	// Fast path: a token was already minted for this project (by us or an earlier flock holder).
	if b, rerr := os.ReadFile(path); rerr == nil {
		if tok := strings.TrimSpace(string(b)); tok != "" {
			return tok, nil
		}
	} else if !errors.Is(rerr, os.ErrNotExist) {
		return "", fmt.Errorf("read token: %w", rerr)
	}

	// Mint once, then write atomically (temp file in the same dir + rename).
	_, token, err := register(filepath.Base(key))
	if err != nil {
		return "", fmt.Errorf("register agent: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return "", fmt.Errorf("create temp token: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(token); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("write temp token: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("chmod temp token: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("close temp token: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("rename token: %w", err)
	}
	return token, nil
}
```

- [ ] Run it (with the race detector — the test exercises concurrency):

```bash
go test -race ./internal/agentid/
# expected: ok  github.com/Sipaha/outwall/internal/agentid
```

- [ ] `make fmt && make vet && make test`, then commit:

```bash
git commit -am "feat(agentid): LoadOrRegister — flock-serialized mint-once, atomic 0600 write"
```

---

## Task 3 — `internal/agentapi`: handler skeleton + identity + `/register` + `/whoami`

- [ ] Create `internal/agentapi/agentapi_test.go` with a shared server helper and the register/whoami/401 test:

```go
package agentapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

type testEnv struct {
	ts    *httptest.Server
	agents *agent.Registry
	ups   *upstream.Registry
	vault *secret.Vault
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
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
	h := agentapi.NewHandler(agentapi.Deps{Svc: svc, Agents: ag, Locked: v.Locked})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return &testEnv{ts: ts, agents: ag, ups: up, vault: v}
}

// doJSON issues a request to the agent socket test server and asserts the status code.
func (e *testEnv) doJSON(t *testing.T, token, method, path string, body, out any, wantCode int) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, rdr)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, wantCode, resp.StatusCode)
	if out != nil {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(out))
	}
}

func TestRegisterWhoAmIAndAuth(t *testing.T) {
	e := newEnv(t)

	var reg struct{ ID, Token string }
	e.doJSON(t, "", "POST", "/register", map[string]string{"name": "proj-x"}, &reg, http.StatusOK)
	require.NotEmpty(t, reg.ID)
	require.NotEmpty(t, reg.Token)

	var who map[string]any
	e.doJSON(t, reg.Token, "GET", "/whoami", nil, &who, http.StatusOK)
	require.Equal(t, reg.ID, who["agent_id"])
	require.Equal(t, reg.Token, who["token"])

	// Missing bearer → 401.
	e.doJSON(t, "", "GET", "/whoami", nil, nil, http.StatusUnauthorized)
	// Garbage bearer → 401.
	e.doJSON(t, "owa_nope", "GET", "/whoami", nil, nil, http.StatusUnauthorized)
}
```

- [ ] Run it — must FAIL (no `agentapi` package):

```bash
go test ./internal/agentapi/
# expected: build failure — undefined: agentapi.NewHandler / agentapi.Deps
```

- [ ] Create `internal/agentapi/agentapi.go` with the package doc, `Deps`, `server`, `NewHandler`, helpers, `agentID`, and the `/register` + `/whoami` handlers:

```go
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
```

Note: the remaining handlers (`hListUpstreams`, `hRequestHostAccess`, `hRequestAccess`, `hRequestK8sAccess`, `hRequestPreset`, `hGetAccess`, `hKubeconfig`) are referenced by `NewHandler` but implemented in Task 4. To keep this task green, add temporary stubs at the bottom of the file so the package compiles:

```go
// Implemented in Task 4.
func (s *server) hListUpstreams(w http.ResponseWriter, r *http.Request)     { httpErr(w, http.StatusNotImplemented, "not implemented") }
func (s *server) hRequestHostAccess(w http.ResponseWriter, r *http.Request) { httpErr(w, http.StatusNotImplemented, "not implemented") }
func (s *server) hRequestAccess(w http.ResponseWriter, r *http.Request)     { httpErr(w, http.StatusNotImplemented, "not implemented") }
func (s *server) hRequestK8sAccess(w http.ResponseWriter, r *http.Request)  { httpErr(w, http.StatusNotImplemented, "not implemented") }
func (s *server) hRequestPreset(w http.ResponseWriter, r *http.Request)     { httpErr(w, http.StatusNotImplemented, "not implemented") }
func (s *server) hGetAccess(w http.ResponseWriter, r *http.Request)         { httpErr(w, http.StatusNotImplemented, "not implemented") }
func (s *server) hKubeconfig(w http.ResponseWriter, r *http.Request)        { httpErr(w, http.StatusNotImplemented, "not implemented") }
```

- [ ] Run it — must PASS:

```bash
go test ./internal/agentapi/
# expected: ok  github.com/Sipaha/outwall/internal/agentapi
```

- [ ] `make fmt && make vet && make test`, then commit:

```bash
git commit -am "feat(agentapi): agent-plane handler skeleton — bearer identity, /register, /whoami"
```

---

## Task 4 — `internal/agentapi`: access / list / kubeconfig routes

Replace the Task-3 stubs with the real handlers, mirroring the retired `internal/mcp/server.go` logic (locked → 409 JSON; service tool-errors → 400 JSON; success → 200 JSON). `hWhoAmI` stays un-gated by `locked` (as the old adapter's `handleWhoAmI` was); the access/list/kubeconfig routes return `lockedMsg` (409) while the vault is locked.

- [ ] Append the access-routes test to `internal/agentapi/agentapi_test.go`:

```go
func TestAccessRoutes(t *testing.T) {
	e := newEnv(t)
	_, err := e.ups.Create("github", "https://api.github.com", upstream.AuthConfig{Type: "none"})
	require.NoError(t, err)

	var reg struct{ ID, Token string }
	e.doJSON(t, "", "POST", "/register", map[string]string{"name": "proj"}, &reg, http.StatusOK)

	// list_upstreams → contains github, status needs-request (no rule yet).
	var list struct {
		Upstreams []mcpsvc.UpstreamInfo `json:"upstreams"`
	}
	e.doJSON(t, reg.Token, "GET", "/upstreams", nil, &list, http.StatusOK)
	require.Len(t, list.Upstreams, 1)
	require.Equal(t, "github", list.Upstreams[0].Name)
	require.Equal(t, "needs-request", list.Upstreams[0].Status)

	// request-host-access requires a purpose.
	e.doJSON(t, reg.Token, "POST", "/access/host",
		map[string]string{"host": "github"}, nil, http.StatusBadRequest)

	// request-host-access with a purpose → 200, pending.
	var hostRes mcpsvc.AccessResult
	e.doJSON(t, reg.Token, "POST", "/access/host",
		map[string]string{"host": "github", "purpose": "read repos"}, &hostRes, http.StatusOK)
	require.Equal(t, "pending", hostRes.Status)

	// get-access → 200 (no pending long-poll here since request logging is enabled).
	var getRes mcpsvc.AccessResult
	e.doJSON(t, reg.Token, "GET", "/access/github", nil, &getRes, http.StatusOK)
	require.NotEmpty(t, getRes.Status)

	// kubeconfig for a non-cluster upstream → 400 (service error surfaced as JSON).
	e.doJSON(t, reg.Token, "GET", "/kubeconfig/github", nil, nil, http.StatusBadRequest)
}

func TestLockedReturns409(t *testing.T) {
	e := newEnv(t)
	var reg struct{ ID, Token string }
	e.doJSON(t, "", "POST", "/register", map[string]string{"name": "proj"}, &reg, http.StatusOK)

	e.vault.Lock() // Lock() returns no error

	// A locked vault fails the list/access routes with a clear 409 JSON message.
	var errBody struct {
		Error string `json:"error"`
	}
	e.doJSON(t, reg.Token, "GET", "/upstreams", nil, &errBody, http.StatusConflict)
	require.Contains(t, errBody.Error, "vault locked")
}
```

(If `secret.Vault` exposes a different lock method than `Lock()`, adjust the call — check `internal/secret/vault.go` for the exact name; `Locked func() bool` is what the daemon passes as the probe.)

- [ ] Run it — must FAIL (stubs return 501, not the asserted codes):

```bash
go test ./internal/agentapi/ -run TestAccessRoutes
# expected: FAIL — got 501, want 200/400
```

- [ ] Replace the seven stub functions in `internal/agentapi/agentapi.go` with the real handlers:

```go
func (s *server) hListUpstreams(w http.ResponseWriter, r *http.Request) {
	agentID, _, err := s.agentID(r)
	if err != nil {
		httpErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	if s.locked() {
		httpErr(w, http.StatusConflict, lockedMsg)
		return
	}
	ups, err := s.deps.Svc.ListUpstreams(agentID)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"upstreams": ups})
}

func (s *server) hRequestHostAccess(w http.ResponseWriter, r *http.Request) {
	agentID, _, err := s.agentID(r)
	if err != nil {
		httpErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	var body struct {
		Host    string `json:"host"`
		Purpose string `json:"purpose"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if strings.TrimSpace(body.Purpose) == "" {
		httpErr(w, http.StatusBadRequest, "purpose is required")
		return
	}
	if s.locked() {
		httpErr(w, http.StatusConflict, lockedMsg)
		return
	}
	res, err := s.deps.Svc.RequestHostAccess(agentID, body.Host, body.Purpose)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) hRequestAccess(w http.ResponseWriter, r *http.Request) {
	agentID, _, err := s.agentID(r)
	if err != nil {
		httpErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	var body struct {
		Host          string            `json:"host"`
		Method        string            `json:"method"`
		PathTemplate  string            `json:"path_template"`
		QueryTemplate map[string]string `json:"query_template"`
		BodyTemplate  map[string]string `json:"body_template"`
		Variables     []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"variables"`
		Values  map[string]string `json:"values"`
		Purpose string            `json:"purpose"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if strings.TrimSpace(body.Purpose) == "" {
		httpErr(w, http.StatusBadRequest, "purpose is required")
		return
	}
	if s.locked() {
		httpErr(w, http.StatusConflict, lockedMsg)
		return
	}
	vars := make([]mcpsvc.Variable, 0, len(body.Variables))
	for _, v := range body.Variables {
		vars = append(vars, mcpsvc.Variable{Name: v.Name, Type: v.Type})
	}
	res, err := s.deps.Svc.RequestAccess(agentID, mcpsvc.RequestAccessInput{
		Host: body.Host, Method: body.Method, PathTemplate: body.PathTemplate,
		QueryTemplate: body.QueryTemplate, BodyTemplate: body.BodyTemplate,
		Variables: vars, Values: body.Values, Purpose: body.Purpose,
	})
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) hRequestK8sAccess(w http.ResponseWriter, r *http.Request) {
	agentID, _, err := s.agentID(r)
	if err != nil {
		httpErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	var body struct {
		Cluster   string `json:"cluster"`
		Namespace string `json:"namespace"`
		Grants    []struct {
			Resource string   `json:"resource"`
			Verbs    []string `json:"verbs"`
		} `json:"grants"`
		Purpose string `json:"purpose"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if strings.TrimSpace(body.Purpose) == "" {
		httpErr(w, http.StatusBadRequest, "purpose is required")
		return
	}
	if strings.TrimSpace(body.Cluster) == "" {
		httpErr(w, http.StatusBadRequest, "cluster is required")
		return
	}
	if len(body.Grants) == 0 {
		httpErr(w, http.StatusBadRequest, "at least one grant (resource + verbs) is required")
		return
	}
	if s.locked() {
		httpErr(w, http.StatusConflict, lockedMsg)
		return
	}
	specs := make([]mcpsvc.K8sAccessSpec, 0, len(body.Grants))
	for _, g := range body.Grants {
		specs = append(specs, mcpsvc.K8sAccessSpec{Resource: g.Resource, Verbs: g.Verbs})
	}
	res, err := s.deps.Svc.RequestK8sAccess(agentID, body.Cluster, body.Namespace, specs, body.Purpose)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) hRequestPreset(w http.ResponseWriter, r *http.Request) {
	agentID, _, err := s.agentID(r)
	if err != nil {
		httpErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	var body struct {
		Upstream string            `json:"upstream"`
		Preset   string            `json:"preset"`
		Vars     map[string]string `json:"vars"`
		Purpose  string            `json:"purpose"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if strings.TrimSpace(body.Purpose) == "" {
		httpErr(w, http.StatusBadRequest, "purpose is required")
		return
	}
	if strings.TrimSpace(body.Preset) == "" {
		httpErr(w, http.StatusBadRequest, "preset is required")
		return
	}
	if s.locked() {
		httpErr(w, http.StatusConflict, lockedMsg)
		return
	}
	res, err := s.deps.Svc.RequestPreset(agentID, mcpsvc.RequestPresetInput{
		Host: body.Upstream, PresetID: body.Preset, Bindings: body.Vars, Purpose: body.Purpose,
	})
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) hGetAccess(w http.ResponseWriter, r *http.Request) {
	agentID, _, err := s.agentID(r)
	if err != nil {
		httpErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	if s.locked() {
		httpErr(w, http.StatusConflict, lockedMsg)
		return
	}
	// GetAccess long-polls (~25s) internally when a decision is pending.
	res, err := s.deps.Svc.GetAccess(agentID, r.PathValue("upstream"))
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) hKubeconfig(w http.ResponseWriter, r *http.Request) {
	_, token, err := s.agentID(r)
	if err != nil {
		httpErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	if s.locked() {
		httpErr(w, http.StatusConflict, lockedMsg)
		return
	}
	cluster := strings.TrimSpace(r.PathValue("cluster"))
	if cluster == "" {
		httpErr(w, http.StatusBadRequest, "cluster is required")
		return
	}
	yamlBytes, err := s.deps.Svc.Kubeconfig(cluster, token)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"kubeconfig": string(yamlBytes)})
}
```

- [ ] Run it — must PASS:

```bash
go test ./internal/agentapi/
# expected: ok  github.com/Sipaha/outwall/internal/agentapi
```

- [ ] `make fmt && make vet && make test`, then commit:

```bash
git commit -am "feat(agentapi): access/list/kubeconfig routes over the agent socket"
```

---

## Task 5 — `internal/client`: bearer support (`DoAuth`)

The existing `Client.Do` sets no Authorization header (operator CLI over the admin socket). Add a bearer variant so the agent CLI can present its token to the agent socket.

- [ ] Create `internal/client/client_test.go`:

```go
package client_test

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/client"
)

func TestDoAuthSetsBearer(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "t.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /whoami", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"authz": r.Header.Get("Authorization")})
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	c := client.New(sock)

	// DoAuth sets the bearer header.
	var withTok struct{ Authz string }
	require.NoError(t, c.DoAuth("owa_abc", "GET", "/whoami", nil, &withTok))
	require.Equal(t, "Bearer owa_abc", withTok.Authz)

	// Do sets no Authorization header (operator CLI behavior preserved).
	var noTok struct{ Authz string }
	require.NoError(t, c.Do("GET", "/whoami", nil, &noTok))
	require.Equal(t, "", noTok.Authz)

	_ = os.Remove(sock)
}
```

- [ ] Run it — must FAIL (undefined: `DoAuth`):

```bash
go test ./internal/client/
# expected: undefined: (*client.Client).DoAuth
```

- [ ] Refactor `internal/client/client.go`: extract the body of `Do` into a private `do(token, ...)` and add `Do` + `DoAuth` wrappers. Replace the existing `Do` method with:

```go
// Do sends a request with no Authorization header (operator CLI over the admin socket).
func (c *Client) Do(method, path string, body, out any) error {
	return c.do("", method, path, body, out)
}

// DoAuth sends a request with Authorization: Bearer <token> (agent CLI over the agent socket).
func (c *Client) DoAuth(token, method, path string, body, out any) error {
	return c.do(token, method, path, body, out)
}

func (c *Client) do(token, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://unix"+path, rdr)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call daemon (is it running?): %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return fmt.Errorf("daemon: %s", e.Error)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
```

- [ ] Run it — must PASS:

```bash
go test ./internal/client/
# expected: ok  github.com/Sipaha/outwall/internal/client
```

- [ ] `make fmt && make vet && make test`, then commit:

```bash
git commit -am "feat(client): DoAuth — bearer variant for the agent socket"
```

---

## Task 6 — CLI: `--agent-socket` flag, agent-token helper, and the eight agent subcommands

- [ ] Create `internal/cli/agentplane_test.go` — an end-to-end test of one command against a real agent socket:

```go
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
```

- [ ] Run it — must FAIL (unknown flag `--agent-socket` / unknown command `list-upstreams`):

```bash
go test ./internal/cli/ -run TestListUpstreamsCommand
# expected: FAIL — unknown flag: --agent-socket
```

- [ ] Edit `internal/cli/root.go`: swap the `mcpListen` field/flag for `agentSocket`, and register the new commands. Change the struct:

```go
type globalFlags struct {
	socket         string
	agentSocket    string
	db             string
	listen         string
	uiListen       string
	callbackListen string
	browseDomain   string
}
```

Replace the `--mcp-listen` flag registration with:

```go
	root.PersistentFlags().StringVar(&gf.agentSocket, "agent-socket", filepath.Join(dir, "agent.sock"), "agent-plane unix socket path")
```

Extend `root.AddCommand(...)` with the eight agent commands:

```go
	root.AddCommand(
		newServeCmd(gf),
		newVaultCmd(gf),
		newUpstreamCmd(gf),
		newClusterCmd(gf),
		newKubeconfigCmd(gf),
		newAgentCmd(gf),
		newRuleCmd(gf),
		newApprovalCmd(gf),
		newAccessCmd(gf),
		newAuditCmd(gf),
		newListUpstreamsCmd(gf),
		newWhoamiCmd(gf),
		newRequestHostAccessCmd(gf),
		newRequestAccessCmd(gf),
		newRequestPresetCmd(gf),
		newRequestK8sAccessCmd(gf),
		newGetAccessCmd(gf),
		newGetKubeconfigCmd(gf),
	)
```

- [ ] Create `internal/cli/agentplane.go` with the helpers and the eight commands:

```go
package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sipaha/outwall/internal/agentid"
	"github.com/Sipaha/outwall/internal/client"
)

// agentClient returns a client bound to the agent unix socket.
func agentClient(gf *globalFlags) *client.Client { return client.New(gf.agentSocket) }

// agentToken resolves (registering once on first use) the per-project agent token, minting it via
// the agent socket's /register endpoint. internal/agentid persists it per project.
func agentToken(gf *globalFlags) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	return agentid.LoadOrRegister(cwd, func(name string) (string, string, error) {
		var out struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		}
		if err := agentClient(gf).Do("POST", "/register", map[string]string{"name": name}, &out); err != nil {
			return "", "", err
		}
		return out.ID, out.Token, nil
	})
}

// printJSON writes v as indented JSON to the command's stdout (agent-friendly output).
func printJSON(c *cobra.Command, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(c.OutOrStdout(), string(b))
	return nil
}

func newListUpstreamsCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list-upstreams",
		Short: "List upstreams outwall knows about and your access status for each",
		RunE: func(c *cobra.Command, _ []string) error {
			token, err := agentToken(gf)
			if err != nil {
				return err
			}
			var out any
			if err := agentClient(gf).DoAuth(token, "GET", "/upstreams", nil, &out); err != nil {
				return err
			}
			return printJSON(c, out)
		},
	}
}

func newWhoamiCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Print your agent identity, data-plane bearer token, and current accesses",
		RunE: func(c *cobra.Command, _ []string) error {
			token, err := agentToken(gf)
			if err != nil {
				return err
			}
			var out any
			if err := agentClient(gf).DoAuth(token, "GET", "/whoami", nil, &out); err != nil {
				return err
			}
			return printJSON(c, out)
		},
	}
}

func newRequestHostAccessCmd(gf *globalFlags) *cobra.Command {
	var purpose string
	cmd := &cobra.Command{
		Use:   "request-host-access <host>",
		Short: "Request access to a host, stating your purpose",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			token, err := agentToken(gf)
			if err != nil {
				return err
			}
			var out any
			body := map[string]string{"host": args[0], "purpose": purpose}
			if err := agentClient(gf).DoAuth(token, "POST", "/access/host", body, &out); err != nil {
				return err
			}
			return printJSON(c, out)
		},
	}
	cmd.Flags().StringVar(&purpose, "purpose", "", "why you need this host (required)")
	return cmd
}

func newRequestAccessCmd(gf *globalFlags) *cobra.Command {
	var (
		method, path, purpose string
		query, values         map[string]string
	)
	cmd := &cobra.Command{
		Use:   "request-access <host>",
		Short: "Request access to an operation on an already-approved host",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			token, err := agentToken(gf)
			if err != nil {
				return err
			}
			body := map[string]any{
				"host": args[0], "method": method, "path_template": path,
				"query_template": query, "values": values, "purpose": purpose,
			}
			var out any
			if err := agentClient(gf).DoAuth(token, "POST", "/access/op", body, &out); err != nil {
				return err
			}
			return printJSON(c, out)
		},
	}
	cmd.Flags().StringVar(&method, "method", "GET", "HTTP method")
	cmd.Flags().StringVar(&path, "path", "", "path template with {name:type} placeholders")
	cmd.Flags().StringToStringVar(&query, "query", nil, "query template entries key=value (repeatable)")
	cmd.Flags().StringToStringVar(&values, "value", nil, "concrete values key=value (repeatable)")
	cmd.Flags().StringVar(&purpose, "purpose", "", "why you need this operation (required)")
	return cmd
}

func newRequestPresetCmd(gf *globalFlags) *cobra.Command {
	var (
		preset, purpose string
		vars            map[string]string
	)
	cmd := &cobra.Command{
		Use:   "request-preset <upstream>",
		Short: "Request a named preset (a bundle of rights) on an upstream",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			token, err := agentToken(gf)
			if err != nil {
				return err
			}
			body := map[string]any{"upstream": args[0], "preset": preset, "vars": vars, "purpose": purpose}
			var out any
			if err := agentClient(gf).DoAuth(token, "POST", "/access/preset", body, &out); err != nil {
				return err
			}
			return printJSON(c, out)
		},
	}
	cmd.Flags().StringVar(&preset, "preset", "", "preset id from list-upstreams (required)")
	cmd.Flags().StringToStringVar(&vars, "var", nil, "slot values key=value (repeatable)")
	cmd.Flags().StringVar(&purpose, "purpose", "", "why you need this preset (required)")
	return cmd
}

func newRequestK8sAccessCmd(gf *globalFlags) *cobra.Command {
	var (
		namespace, purpose string
		grantSpecs         []string
	)
	cmd := &cobra.Command{
		Use:   "request-k8s-access <cluster>",
		Short: "Request k8s access on a registered cluster for one namespace",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			token, err := agentToken(gf)
			if err != nil {
				return err
			}
			grants := make([]map[string]any, 0, len(grantSpecs))
			for _, g := range grantSpecs {
				parts := strings.SplitN(g, "=", 2)
				if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
					return fmt.Errorf("invalid --grant %q (want resource=verb1,verb2)", g)
				}
				grants = append(grants, map[string]any{
					"resource": parts[0], "verbs": strings.Split(parts[1], ","),
				})
			}
			body := map[string]any{"cluster": args[0], "namespace": namespace, "grants": grants, "purpose": purpose}
			var out any
			if err := agentClient(gf).DoAuth(token, "POST", "/access/k8s", body, &out); err != nil {
				return err
			}
			return printJSON(c, out)
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "k8s namespace")
	cmd.Flags().StringArrayVar(&grantSpecs, "grant", nil, "resource=verb1,verb2 (repeatable)")
	cmd.Flags().StringVar(&purpose, "purpose", "", "why you need this access (required)")
	return cmd
}

func newGetAccessCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "get-access <upstream>",
		Short: "Report your current access status for an upstream (waits for a pending decision)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			token, err := agentToken(gf)
			if err != nil {
				return err
			}
			var out any
			if err := agentClient(gf).DoAuth(token, "GET", "/access/"+url.PathEscape(args[0]), nil, &out); err != nil {
				return err
			}
			return printJSON(c, out)
		},
	}
}

func newGetKubeconfigCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "get-kubeconfig <cluster>",
		Short: "Print a kubeconfig for a registered k8s cluster using your own outwall token",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			token, err := agentToken(gf)
			if err != nil {
				return err
			}
			var out struct {
				Kubeconfig string `json:"kubeconfig"`
			}
			if err := agentClient(gf).DoAuth(token, "GET", "/kubeconfig/"+url.PathEscape(args[0]), nil, &out); err != nil {
				return err
			}
			fmt.Fprint(c.OutOrStdout(), out.Kubeconfig)
			return nil
		},
	}
}
```

- [ ] Run it — must PASS:

```bash
go test ./internal/cli/ -run TestListUpstreamsCommand
# expected: ok  github.com/Sipaha/outwall/internal/cli
```

- [ ] `make fmt && make vet && make test`, then commit:

```bash
git commit -am "feat(cli): agent-facing subcommands over the agent socket (per-project token)"
```

---

## Task 7 — daemon: wire the agent socket; drop the MCP handler field

The service `svc := mcpsvc.New(...)` and all `svc.SetX(...)` calls STAY — they are now consumed by the agent socket. Remove only the MCP handler build + field + TCP listener.

- [ ] Add a regression test `internal/daemon/agentsocket_test.go` proving the daemon serves `/register` over the agent socket:

```go
package daemon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServeAgentSocket(t *testing.T) {
	dir := t.TempDir()
	d, err := New(Config{
		DBPath:     filepath.Join(dir, "outwall.db"),
		SocketPath: filepath.Join(dir, "outwall.sock"),
		Listen:     "127.0.0.1:0",
		UIListen:   "127.0.0.1:0",
		PruneInterval: -1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = d.Serve(ctx) }()

	sock := filepath.Join(dir, "agent.sock")
	require.Eventually(t, func() bool {
		_, err := net.Dial("unix", sock)
		return err == nil
	}, 3*time.Second, 20*time.Millisecond)

	c := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
	resp, err := c.Post("http://unix/register", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var reg struct{ ID, Token string }
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&reg))
	require.NotEmpty(t, reg.Token)
}
```

- [ ] Run it — must FAIL (the daemon still binds MCP over TCP; `agent.sock` is never created and `Config` has no `AgentSocketPath`; also `Config.Listen: "127.0.0.1:0"` with the still-present `mcpSrv` TCP bind may fail):

```bash
go test ./internal/daemon/ -run TestServeAgentSocket
# expected: FAIL — dial unix .../agent.sock: no such file
```

- [ ] Edit `internal/daemon/daemon.go`:
  1. Remove the import `owmcp "github.com/Sipaha/outwall/internal/mcp"`; add `"github.com/Sipaha/outwall/internal/agentapi"`.
  2. Remove the constant `DefaultMCPListen`.
  3. In `Config`, remove the `MCPListen` field and add `AgentSocketPath string` (documented):

```go
	// AgentSocketPath is the unix socket for the agent plane (agentapi). Empty defaults to
	// <dir(DBPath)>/agent.sock. It is created 0600 alongside the admin socket.
	AgentSocketPath string
```

  4. In the `Daemon` struct, replace the field `mcp http.Handler` with `agentPlane http.Handler`.
  5. In `New`, remove the `if cfg.MCPListen == "" { cfg.MCPListen = DefaultMCPListen }` block and add, near the other defaults:

```go
	if cfg.AgentSocketPath == "" {
		cfg.AgentSocketPath = filepath.Join(filepath.Dir(cfg.DBPath), "agent.sock")
	}
```

  6. In `New`, delete the `mcpHandler, err := owmcp.NewHandler(...) { ... }` block and build the agent handler instead (keeping the `svc := mcpsvc.New(...)` + all `svc.SetX(...)` calls above it):

```go
	agentPlane := agentapi.NewHandler(agentapi.Deps{Svc: svc, Agents: ag, Locked: v.Locked})
```

  7. In the `d := &Daemon{...}` struct literal, replace `mcp: mcpHandler,` with `agentPlane: agentPlane,`.
  8. In `Serve`, replace the MCP TCP server with an agent unix listener. Delete:

```go
	mcpSrv := &http.Server{Addr: d.cfg.MCPListen, Handler: d.mcp}
```

  and add (after the admin socket setup, before the TLS block or alongside the admin listener):

```go
	_ = os.Remove(d.cfg.AgentSocketPath)
	agentLn, err := net.Listen("unix", d.cfg.AgentSocketPath)
	if err != nil {
		return fmt.Errorf("listen agent socket: %w", err)
	}
	if err := os.Chmod(d.cfg.AgentSocketPath, 0o600); err != nil {
		return fmt.Errorf("chmod agent socket: %w", err)
	}
	agentSrv := &http.Server{Handler: d.agentPlane}
```

  9. In `Serve`, keep `errc := make(chan error, 4)` (admin, data, agent, ui). Replace the MCP goroutine `go func() { errc <- mcpSrv.ListenAndServe() }()` with:

```go
	go func() { errc <- agentSrv.Serve(agentLn) }()
```

  10. In the `<-ctx.Done()` cleanup, replace `_ = mcpSrv.Close()` with:

```go
		_ = agentSrv.Close()
		_ = os.Remove(d.cfg.AgentSocketPath)
```

- [ ] Run it — must PASS:

```bash
go test ./internal/daemon/ -run TestServeAgentSocket
# expected: ok  github.com/Sipaha/outwall/internal/daemon
```

- [ ] `make fmt && make vet && make test` (the daemon still imports `internal/mcp` nowhere now; `internal/mcp` itself is removed in Task 8), then commit:

```bash
git commit -am "feat(daemon): serve the agent plane over agent.sock; drop the MCP handler"
```

---

## Task 8 — remove `internal/mcp`, the MCP listen flag/const/banner/config, and the go-sdk

- [ ] Delete the MCP adapter package:

```bash
git rm -r internal/mcp
```

- [ ] Edit `internal/cli/serve.go`: remove `MCPListen: gf.mcpListen` from the `daemon.Config{...}` literal and add `AgentSocketPath: gf.agentSocket`; update the banner. The `daemon.New(...)` call and `Fprintf` become:

```go
				d, err := daemon.New(daemon.Config{
					DBPath: gf.db, SocketPath: gf.socket, Listen: gf.listen,
					AgentSocketPath: gf.agentSocket, UIListen: gf.uiListen,
					CallbackListen: gf.callbackListen, BrowseDomain: gf.browseDomain,
				})
```

```go
				fmt.Fprintf(cmd.OutOrStdout(), "outwall serving: data plane %s, agent-socket %s, ui %s, admin %s\n",
					gf.listen, gf.agentSocket, gf.uiListen, gf.socket)
```

- [ ] Edit `cmd/outwall-desktop/main.go`: remove the `mcpListen` const from the `const (...)` block and remove `MCPListen: mcpListen,` from the `desktop.Run(daemon.Config{...})` literal. The agent socket defaults to `<dir>/agent.sock` (since `DBPath` is `<dir>/outwall.db`), so no explicit field is needed. Update the comment on the const block:

```go
// Loopback binds for the in-process daemon. The webview loads UIListen; the
// data plane is bound for agents running on the host. The agent plane is a unix
// socket (agent.sock), created by the daemon under the data dir.
const (
	uiListen   = "127.0.0.1:8182"
	dataListen = "127.0.0.1:8099"
	// cbListen is the fixed OIDC browser-login callback bind; its /callback is the redirect URI
	// registered in the IdP.
	cbListen = "127.0.0.1:23312"
)
```

- [ ] Search for any other `daemon.Config{...}` construction that still sets `MCPListen` and remove it:

```bash
grep -rn "MCPListen\|mcpListen\|mcp-listen\|DefaultMCPListen" --include='*.go' .
# expected: no matches (all removed)
```

- [ ] Drop the go-sdk dependency and verify it is gone:

```bash
go mod tidy
grep -rn "modelcontextprotocol" --include='*.go' .
# expected: no matches
grep -n "modelcontextprotocol" go.mod
# expected: no matches (may still appear in go.sum history until tidy prunes it; go.mod must be clean)
```

- [ ] Build the server binary (CGO-free) and run the full suite:

```bash
make build
# expected: dist/bin/outwall built, exit 0
make test
# expected: ok across all packages
```

- [ ] `make fmt && make vet`, then commit:

```bash
git commit -am "refactor: remove MCP control plane (internal/mcp + go-sdk), drop --mcp-listen"
```

---

## Task 9 — docs: new ADR-0040, supersede ADR-0003, overview, INDEX

Pick the next free ADR number by listing the dir (`ls docs/architecture/decisions/` — highest is `0039`, so this plan writes **ADR-0040**).

- [ ] Create `docs/architecture/decisions/0040-agent-socket-control-plane.md`:

```markdown
# ADR-0040: Agent socket control plane (CLI over agent.sock, per-project token)

- **Status:** accepted
- **Date:** 2026-07-06

## Context

The control plane was an MCP server (streamable HTTP; adapter `internal/mcp`). An agent harness binds
the MCP tool set at session start, so the tools never appear if the agent starts before outwall, go
stale if outwall is rebuilt mid-session, and identity was `session = agent` (ADR-0003): every
reconnect minted a new agent record + token, letting a long-lived agent escape a per-agent deny rule
by reconnecting. Agents should reach outwall through independent, stateless calls that do not care
about start order or daemon restarts. (Scope here is R1–R4 of the 2026-07-06 design spec; the
operator-plane sealing R5–R8 is a separate ADR.)

## Decision

- **Remove MCP entirely.** Delete `internal/mcp` and the `github.com/modelcontextprotocol/go-sdk`
  dependency. `mcpsvc.Service` and the registries it uses stay.
- **New agent plane** `internal/agentapi`: a plain net/http HTTP/JSON handler (`NewHandler(Deps)`)
  over the SAME `mcpsvc.Service`, served on a dedicated `0600` unix socket `~/.spk/outwall/agent.sock`.
  Each request authenticates by `Authorization: Bearer <owa-token>` via `agent.Registry.Authenticate`
  — no session cache. It is unprivileged by construction: it exposes only list/whoami/request/get
  routes; it cannot approve, grant, or unlock. Routes: `POST /register`, `GET /upstreams`,
  `GET /whoami`, `POST /access/{host,op,k8s,preset}`, `GET /access/{upstream}` (long-poll inside the
  service), `GET /kubeconfig/{cluster}`.
- **CLI is the agent's face** (`list-upstreams`, `whoami`, `request-host-access`, `request-access`,
  `request-preset`, `request-k8s-access`, `get-access`, `get-kubeconfig`). Each call is independent;
  start order and daemon restarts are irrelevant. The CLI knows the socket path by default
  (`--agent-socket`, defaulting under the data dir).
- **Per-project accountability token** (`internal/agentid`): the CLI persists the `owa_` token in
  `~/.spk/outwall/agents/<hex-sha256(projectKey)>.token` (`0600`), where `projectKey` is the realpath
  of the git top-level when cwd is inside a repo, else the realpath of cwd. A `cd` into a subdir keeps
  the same identity. First use registers once under an exclusive `flock` on `<path>.lock` (winner
  writes atomically, losers read), so concurrent first-calls mint exactly one agent. The token hash is
  persisted by the registry, so `Authenticate` is valid across daemon restarts.

The token is **accountability-only, not an isolation boundary**: a same-user process can read any
project's token file. The real security boundary (operator approvals gated by the master password) is
addressed by the separate operator-session ADR.

## Alternatives considered

- **Keep MCP alongside the new plane** — rejected: the user wants MCP gone; alpha has no compat to
  preserve, and two control planes double the surface.
- **Per-directory (not per-project) token** — rejected: a `cd` into a subdir would mint a new agent,
  re-introducing the record-accretion problem. Keying on the git top-level fixes identity per project.
- **Serve the agent plane over loopback TCP** — rejected: a unix socket needs no port allocation, is
  `0600` by default, and matches the existing admin-socket pattern.

## Consequences

- Agents call independent CLI commands; no start-order or restart fragility; one stable agent per
  project. The `mcpsvc.Service` and its tests are unchanged (SDK-free all along).
- The go-sdk dependency and its transitive tree are dropped.
- Supersedes ADR-0003 (MCP control plane, session=agent identity).
- A future revisit that wants true cross-process isolation of the token would need a separate OS
  identity for the daemon (recorded as the escalation path in the threat-model ADR).
```

- [ ] Edit `docs/architecture/decisions/0003-mcp-control-plane.md`: change the status line and add a supersede note directly under it. Replace:

```markdown
- **Status:** accepted
```

with:

```markdown
- **Status:** superseded by ADR-0040
- **Superseded by:** [ADR-0040](0040-agent-socket-control-plane.md) — the MCP control plane is
  replaced by a direct agent socket + CLI with a per-project accountability token; `session = agent`
  identity is retired.
```

- [ ] Edit `docs/architecture/overview.md`:
  - In the two-plane ASCII/bullets, change the control-plane line `control plane (MCP, streamable HTTP, localhost)` to `control plane (agent socket, HTTP/JSON over unix, localhost)`.
  - Replace the control-plane bullet (lines ~20-22) with:

```markdown
- **Control plane** — a plain HTTP/JSON handler on a `0600` unix socket (`agent.sock`), driven by the
  `outwall` CLI. Agents discover what they may call, self-register (per-project token), and receive a
  bearer token. Commands: `list-upstreams`, `request-host-access`/`request-access`/`request-preset`,
  `get-access`, `whoami`, `get-kubeconfig`.
```

  - In the Subsystems table, replace the `| `mcp` | the control-plane MCP server. |` row with:

```markdown
| `agentapi` | the control-plane HTTP/JSON adapter over `mcpsvc.Service`, served on the agent socket. |
| `agentid` | the CLI's per-project agent-token store (realpath-of-git-top-level keyed, flock mint-once). |
```

  - In the Plan-1 caveat sentence, drop the stale `mcp` reference if present.

- [ ] Edit `docs/INDEX.md`:
  - In the module-doc list (the `policy`, `approval`, ... line), drop `mcp`, keep `mcpsvc`, and add `agentapi`, `agentid`.
  - In the ADR list, append a line for `0040-agent-socket-control-plane.md`, and update the `0003` line to note it is superseded by ADR-0040.

- [ ] Verify docs build/links and run the gate:

```bash
grep -rn "internal/mcp\b\|control-plane MCP\|--mcp-listen" docs/
# expected: only historical mentions inside superseded ADRs (0003) / older plans — no live references
make fmt && make vet && make test
```

- [ ] Commit:

```bash
git commit -am "docs: ADR-0040 agent socket control plane; supersede ADR-0003; overview + INDEX"
```

---

## Requirement coverage check (R1–R4)

- **R1 (remove MCP + go-sdk):** Tasks 7 (drop handler) + 8 (delete package, drop dep, `go mod tidy`).
- **R2 (agent plane over agent.sock, thin adapter over mcpsvc.Service, unprivileged):** Tasks 3, 4 (`internal/agentapi`) + 7 (unix listener, `0600`).
- **R3 (CLI is the agent's face; independent, restart-immune calls):** Tasks 5 (`DoAuth`) + 6 (eight subcommands + `--agent-socket`).
- **R4 (per-project identity token under `agents/<hash>.token`, flock, survives restart):** Tasks 1, 2 (`internal/agentid`) + 6 (CLI wiring).

## Final full-gate

- [ ] `make check` (fmt-check + vet + lint + test) once golangci-lint is wired; otherwise `make fmt && make vet && make test && make build`.
- [ ] Manual smoke (optional, `make run-server` in one shell): `outwall list-upstreams` in a project dir registers once and prints upstreams; re-running reuses the same token; `outwall whoami` prints the identity AND the data-plane token.
