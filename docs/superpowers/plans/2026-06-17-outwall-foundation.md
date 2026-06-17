# outwall — Plan 1: Foundation & Data-Plane Skeleton

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up a working vertical slice of outwall: an agent's bearer token, a vault-encrypted upstream credential, and a localhost reverse proxy that injects upstream auth, enforces default-deny via grants, and returns 503 while the vault is locked — all driven by a CLI talking to the daemon over a Unix socket.

**Architecture:** Single Go binary (`outwall`) that is both daemon and CLI, mirroring the citeck-launcher pattern (no citeck code/deps). The daemon (`internal/daemon`) wires a SQLite store, an in-memory-keyed vault, upstream/agent/grant registries, and an `httputil.ReverseProxy`-based data plane, and exposes a small admin API over a `0600` Unix socket. CLI subcommands are thin clients of that socket. No MCP, no web UI, no policy engine yet — those are later milestones.

**Tech Stack:** Go 1.26, `modernc.org/sqlite` (pure-Go, no CGO), `golang.org/x/crypto/argon2`, `crypto/aes`+`crypto/cipher` (AES-256-GCM), `spf13/cobra`, `net/http` (stdlib, Go 1.22 routing), `log/slog`, `stretchr/testify`.

## Global Constraints

- Go module path: `github.com/Sipaha/outwall` — exact, verbatim, in every import.
- **No `citeck` strings, imports, or branding anywhere.** citeck-launcher is reference-only.
- Go version floor: `go 1.26` in `go.mod`.
- SQLite driver: `modernc.org/sqlite` (driver name `"sqlite"`), pure-Go, **CGO must stay disabled** for the server binary (`CGO_ENABLED=0`).
- Crypto: KDF = Argon2id with params `time=1, memory=64*1024 (64 MiB), threads=4, keyLen=32`; cipher = AES-256-GCM with a fresh 12-byte random nonce per encryption, stored as `nonce || ciphertext`.
- Indentation: tabs (Go standard, `gofmt`).
- Logging: `log/slog`.
- Tests: `stretchr/testify` (`require`/`assert`). Every task ends green via `go test ./...`.
- Unix socket admin listener: file mode `0600`.
- Agent token format: `owa_` + base64url(32 random bytes); only the SHA-256 hex of the token is persisted.

## File Structure

```
outwall/
├── go.mod, go.sum
├── Makefile
├── cmd/outwall/main.go              # cobra entrypoint
├── internal/
│   ├── version/version.go           # version string
│   ├── store/
│   │   ├── store.go                 # Open(), *Store wrapping *sql.DB
│   │   └── migrate.go               # schema migrations
│   ├── secret/
│   │   └── vault.go                 # KDF, AES-GCM, Init/Unlock/Lock/Encrypt/Decrypt
│   ├── upstream/
│   │   └── registry.go              # Upstream model + CRUD (secrets encrypted via vault)
│   ├── agent/
│   │   └── registry.go              # Agent model + Register/Authenticate
│   ├── grant/
│   │   └── registry.go              # simple (agent,upstream) allow rows  [replaced by policy in Plan 2]
│   ├── authn/
│   │   └── authn.go                 # Authenticator interface + none/static/basic
│   ├── proxy/
│   │   └── proxy.go                 # data-plane http.Handler
│   ├── daemon/
│   │   ├── daemon.go                # Daemon struct wiring everything
│   │   └── admin.go                 # Unix-socket admin API (handlers)
│   ├── client/
│   │   └── client.go                # CLI → daemon admin client over unix socket
│   └── cli/
│       ├── root.go                  # cobra root + global flags
│       ├── serve.go                 # `outwall serve`
│       ├── vault.go                 # `outwall vault init|unlock|status`
│       ├── upstream.go              # `outwall upstream add|list`
│       ├── agent.go                 # `outwall agent list`
│       └── grant.go                 # `outwall grant add`
└── docs/superpowers/...
```

Each `internal/*` package is independently unit-tested. `internal/daemon` is the only package that composes them.

---

### Task 1: Repo scaffold + version command

**Files:**
- Create: `go.mod`, `Makefile`, `cmd/outwall/main.go`, `internal/version/version.go`, `internal/cli/root.go`, `internal/version/version_test.go`

**Interfaces:**
- Produces: `version.String() string`; `cli.NewRootCmd() *cobra.Command`.

- [ ] **Step 1: Write the failing test**

`internal/version/version_test.go`:
```go
package version

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStringNotEmpty(t *testing.T) {
	require.NotEmpty(t, String())
	require.Contains(t, String(), ".")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/version/`
Expected: FAIL — package/function not defined (or build error: no go.mod yet).

- [ ] **Step 3: Write minimal implementation**

`go.mod`:
```
module github.com/Sipaha/outwall

go 1.26

require (
	github.com/spf13/cobra v1.10.2
	github.com/stretchr/testify v1.11.1
)
```

`internal/version/version.go`:
```go
// Package version exposes the build version string.
package version

// version is overridden at build time via -ldflags "-X .../version.version=...".
var version = "0.1.0-dev"

// String returns the current build version.
func String() string { return version }
```

`internal/cli/root.go`:
```go
// Package cli defines the outwall command tree.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/Sipaha/outwall/internal/version"
)

// NewRootCmd builds the root cobra command.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "outwall",
		Short:         "Authenticating egress gateway for AI agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.String(),
	}
	return root
}
```

`cmd/outwall/main.go`:
```go
package main

import (
	"fmt"
	"os"

	"github.com/Sipaha/outwall/internal/cli"
)

func main() {
	if err := cli.NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
```

`Makefile`:
```makefile
BINDIR := dist/bin
BIN := $(BINDIR)/outwall

.PHONY: build test fmt vet tidy
build:
	@mkdir -p $(BINDIR)
	CGO_ENABLED=0 go build -ldflags "-X github.com/Sipaha/outwall/internal/version.version=$(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)" -o $(BIN) ./cmd/outwall

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

tidy:
	go mod tidy
```

- [ ] **Step 4: Run test + build to verify they pass**

Run: `go mod tidy && go test ./internal/version/ && make build && ./dist/bin/outwall --version`
Expected: test PASS; build OK; prints `outwall version 0.1.0-dev`.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum Makefile cmd internal/version internal/cli
git commit -m "feat: scaffold outwall binary and version command"
```

---

### Task 2: SQLite store + migrations

**Files:**
- Create: `internal/store/store.go`, `internal/store/migrate.go`, `internal/store/store_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `store.Open(path string) (*store.Store, error)` — opens/creates the DB file and runs migrations.
  - `(*store.Store).DB() *sql.DB` — underlying handle for other packages.
  - `(*store.Store).Close() error`.
  - Schema tables created: `vault_meta(id INTEGER PRIMARY KEY CHECK(id=1), salt BLOB, verifier BLOB)`, `upstreams(id TEXT PRIMARY KEY, name TEXT UNIQUE, base_url TEXT, auth_type TEXT, auth_config BLOB, created_at TEXT)`, `agents(id TEXT PRIMARY KEY, name TEXT, token_sha256 TEXT UNIQUE, status TEXT, created_at TEXT)`, `grants(agent_id TEXT, upstream_id TEXT, created_at TEXT, PRIMARY KEY(agent_id, upstream_id))`.

- [ ] **Step 1: Write the failing test**

`internal/store/store_test.go`:
```go
package store

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAppliesSchemaIdempotently(t *testing.T) {
	p := filepath.Join(t.TempDir(), "outwall.db")

	s, err := Open(p)
	require.NoError(t, err)

	// Tables exist.
	for _, table := range []string{"vault_meta", "upstreams", "agents", "grants"} {
		var name string
		err := s.DB().QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		require.NoError(t, err, "table %s missing", table)
		require.Equal(t, table, name)
	}
	require.NoError(t, s.Close())

	// Re-open is idempotent (migrations don't fail on existing schema).
	s2, err := Open(p)
	require.NoError(t, err)
	require.NoError(t, s2.Close())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/`
Expected: FAIL — `Open` undefined.

- [ ] **Step 3: Write minimal implementation**

`internal/store/store.go`:
```go
// Package store is the SQLite persistence layer for outwall.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite database handle.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and applies migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // single writer; avoids SQLITE_BUSY in the daemon
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pragma: %w", err)
	}
	s := &Store{db: db}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// DB returns the underlying database handle.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }
```

`internal/store/migrate.go`:
```go
package store

import (
	"database/sql"
	"fmt"
)

const schema = `
CREATE TABLE IF NOT EXISTS vault_meta (
	id       INTEGER PRIMARY KEY CHECK (id = 1),
	salt     BLOB NOT NULL,
	verifier BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS upstreams (
	id          TEXT PRIMARY KEY,
	name        TEXT NOT NULL UNIQUE,
	base_url    TEXT NOT NULL,
	auth_type   TEXT NOT NULL,
	auth_config BLOB,
	created_at  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS agents (
	id           TEXT PRIMARY KEY,
	name         TEXT NOT NULL,
	token_sha256 TEXT NOT NULL UNIQUE,
	status       TEXT NOT NULL,
	created_at   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS grants (
	agent_id    TEXT NOT NULL,
	upstream_id TEXT NOT NULL,
	created_at  TEXT NOT NULL,
	PRIMARY KEY (agent_id, upstream_id)
);
`

func migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go mod tidy && go test ./internal/store/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/store
git commit -m "feat: SQLite store with schema migrations"
```

---

### Task 3: Secret vault (Argon2id + AES-256-GCM)

**Files:**
- Create: `internal/secret/vault.go`, `internal/secret/vault_test.go`

**Interfaces:**
- Consumes: `*store.Store` (reads/writes `vault_meta`).
- Produces:
  - `secret.NewVault(s *store.Store) *secret.Vault`
  - `(*Vault).Initialized() (bool, error)` — whether `vault_meta` row exists.
  - `(*Vault).Init(password string) error` — first-time setup: random salt, store salt+verifier; leaves vault **unlocked**.
  - `(*Vault).Unlock(password string) error` — derive key, verify against stored verifier; errors `ErrNotInitialized`, `ErrBadPassword`.
  - `(*Vault).Lock()` — zero the in-memory key.
  - `(*Vault).Locked() bool`
  - `(*Vault).Encrypt(plaintext []byte) ([]byte, error)` — `ErrLocked` if locked.
  - `(*Vault).Decrypt(ciphertext []byte) ([]byte, error)` — `ErrLocked` if locked.

- [ ] **Step 1: Write the failing test**

`internal/secret/vault_test.go`:
```go
package secret

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/store"
)

func newVault(t *testing.T) *Vault {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "v.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return NewVault(s)
}

func TestInitUnlockRoundTrip(t *testing.T) {
	v := newVault(t)

	init, err := v.Initialized()
	require.NoError(t, err)
	require.False(t, init)

	require.NoError(t, v.Init("correct horse"))
	require.False(t, v.Locked()) // Init leaves it unlocked

	enc, err := v.Encrypt([]byte("s3cr3t"))
	require.NoError(t, err)
	require.NotContains(t, string(enc), "s3cr3t")

	v.Lock()
	require.True(t, v.Locked())
	_, err = v.Encrypt([]byte("x"))
	require.ErrorIs(t, err, ErrLocked)

	require.ErrorIs(t, v.Unlock("wrong"), ErrBadPassword)
	require.NoError(t, v.Unlock("correct horse"))

	dec, err := v.Decrypt(enc)
	require.NoError(t, err)
	require.Equal(t, "s3cr3t", string(dec))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/secret/`
Expected: FAIL — `NewVault` / `Vault` undefined.

- [ ] **Step 3: Write minimal implementation**

`internal/secret/vault.go`:
```go
// Package secret implements the master-password vault: Argon2id key derivation
// and per-secret AES-256-GCM encryption. The derived key lives only in memory
// while unlocked; the master password itself is never stored.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/crypto/argon2"

	"github.com/Sipaha/outwall/internal/store"
)

var (
	ErrNotInitialized = errors.New("vault not initialized")
	ErrBadPassword    = errors.New("incorrect master password")
	ErrLocked         = errors.New("vault is locked")
)

// Argon2id parameters (see Global Constraints).
const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	keyLen       = 32
	saltLen      = 16
	nonceLen     = 12
)

// verifierPlaintext is encrypted at Init and decrypted at Unlock to check the password.
var verifierPlaintext = []byte("outwall-vault-verifier-v1")

// Vault holds the derived key in memory while unlocked.
type Vault struct {
	store *store.Store
	mu    sync.RWMutex
	key   []byte // nil when locked
}

// NewVault constructs a vault backed by the given store.
func NewVault(s *store.Store) *Vault { return &Vault{store: s} }

// Initialized reports whether a vault_meta row exists.
func (v *Vault) Initialized() (bool, error) {
	var n int
	err := v.store.DB().QueryRow(`SELECT COUNT(*) FROM vault_meta WHERE id=1`).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("query vault_meta: %w", err)
	}
	return n > 0, nil
}

func deriveKey(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, keyLen)
}

// Init performs first-time setup and leaves the vault unlocked.
func (v *Vault) Init(password string) error {
	init, err := v.Initialized()
	if err != nil {
		return err
	}
	if init {
		return errors.New("vault already initialized")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("read salt: %w", err)
	}
	key := deriveKey(password, salt)
	verifier, err := sealWith(key, verifierPlaintext)
	if err != nil {
		return err
	}
	if _, err := v.store.DB().Exec(
		`INSERT INTO vault_meta (id, salt, verifier) VALUES (1, ?, ?)`, salt, verifier,
	); err != nil {
		return fmt.Errorf("store vault_meta: %w", err)
	}
	v.mu.Lock()
	v.key = key
	v.mu.Unlock()
	return nil
}

// Unlock derives the key and verifies it against the stored verifier.
func (v *Vault) Unlock(password string) error {
	var salt, verifier []byte
	err := v.store.DB().QueryRow(`SELECT salt, verifier FROM vault_meta WHERE id=1`).Scan(&salt, &verifier)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotInitialized
	}
	if err != nil {
		return fmt.Errorf("load vault_meta: %w", err)
	}
	key := deriveKey(password, salt)
	got, err := openWith(key, verifier)
	if err != nil || string(got) != string(verifierPlaintext) {
		return ErrBadPassword
	}
	v.mu.Lock()
	v.key = key
	v.mu.Unlock()
	return nil
}

// Lock zeroes the in-memory key.
func (v *Vault) Lock() {
	v.mu.Lock()
	for i := range v.key {
		v.key[i] = 0
	}
	v.key = nil
	v.mu.Unlock()
}

// Locked reports whether the vault is currently locked.
func (v *Vault) Locked() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.key == nil
}

// Encrypt seals plaintext with the unlocked key.
func (v *Vault) Encrypt(plaintext []byte) ([]byte, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.key == nil {
		return nil, ErrLocked
	}
	return sealWith(v.key, plaintext)
}

// Decrypt opens ciphertext with the unlocked key.
func (v *Vault) Decrypt(ciphertext []byte) ([]byte, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.key == nil {
		return nil, ErrLocked
	}
	return openWith(v.key, ciphertext)
}

func sealWith(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func openWith(key, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < nonceLen {
		return nil, errors.New("ciphertext too short")
	}
	nonce, body := ciphertext[:nonceLen], ciphertext[nonceLen:]
	pt, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return nil, fmt.Errorf("gcm open: %w", err)
	}
	return pt, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return gcm, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go mod tidy && go test ./internal/secret/`
Expected: PASS. (Argon2id makes this test take ~0.1–0.3s — acceptable.)

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/secret
git commit -m "feat: master-password vault (Argon2id + AES-256-GCM)"
```

---

### Task 4: Upstream registry (vault-encrypted auth config)

**Files:**
- Create: `internal/upstream/registry.go`, `internal/upstream/registry_test.go`

**Interfaces:**
- Consumes: `*store.Store`, `*secret.Vault`.
- Produces:
  - Type `upstream.AuthConfig struct { Type string; Header string; Token string; Username string; Password string }` (JSON-serializable; superset covering none/static/basic — OIDC fields added in Plan 2).
  - Type `upstream.Upstream struct { ID, Name, BaseURL, AuthType string; Auth AuthConfig; CreatedAt time.Time }`.
  - `upstream.NewRegistry(s *store.Store, v *secret.Vault) *upstream.Registry`
  - `(*Registry).Create(name, baseURL string, auth AuthConfig) (*Upstream, error)` — encrypts the JSON-marshaled `AuthConfig` via the vault before storing in `auth_config`.
  - `(*Registry).GetByName(name string) (*Upstream, error)` — decrypts `Auth`; `ErrNotFound` if absent.
  - `(*Registry).List() ([]*Upstream, error)` — decrypts each (vault must be unlocked).

- [ ] **Step 1: Write the failing test**

`internal/upstream/registry_test.go`:
```go
package upstream

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
)

func setup(t *testing.T) (*store.Store, *Registry) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "u.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	v := secret.NewVault(s)
	require.NoError(t, v.Init("pw"))
	return s, NewRegistry(s, v)
}

func TestCreateEncryptsAuthConfig(t *testing.T) {
	s, reg := setup(t)

	up, err := reg.Create("github", "https://api.github.com", AuthConfig{
		Type: "static", Header: "Authorization", Token: "Bearer ghp_secret",
	})
	require.NoError(t, err)
	require.NotEmpty(t, up.ID)

	// Raw stored blob must NOT contain the plaintext token.
	var blob []byte
	require.NoError(t, s.DB().QueryRow(
		`SELECT auth_config FROM upstreams WHERE id=?`, up.ID).Scan(&blob))
	require.NotContains(t, string(blob), "ghp_secret")

	got, err := reg.GetByName("github")
	require.NoError(t, err)
	require.Equal(t, "Bearer ghp_secret", got.Auth.Token)
	require.Equal(t, "https://api.github.com", got.BaseURL)

	_, err = reg.GetByName("missing")
	require.ErrorIs(t, err, ErrNotFound)

	_ = sql.ErrNoRows // keep import honest if refactored
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/upstream/`
Expected: FAIL — `NewRegistry` undefined.

- [ ] **Step 3: Write minimal implementation**

`internal/upstream/registry.go`:
```go
// Package upstream is the registry of named external APIs and their (encrypted) auth config.
package upstream

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
)

// ErrNotFound is returned when an upstream does not exist.
var ErrNotFound = errors.New("upstream not found")

// AuthConfig is the (encrypted-at-rest) credential material for an upstream.
// OIDC fields are added in Plan 2.
type AuthConfig struct {
	Type     string `json:"type"` // none | static | basic
	Header   string `json:"header,omitempty"`
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// Upstream is a named external API.
type Upstream struct {
	ID        string
	Name      string
	BaseURL   string
	AuthType  string
	Auth      AuthConfig
	CreatedAt time.Time
}

// Registry persists upstreams.
type Registry struct {
	store *store.Store
	vault *secret.Vault
}

// NewRegistry constructs an upstream registry.
func NewRegistry(s *store.Store, v *secret.Vault) *Registry {
	return &Registry{store: s, vault: v}
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Create encrypts the auth config and stores a new upstream.
func (r *Registry) Create(name, baseURL string, auth AuthConfig) (*Upstream, error) {
	raw, err := json.Marshal(auth)
	if err != nil {
		return nil, fmt.Errorf("marshal auth: %w", err)
	}
	enc, err := r.vault.Encrypt(raw)
	if err != nil {
		return nil, fmt.Errorf("encrypt auth: %w", err)
	}
	up := &Upstream{
		ID: newID(), Name: name, BaseURL: baseURL, AuthType: auth.Type,
		Auth: auth, CreatedAt: time.Now().UTC(),
	}
	_, err = r.store.DB().Exec(
		`INSERT INTO upstreams (id, name, base_url, auth_type, auth_config, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		up.ID, up.Name, up.BaseURL, up.AuthType, enc, up.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("insert upstream: %w", err)
	}
	return up, nil
}

func (r *Registry) scan(row interface{ Scan(...any) error }) (*Upstream, error) {
	var (
		up      Upstream
		enc     []byte
		created string
	)
	if err := row.Scan(&up.ID, &up.Name, &up.BaseURL, &up.AuthType, &enc, &created); err != nil {
		return nil, err
	}
	up.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	raw, err := r.vault.Decrypt(enc)
	if err != nil {
		return nil, fmt.Errorf("decrypt auth: %w", err)
	}
	if err := json.Unmarshal(raw, &up.Auth); err != nil {
		return nil, fmt.Errorf("unmarshal auth: %w", err)
	}
	return &up, nil
}

// GetByName returns the upstream with the given name.
func (r *Registry) GetByName(name string) (*Upstream, error) {
	row := r.store.DB().QueryRow(
		`SELECT id, name, base_url, auth_type, auth_config, created_at FROM upstreams WHERE name=?`, name)
	up, err := r.scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return up, nil
}

// List returns all upstreams (vault must be unlocked).
func (r *Registry) List() ([]*Upstream, error) {
	rows, err := r.store.DB().Query(
		`SELECT id, name, base_url, auth_type, auth_config, created_at FROM upstreams ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("query upstreams: %w", err)
	}
	defer rows.Close()
	var out []*Upstream
	for rows.Next() {
		up, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, up)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/upstream/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/upstream
git commit -m "feat: upstream registry with vault-encrypted auth config"
```

---

### Task 5: Agent registry (dynamic registration + tokens)

**Files:**
- Create: `internal/agent/registry.go`, `internal/agent/registry_test.go`

**Interfaces:**
- Consumes: `*store.Store`.
- Produces:
  - Type `agent.Agent struct { ID, Name, Status string; CreatedAt time.Time }` (status: `new` initially).
  - `agent.NewRegistry(s *store.Store) *agent.Registry`
  - `(*Registry).Register(name string) (a *Agent, token string, err error)` — generates `owa_<base64url-32B>` token, stores its SHA-256 hex, status `new`.
  - `(*Registry).Authenticate(token string) (*Agent, error)` — hashes token, looks up; `ErrUnknownToken` if none.
  - `(*Registry).List() ([]*Agent, error)`.

- [ ] **Step 1: Write the failing test**

`internal/agent/registry_test.go`:
```go
package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/store"
)

func newReg(t *testing.T) *Registry {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return NewRegistry(s)
}

func TestRegisterAndAuthenticate(t *testing.T) {
	reg := newReg(t)

	a, token, err := reg.Register("claude-code")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(token, "owa_"))
	require.Equal(t, "new", a.Status)

	got, err := reg.Authenticate(token)
	require.NoError(t, err)
	require.Equal(t, a.ID, got.ID)

	_, err = reg.Authenticate("owa_bogus")
	require.ErrorIs(t, err, ErrUnknownToken)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/`
Expected: FAIL — `NewRegistry` undefined.

- [ ] **Step 3: Write minimal implementation**

`internal/agent/registry.go`:
```go
// Package agent is the registry of agents that connect through outwall.
package agent

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Sipaha/outwall/internal/store"
)

// ErrUnknownToken is returned when a token matches no agent.
var ErrUnknownToken = errors.New("unknown agent token")

// StatusNew is the default status of a freshly registered agent (default-deny).
const StatusNew = "new"

// Agent is a registered consumer of the gateway.
type Agent struct {
	ID        string
	Name      string
	Status    string
	CreatedAt time.Time
}

// Registry persists agents and their token hashes.
type Registry struct {
	store *store.Store
}

// NewRegistry constructs an agent registry.
func NewRegistry(s *store.Store) *Registry { return &Registry{store: s} }

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Register creates a new agent and returns its bearer token (shown once).
func (r *Registry) Register(name string) (*Agent, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", fmt.Errorf("read token: %w", err)
	}
	token := "owa_" + base64.RawURLEncoding.EncodeToString(raw)
	a := &Agent{ID: newID(), Name: name, Status: StatusNew, CreatedAt: time.Now().UTC()}
	_, err := r.store.DB().Exec(
		`INSERT INTO agents (id, name, token_sha256, status, created_at) VALUES (?, ?, ?, ?, ?)`,
		a.ID, a.Name, hashToken(token), a.Status, a.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, "", fmt.Errorf("insert agent: %w", err)
	}
	return a, token, nil
}

// Authenticate resolves an agent by its bearer token.
func (r *Registry) Authenticate(token string) (*Agent, error) {
	var (
		a       Agent
		created string
	)
	err := r.store.DB().QueryRow(
		`SELECT id, name, status, created_at FROM agents WHERE token_sha256=?`, hashToken(token),
	).Scan(&a.ID, &a.Name, &a.Status, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnknownToken
	}
	if err != nil {
		return nil, fmt.Errorf("query agent: %w", err)
	}
	a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &a, nil
}

// List returns all agents.
func (r *Registry) List() ([]*Agent, error) {
	rows, err := r.store.DB().Query(
		`SELECT id, name, status, created_at FROM agents ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("query agents: %w", err)
	}
	defer rows.Close()
	var out []*Agent
	for rows.Next() {
		var (
			a       Agent
			created string
		)
		if err := rows.Scan(&a.ID, &a.Name, &a.Status, &created); err != nil {
			return nil, err
		}
		a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, &a)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent
git commit -m "feat: agent registry with dynamic registration and bearer tokens"
```

---

### Task 6: Authenticators (none / static / basic)

**Files:**
- Create: `internal/authn/authn.go`, `internal/authn/authn_test.go`

**Interfaces:**
- Consumes: `upstream.AuthConfig`.
- Produces:
  - Interface `authn.Authenticator interface { Apply(req *http.Request) error }`.
  - `authn.For(cfg upstream.AuthConfig) (Authenticator, error)` — factory; `ErrUnsupported` for unknown types. (This is the **pluggable seam**: Plan 2 adds an `oidc-client-credentials` case and a token-refresh-capable type without touching callers.)

- [ ] **Step 1: Write the failing test**

`internal/authn/authn_test.go`:
```go
package authn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/upstream"
)

func TestApply(t *testing.T) {
	cases := []struct {
		name   string
		cfg    upstream.AuthConfig
		assert func(t *testing.T, r *http.Request)
	}{
		{"none", upstream.AuthConfig{Type: "none"}, func(t *testing.T, r *http.Request) {
			require.Empty(t, r.Header.Get("Authorization"))
		}},
		{"static", upstream.AuthConfig{Type: "static", Header: "X-API-Key", Token: "k123"},
			func(t *testing.T, r *http.Request) {
				require.Equal(t, "k123", r.Header.Get("X-API-Key"))
			}},
		{"basic", upstream.AuthConfig{Type: "basic", Username: "u", Password: "p"},
			func(t *testing.T, r *http.Request) {
				gotU, gotP, ok := r.BasicAuth()
				require.True(t, ok)
				require.Equal(t, "u", gotU)
				require.Equal(t, "p", gotP)
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := For(tc.cfg)
			require.NoError(t, err)
			r, _ := http.NewRequest(http.MethodGet, "https://x", nil)
			require.NoError(t, a.Apply(r))
			tc.assert(t, r)
		})
	}

	_, err := For(upstream.AuthConfig{Type: "weird"})
	require.ErrorIs(t, err, ErrUnsupported)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/authn/`
Expected: FAIL — `For` undefined.

- [ ] **Step 3: Write minimal implementation**

`internal/authn/authn.go`:
```go
// Package authn injects upstream credentials into proxied requests.
// Authenticator is the pluggable seam for future schemes (OIDC, mTLS, SigV4, HMAC).
package authn

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/Sipaha/outwall/internal/upstream"
)

// ErrUnsupported is returned for an unknown auth type.
var ErrUnsupported = errors.New("unsupported auth type")

// Authenticator mutates an outgoing request to add upstream credentials.
type Authenticator interface {
	Apply(req *http.Request) error
}

// For builds an Authenticator from an upstream's auth config.
func For(cfg upstream.AuthConfig) (Authenticator, error) {
	switch cfg.Type {
	case "none", "":
		return noneAuth{}, nil
	case "static":
		if cfg.Header == "" {
			return nil, fmt.Errorf("static auth: empty header")
		}
		return staticAuth{header: cfg.Header, token: cfg.Token}, nil
	case "basic":
		return basicAuth{user: cfg.Username, pass: cfg.Password}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupported, cfg.Type)
	}
}

type noneAuth struct{}

func (noneAuth) Apply(*http.Request) error { return nil }

type staticAuth struct{ header, token string }

func (s staticAuth) Apply(r *http.Request) error {
	r.Header.Set(s.header, s.token)
	return nil
}

type basicAuth struct{ user, pass string }

func (b basicAuth) Apply(r *http.Request) error {
	r.SetBasicAuth(b.user, b.pass)
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/authn/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/authn
git commit -m "feat: upstream authenticators (none/static/basic) with pluggable factory"
```

---

### Task 7: Grant registry (default-deny allow rows)

**Files:**
- Create: `internal/grant/registry.go`, `internal/grant/registry_test.go`

**Interfaces:**
- Consumes: `*store.Store`.
- Produces:
  - `grant.NewRegistry(s *store.Store) *grant.Registry`
  - `(*Registry).Add(agentID, upstreamID string) error` — idempotent (INSERT OR IGNORE).
  - `(*Registry).Allowed(agentID, upstreamID string) (bool, error)`.
- Note: this is the **minimal stand-in for the policy engine** so the data plane has a real allow/deny decision in Plan 1. Plan 2 replaces `Allowed` with the rule engine (method/path/rate-limit/require-approval) behind the same call site in the proxy.

- [ ] **Step 1: Write the failing test**

`internal/grant/registry_test.go`:
```go
package grant

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/store"
)

func TestDefaultDenyThenAllow(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "g.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	reg := NewRegistry(s)

	ok, err := reg.Allowed("a1", "u1")
	require.NoError(t, err)
	require.False(t, ok) // default-deny

	require.NoError(t, reg.Add("a1", "u1"))
	require.NoError(t, reg.Add("a1", "u1")) // idempotent

	ok, err = reg.Allowed("a1", "u1")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = reg.Allowed("a1", "u2")
	require.NoError(t, err)
	require.False(t, ok)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/grant/`
Expected: FAIL — `NewRegistry` undefined.

- [ ] **Step 3: Write minimal implementation**

`internal/grant/registry.go`:
```go
// Package grant is the Plan-1 stand-in for the policy engine: a flat allow-list of
// (agent, upstream) pairs enforcing default-deny. Replaced by internal/policy in Plan 2.
package grant

import (
	"fmt"
	"time"

	"github.com/Sipaha/outwall/internal/store"
)

// Registry persists allow rows.
type Registry struct {
	store *store.Store
}

// NewRegistry constructs a grant registry.
func NewRegistry(s *store.Store) *Registry { return &Registry{store: s} }

// Add grants an agent access to an upstream (idempotent).
func (r *Registry) Add(agentID, upstreamID string) error {
	_, err := r.store.DB().Exec(
		`INSERT OR IGNORE INTO grants (agent_id, upstream_id, created_at) VALUES (?, ?, ?)`,
		agentID, upstreamID, time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert grant: %w", err)
	}
	return nil
}

// Allowed reports whether an agent may use an upstream.
func (r *Registry) Allowed(agentID, upstreamID string) (bool, error) {
	var n int
	err := r.store.DB().QueryRow(
		`SELECT COUNT(*) FROM grants WHERE agent_id=? AND upstream_id=?`, agentID, upstreamID,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("query grant: %w", err)
	}
	return n > 0, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/grant/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/grant
git commit -m "feat: grant registry (default-deny allow-list, policy-engine stand-in)"
```

---

### Task 8: Data-plane reverse proxy

**Files:**
- Create: `internal/proxy/proxy.go`, `internal/proxy/proxy_test.go`

**Interfaces:**
- Consumes: `*agent.Registry`, `*upstream.Registry`, `*grant.Registry`, `*secret.Vault`, `authn.For`.
- Produces:
  - `proxy.New(deps proxy.Deps) http.Handler` where
    `type Deps struct { Agents *agent.Registry; Upstreams *upstream.Registry; Grants *grant.Registry; Vault *secret.Vault; Logger *slog.Logger }`.
  - Behavior for `<METHOD> /<upstream>/<rest...>`:
    1. Vault locked → `503` `{"error":"vault locked"}`.
    2. Missing/!`Bearer ` Authorization → `401`.
    3. Token not an agent → `401`.
    4. Upstream name unknown → `404`.
    5. Grant absent → `403` `{"error":"access denied"}`.
    6. Else build target URL = `upstream.BaseURL` + `/<rest>` (+ raw query), strip the agent's `Authorization`, apply the upstream authenticator, reverse-proxy, copy status/body back.

- [ ] **Step 1: Write the failing test**

`internal/proxy/proxy_test.go`:
```go
package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/grant"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
	"github.com/Sipaha/outwall/internal/upstream"
)

func build(t *testing.T) (http.Handler, *agent.Registry, *upstream.Registry, *grant.Registry, *secret.Vault) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	v := secret.NewVault(s)
	require.NoError(t, v.Init("pw"))
	ag := agent.NewRegistry(s)
	up := upstream.NewRegistry(s, v)
	gr := grant.NewRegistry(s)
	h := New(Deps{Agents: ag, Upstreams: up, Grants: gr, Vault: v})
	return h, ag, up, gr, v
}

func do(t *testing.T, h http.Handler, method, target, token string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestProxyHappyPathInjectsAuthAndStripsAgentToken(t *testing.T) {
	var gotAuth, gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	h, ag, up, gr, _ := build(t)
	_, token, err := ag.Register("claude")
	require.NoError(t, err)
	a, err := ag.Authenticate(token)
	require.NoError(t, err)
	u, err := up.Create("be", backend.URL, upstream.AuthConfig{
		Type: "static", Header: "Authorization", Token: "Bearer upstreamtok",
	})
	require.NoError(t, err)
	require.NoError(t, gr.Add(a.ID, u.ID))

	w := do(t, h, http.MethodGet, "/be/repos/x?page=2", token)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "ok", w.Body.String())
	require.Equal(t, "Bearer upstreamtok", gotAuth) // upstream cred injected
	require.Equal(t, "/repos/x?page=2", gotPath)     // agent token NOT forwarded
}

func TestProxyGuards(t *testing.T) {
	h, ag, up, gr, v := build(t)
	_, token, _ := ag.Register("claude")
	a, _ := ag.Authenticate(token)
	u, _ := up.Create("be", "http://127.0.0.1:1", upstream.AuthConfig{Type: "none"})

	// 401 missing token
	require.Equal(t, http.StatusUnauthorized, do(t, h, http.MethodGet, "/be/x", "").Code)
	// 401 bad token
	require.Equal(t, http.StatusUnauthorized, do(t, h, http.MethodGet, "/be/x", "owa_bad").Code)
	// 404 unknown upstream
	require.Equal(t, http.StatusNotFound, do(t, h, http.MethodGet, "/nope/x", token).Code)
	// 403 default-deny (no grant yet)
	require.Equal(t, http.StatusForbidden, do(t, h, http.MethodGet, "/be/x", token).Code)
	// 503 vault locked
	require.NoError(t, gr.Add(a.ID, u.ID))
	v.Lock()
	require.Equal(t, http.StatusServiceUnavailable, do(t, h, http.MethodGet, "/be/x", token).Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/proxy/`
Expected: FAIL — `New` undefined.

- [ ] **Step 3: Write minimal implementation**

`internal/proxy/proxy.go`:
```go
// Package proxy is the data plane: a localhost reverse proxy that authenticates the
// calling agent, enforces default-deny, injects upstream credentials, and forwards.
package proxy

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/authn"
	"github.com/Sipaha/outwall/internal/grant"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/upstream"
)

// Deps are the data-plane dependencies.
type Deps struct {
	Agents    *agent.Registry
	Upstreams *upstream.Registry
	Grants    *grant.Registry
	Vault     *secret.Vault
	Logger    *slog.Logger
}

type handler struct {
	Deps
}

// New builds the data-plane HTTP handler.
func New(d Deps) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &handler{Deps: d}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Vault.Locked() {
		writeErr(w, http.StatusServiceUnavailable, "vault locked")
		return
	}

	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, "Bearer ") {
		writeErr(w, http.StatusUnauthorized, "missing bearer token")
		return
	}
	token := strings.TrimPrefix(authz, "Bearer ")
	ag, err := h.Agents.Authenticate(token)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid token")
		return
	}

	// Split "/<upstream>/<rest...>".
	trimmed := strings.TrimPrefix(r.URL.Path, "/")
	name, rest, _ := strings.Cut(trimmed, "/")
	if name == "" {
		writeErr(w, http.StatusNotFound, "no upstream in path")
		return
	}
	up, err := h.Upstreams.GetByName(name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "unknown upstream")
		return
	}

	allowed, err := h.Grants.Allowed(ag.ID, up.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "policy error")
		return
	}
	if !allowed {
		writeErr(w, http.StatusForbidden, "access denied")
		return
	}

	base, err := url.Parse(up.BaseURL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "bad upstream url")
		return
	}
	auth, err := authn.For(up.Auth)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "auth config error")
		return
	}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = base.Scheme
			pr.Out.URL.Host = base.Host
			pr.Out.Host = base.Host
			pr.Out.URL.Path = singleJoin(base.Path, rest)
			pr.Out.URL.RawQuery = r.URL.RawQuery
			pr.Out.Header.Del("Authorization") // never forward the agent's token
			if err := auth.Apply(pr.Out); err != nil {
				h.Logger.Error("apply upstream auth", "upstream", up.Name, "err", err)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			h.Logger.Error("proxy upstream", "upstream", up.Name, "err", err)
			writeErr(w, http.StatusBadGateway, "upstream error")
		},
	}
	rp.ServeHTTP(w, r)
}

func singleJoin(a, b string) string {
	a = strings.TrimSuffix(a, "/")
	b = strings.TrimPrefix(b, "/")
	if b == "" {
		if a == "" {
			return "/"
		}
		return a
	}
	return a + "/" + b
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/proxy/`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add internal/proxy
git commit -m "feat: data-plane reverse proxy with default-deny and auth injection"
```

---

### Task 9: Daemon assembly + Unix-socket admin API + CLI clients

This task wires everything into `outwall serve` (data plane on a TCP localhost port + admin API on a `0600` Unix socket) and the CLI client commands. It folds in the small `internal/client` and the cobra subcommands.

**Files:**
- Create: `internal/daemon/daemon.go`, `internal/daemon/admin.go`, `internal/daemon/admin_test.go`, `internal/client/client.go`, `internal/cli/serve.go`, `internal/cli/vault.go`, `internal/cli/upstream.go`, `internal/cli/agent.go`, `internal/cli/grant.go`
- Modify: `internal/cli/root.go` (register subcommands, add `--socket` / `--db` / `--listen` persistent flags)

**Interfaces:**
- Consumes: every registry above, `*proxy`, `*secret.Vault`.
- Produces:
  - `daemon.Config struct { DBPath, SocketPath, Listen string }`.
  - `daemon.New(cfg Config) (*daemon.Daemon, error)` — opens store, builds vault + registries + proxy.
  - `(*Daemon).AdminHandler() http.Handler` — admin API mux (tested directly via httptest).
  - `(*Daemon).Serve(ctx context.Context) error` — starts the data-plane TCP listener and the admin Unix-socket listener; blocks until ctx canceled.
  - Admin endpoints (JSON over the unix socket):
    - `POST /vault/init {password}` → `{initialized:true}`
    - `POST /vault/unlock {password}` → `{locked:false}` or 401
    - `GET  /vault/status` → `{initialized,locked}`
    - `POST /upstreams {name,base_url,auth:{...}}` → `{id}` (requires unlocked)
    - `GET  /upstreams` → `[{id,name,base_url,auth_type}]` (no secrets in output)
    - `POST /agents/register {name}` → `{id,token}` (token shown once)
    - `GET  /agents` → `[{id,name,status}]`
    - `POST /grants {agent_id,upstream_id}` → `{ok:true}`
  - `client.New(socketPath string) *client.Client` with one method `Do(method, path string, body, out any) error` (HTTP over `net.Dial("unix", ...)`).

- [ ] **Step 1: Write the failing test (admin API, in-process)**

`internal/daemon/admin_test.go`:
```go
package daemon

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func newDaemon(t *testing.T) *Daemon {
	t.Helper()
	d, err := New(Config{
		DBPath:     filepath.Join(t.TempDir(), "d.db"),
		SocketPath: filepath.Join(t.TempDir(), "d.sock"),
		Listen:     "127.0.0.1:0",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func req(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAdminVaultAndUpstreamFlow(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()

	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)

	// Wrong password on unlock → 401.
	d.vault.Lock()
	require.Equal(t, http.StatusUnauthorized, req(t, h, "POST", "/vault/unlock", `{"password":"no"}`).Code)
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/unlock", `{"password":"pw"}`).Code)

	// Create upstream.
	w := req(t, h, "POST", "/upstreams",
		`{"name":"gh","base_url":"https://api.github.com","auth":{"type":"static","header":"Authorization","token":"Bearer x"}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// List upstreams must not leak the secret token.
	wl := req(t, h, "GET", "/upstreams", "")
	require.Equal(t, http.StatusOK, wl.Code)
	require.NotContains(t, wl.Body.String(), "Bearer x")

	// Register agent returns a token.
	wa := req(t, h, "POST", "/agents/register", `{"name":"claude"}`)
	require.Equal(t, http.StatusOK, wa.Code)
	require.Contains(t, wa.Body.String(), "owa_")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/`
Expected: FAIL — `New` / `Daemon` undefined.

- [ ] **Step 3: Write minimal implementation**

`internal/daemon/daemon.go`:
```go
// Package daemon wires the store, vault, registries, and data-plane proxy together
// and serves the data plane (TCP localhost) plus an admin API (unix socket).
package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/grant"
	"github.com/Sipaha/outwall/internal/proxy"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
	"github.com/Sipaha/outwall/internal/upstream"
)

// Config holds daemon paths/addresses.
type Config struct {
	DBPath     string
	SocketPath string
	Listen     string // data-plane TCP listen address, e.g. 127.0.0.1:8080
}

// Daemon owns the running gateway.
type Daemon struct {
	cfg       Config
	store     *store.Store
	vault     *secret.Vault
	agents    *agent.Registry
	upstreams *upstream.Registry
	grants    *grant.Registry
	dataPlane http.Handler
}

// New constructs a Daemon (does not start listeners).
func New(cfg Config) (*Daemon, error) {
	s, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	v := secret.NewVault(s)
	ag := agent.NewRegistry(s)
	up := upstream.NewRegistry(s, v)
	gr := grant.NewRegistry(s)
	d := &Daemon{
		cfg: cfg, store: s, vault: v, agents: ag, upstreams: up, grants: gr,
		dataPlane: proxy.New(proxy.Deps{Agents: ag, Upstreams: up, Grants: gr, Vault: v}),
	}
	return d, nil
}

// Close releases resources.
func (d *Daemon) Close() error { return d.store.Close() }

// Serve starts the data-plane and admin listeners until ctx is canceled.
func (d *Daemon) Serve(ctx context.Context) error {
	_ = os.Remove(d.cfg.SocketPath)
	ln, err := net.Listen("unix", d.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen unix: %w", err)
	}
	if err := os.Chmod(d.cfg.SocketPath, 0o600); err != nil {
		return fmt.Errorf("chmod socket: %w", err)
	}
	adminSrv := &http.Server{Handler: d.AdminHandler()}
	dataSrv := &http.Server{Addr: d.cfg.Listen, Handler: d.dataPlane}

	errc := make(chan error, 2)
	go func() { errc <- adminSrv.Serve(ln) }()
	go func() { errc <- dataSrv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		_ = adminSrv.Close()
		_ = dataSrv.Close()
		_ = os.Remove(d.cfg.SocketPath)
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
```

`internal/daemon/admin.go`:
```go
package daemon

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/upstream"
)

// AdminHandler builds the admin API mux (served over the unix socket).
func (d *Daemon) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /vault/init", d.hVaultInit)
	mux.HandleFunc("POST /vault/unlock", d.hVaultUnlock)
	mux.HandleFunc("GET /vault/status", d.hVaultStatus)
	mux.HandleFunc("POST /upstreams", d.hUpstreamCreate)
	mux.HandleFunc("GET /upstreams", d.hUpstreamList)
	mux.HandleFunc("POST /agents/register", d.hAgentRegister)
	mux.HandleFunc("GET /agents", d.hAgentList)
	mux.HandleFunc("POST /grants", d.hGrantAdd)
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func adminErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func decode(r *http.Request, v any) error { return json.NewDecoder(r.Body).Decode(v) }

func (d *Daemon) hVaultInit(w http.ResponseWriter, r *http.Request) {
	var body struct{ Password string `json:"password"` }
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if err := d.vault.Init(body.Password); err != nil {
		adminErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"initialized": true})
}

func (d *Daemon) hVaultUnlock(w http.ResponseWriter, r *http.Request) {
	var body struct{ Password string `json:"password"` }
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	switch err := d.vault.Unlock(body.Password); {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]bool{"locked": false})
	case errors.Is(err, secret.ErrBadPassword):
		adminErr(w, http.StatusUnauthorized, "incorrect master password")
	default:
		adminErr(w, http.StatusBadRequest, err.Error())
	}
}

func (d *Daemon) hVaultStatus(w http.ResponseWriter, _ *http.Request) {
	init, err := d.vault.Initialized()
	if err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"initialized": init, "locked": d.vault.Locked()})
}

func (d *Daemon) hUpstreamCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string              `json:"name"`
		BaseURL string              `json:"base_url"`
		Auth    upstream.AuthConfig `json:"auth"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	up, err := d.upstreams.Create(body.Name, body.BaseURL, body.Auth)
	if err != nil {
		adminErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": up.ID})
}

func (d *Daemon) hUpstreamList(w http.ResponseWriter, _ *http.Request) {
	ups, err := d.upstreams.List()
	if err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]string, 0, len(ups))
	for _, u := range ups {
		out = append(out, map[string]string{
			"id": u.ID, "name": u.Name, "base_url": u.BaseURL, "auth_type": u.AuthType,
		}) // secrets intentionally omitted
	}
	writeJSON(w, http.StatusOK, out)
}

func (d *Daemon) hAgentRegister(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name string `json:"name"` }
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	a, token, err := d.agents.Register(body.Name)
	if err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": a.ID, "token": token})
}

func (d *Daemon) hAgentList(w http.ResponseWriter, _ *http.Request) {
	ags, err := d.agents.List()
	if err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]string, 0, len(ags))
	for _, a := range ags {
		out = append(out, map[string]string{"id": a.ID, "name": a.Name, "status": a.Status})
	}
	writeJSON(w, http.StatusOK, out)
}

func (d *Daemon) hGrantAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AgentID    string `json:"agent_id"`
		UpstreamID string `json:"upstream_id"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if err := d.grants.Add(body.AgentID, body.UpstreamID); err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/daemon/`
Expected: PASS.

- [ ] **Step 5: Add the unix-socket client + CLI commands**

`internal/client/client.go`:
```go
// Package client is a thin HTTP-over-unix-socket client for the admin API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
)

// Client talks to the daemon admin API over a unix socket.
type Client struct {
	http *http.Client
}

// New constructs a client bound to socketPath.
func New(socketPath string) *Client {
	return &Client{http: &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
	}}
}

// Do sends a request; body and out may be nil. Non-2xx returns the server error message.
func (c *Client) Do(method, path string, body, out any) error {
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
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call daemon (is it running?): %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var e struct{ Error string `json:"error"` }
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

`internal/cli/root.go` (replace body to add flags + subcommands):
```go
// Package cli defines the outwall command tree.
package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Sipaha/outwall/internal/client"
	"github.com/Sipaha/outwall/internal/version"
)

type globalFlags struct {
	socket string
	db     string
	listen string
}

func defaultDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "outwall")
	}
	return ".outwall"
}

// NewRootCmd builds the root cobra command.
func NewRootCmd() *cobra.Command {
	gf := &globalFlags{}
	root := &cobra.Command{
		Use:           "outwall",
		Short:         "Authenticating egress gateway for AI agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.String(),
	}
	dir := defaultDir()
	root.PersistentFlags().StringVar(&gf.socket, "socket", filepath.Join(dir, "outwall.sock"), "admin unix socket path")
	root.PersistentFlags().StringVar(&gf.db, "db", filepath.Join(dir, "outwall.db"), "database path")
	root.PersistentFlags().StringVar(&gf.listen, "listen", "127.0.0.1:8080", "data-plane listen address")

	root.AddCommand(
		newServeCmd(gf),
		newVaultCmd(gf),
		newUpstreamCmd(gf),
		newAgentCmd(gf),
		newGrantCmd(gf),
	)
	return root
}

func newClient(gf *globalFlags) *client.Client { return client.New(gf.socket) }
```

`internal/cli/serve.go`:
```go
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Sipaha/outwall/internal/daemon"
)

func newServeCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the outwall daemon (data plane + admin socket)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := os.MkdirAll(filepath.Dir(gf.db), 0o700); err != nil {
				return fmt.Errorf("create data dir: %w", err)
			}
			d, err := daemon.New(daemon.Config{DBPath: gf.db, SocketPath: gf.socket, Listen: gf.listen})
			if err != nil {
				return err
			}
			defer d.Close()
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			fmt.Fprintf(cmd.OutOrStdout(), "outwall serving: data plane %s, admin %s\n", gf.listen, gf.socket)
			return d.Serve(ctx)
		},
	}
}
```

`internal/cli/vault.go`:
```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func promptPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	b, err := term.ReadPassword(0)
	fmt.Println()
	return string(b), err
}

func newVaultCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "vault", Short: "Manage the master-password vault"}

	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Initialize the vault with a master password",
		RunE: func(c *cobra.Command, _ []string) error {
			pw, err := promptPassword("New master password: ")
			if err != nil {
				return err
			}
			return newClient(gf).Do("POST", "/vault/init", map[string]string{"password": pw}, nil)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "unlock",
		Short: "Unlock the vault",
		RunE: func(c *cobra.Command, _ []string) error {
			pw, err := promptPassword("Master password: ")
			if err != nil {
				return err
			}
			return newClient(gf).Do("POST", "/vault/unlock", map[string]string{"password": pw}, nil)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show vault status",
		RunE: func(c *cobra.Command, _ []string) error {
			var out map[string]bool
			if err := newClient(gf).Do("GET", "/vault/status", nil, &out); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "initialized=%v locked=%v\n", out["initialized"], out["locked"])
			return nil
		},
	})
	return cmd
}
```

`internal/cli/upstream.go`:
```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Sipaha/outwall/internal/upstream"
)

func newUpstreamCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "upstream", Short: "Manage upstreams"}

	var (
		baseURL, authType, header, token, username, password string
	)
	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Add an upstream",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			req := map[string]any{
				"name":     args[0],
				"base_url": baseURL,
				"auth": upstream.AuthConfig{
					Type: authType, Header: header, Token: token,
					Username: username, Password: password,
				},
			}
			var out map[string]string
			if err := newClient(gf).Do("POST", "/upstreams", req, &out); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "created upstream %s (id=%s)\n", args[0], out["id"])
			return nil
		},
	}
	add.Flags().StringVar(&baseURL, "base-url", "", "upstream base URL (required)")
	add.Flags().StringVar(&authType, "auth", "none", "auth type: none|static|basic")
	add.Flags().StringVar(&header, "header", "", "header name for static auth")
	add.Flags().StringVar(&token, "token", "", "header value for static auth")
	add.Flags().StringVar(&username, "username", "", "basic auth username")
	add.Flags().StringVar(&password, "password", "", "basic auth password")
	_ = add.MarkFlagRequired("base-url")

	list := &cobra.Command{
		Use:   "list",
		Short: "List upstreams",
		RunE: func(c *cobra.Command, _ []string) error {
			var out []map[string]string
			if err := newClient(gf).Do("GET", "/upstreams", nil, &out); err != nil {
				return err
			}
			for _, u := range out {
				fmt.Fprintf(c.OutOrStdout(), "%s\t%s\t%s\t%s\n", u["id"], u["name"], u["auth_type"], u["base_url"])
			}
			return nil
		},
	}
	cmd.AddCommand(add, list)
	return cmd
}
```

`internal/cli/agent.go`:
```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newAgentCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "agent", Short: "Manage agents"}

	cmd.AddCommand(&cobra.Command{
		Use:   "register <name>",
		Short: "Register an agent and print its token (shown once)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			var out map[string]string
			if err := newClient(gf).Do("POST", "/agents/register", map[string]string{"name": args[0]}, &out); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "agent id=%s\ntoken=%s\n", out["id"], out["token"])
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List agents",
		RunE: func(c *cobra.Command, _ []string) error {
			var out []map[string]string
			if err := newClient(gf).Do("GET", "/agents", nil, &out); err != nil {
				return err
			}
			for _, a := range out {
				fmt.Fprintf(c.OutOrStdout(), "%s\t%s\t%s\n", a["id"], a["name"], a["status"])
			}
			return nil
		},
	})
	return cmd
}
```

`internal/cli/grant.go`:
```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newGrantCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "grant <agent_id> <upstream_id>",
		Short: "Grant an agent access to an upstream",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			if err := newClient(gf).Do("POST", "/grants",
				map[string]string{"agent_id": args[0], "upstream_id": args[1]}, nil); err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), "granted")
			return nil
		},
	}
}
```

- [ ] **Step 6: Build, vet, and run the full end-to-end smoke test**

Run:
```bash
go mod tidy && go build ./... && go vet ./... && go test ./...
make build
# smoke (manual): start daemon, init vault, define upstream, register agent, grant, curl through proxy
DIR=$(mktemp -d)
./dist/bin/outwall --db $DIR/o.db --socket $DIR/o.sock --listen 127.0.0.1:8099 serve & SRV=$!
sleep 1
printf 'pw\n' | ./dist/bin/outwall --socket $DIR/o.sock vault init
./dist/bin/outwall --socket $DIR/o.sock upstream add httpbin --base-url https://httpbin.org --auth none
AID=$(./dist/bin/outwall --socket $DIR/o.sock agent register claude | sed -n 's/^agent id=//p')
TOK=$(./dist/bin/outwall --socket $DIR/o.sock agent register claude2 | sed -n 's/^token=//p')
# grab the second agent's id+token cleanly:
OUT=$(./dist/bin/outwall --socket $DIR/o.sock agent register tester); AID=$(echo "$OUT"|sed -n 's/^agent id=//p'); TOK=$(echo "$OUT"|sed -n 's/^token=//p')
UID=$(./dist/bin/outwall --socket $DIR/o.sock upstream list | awk '/httpbin/{print $1}')
./dist/bin/outwall --socket $DIR/o.sock grant $AID $UID
curl -s -H "Authorization: Bearer $TOK" http://127.0.0.1:8099/httpbin/get | head -c 200
kill $SRV
```
Expected: `go test ./...` all PASS; the `curl` prints an httpbin JSON response (proxy reached the upstream). Without the grant, the same curl returns `{"error":"access denied"}` (403).

- [ ] **Step 7: Commit**

```bash
git add internal/daemon internal/client internal/cli go.mod go.sum
git commit -m "feat: daemon serve, unix-socket admin API, and CLI clients"
```

---

## Self-Review (done while writing)

- **Spec coverage (Plan 1 slice):** vault/master-password (Task 3) ✓; encrypted upstream secrets (Task 4) ✓; dynamic agent registration + tokens (Task 5) ✓; default-deny data plane with auth injection (Tasks 6–8) ✓; vault-locked ⇒ data plane halted with 503 (Task 8 test) ✓; single Go binary + SQLite no-CGO + unix socket 0600 (Tasks 1,2,9) ✓. **Deferred to later plans (by design):** MCP control plane (Plan 3), policy engine with method/path/rate-limit/require-approval (Plan 2 — `grant` is the stand-in), OIDC (Plan 2), audit (Plan 4), web UI (Plan 6), Wails wrapper (Plan 7).
- **Placeholder scan:** no TBD/TODO; every code step has complete code.
- **Type consistency:** `proxy.Deps`, `daemon.Config`, `upstream.AuthConfig`, `agent.Register` signatures match across the daemon wiring and tests; `authn.For(upstream.AuthConfig)` is the single seam used by the proxy.

## Roadmap — remaining Phase 1 milestones (each its own plan, written when we get there)

- **Plan 2 — Policy engine + approval + OIDC-CC + rate limit.** Replace `internal/grant.Allowed` with `internal/policy` (rules: subject×upstream×method×path-glob×rate-limit → allow/deny/require-approval); `internal/approval` blocking queue surfaced on the admin API; `authn` gains `oidc-client-credentials` (token cache+refresh) behind the existing `For` seam.
- **Plan 3 — MCP control plane.** Streamable-HTTP MCP server exposing `list_upstreams`, `request_access(host, purpose)`, `get_access`, `whoami`; dynamic agent self-registration via MCP; wires to policy + approval.
- **Plan 4 — Audit.** Request journal + body store (≤256 KB, non-text metadata-only, injected-cred masking), retention/prune; data-plane middleware records every request/response.
- **Plan 5 — Daemon control API + SSE.** Consolidate UI-facing endpoints; SSE stream for new agents / approval queue / audit tail.
- **Plan 6 — Web UI.** React 19 + Vite + Tailwind 4 + Zustand screens: Unlock, Dashboard, Upstreams, Agent detail, Policies, Approvals, Audit, Settings; embedded via `go:embed`.
- **Plan 7 — Wails 3 desktop wrapper.** Thin-wrapper supervises the daemon, renders the embedded UI over the unix socket; master-password unlock screen at launch.
