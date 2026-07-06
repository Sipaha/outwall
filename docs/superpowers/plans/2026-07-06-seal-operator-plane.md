# Seal Operator Plane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the same-user self-approval hole. Today every privileged operator mutation is reachable by the agent, which runs as the SAME OS user as the operator, over two transports with no real barrier:

1. the CSRF-free unix socket `~/.spk/outwall/outwall.sock` (`AdminHandler()` is a bare `apiMux()` — no gate), and
2. TCP `127.0.0.1:8182/api` behind only a STATIC `X-Outwall-CSRF: '1'` header (`csrfMiddleware` rejects only when the header is *empty*).

So `curl --unix-socket ~/.spk/outwall/outwall.sock -XPOST http://unix/approvals/<id>/resolve -d '{"approve":true}'` self-approves a pending request, and `curl -H 'X-Outwall-CSRF: x' http://127.0.0.1:8182/api/rules -d '{…allow…}'` self-grants a rule. `0600` + loopback do not separate two processes of the same user; the upstream credential never enters the agent context, but its *effect* (authenticated, un-approved egress) does. This plan gates **all privileged mutations behind an operator session unlocked by the master password** — a secret the agent does not hold — on **BOTH transports**, and retires the static-CSRF model.

This plan implements the "Operator session + route split" part of the spec (`docs/superpowers/specs/2026-07-06-mcp-to-direct-socket-design.md`, requirements **R5, R6, R7-partial, R8**) plus the threat-model ADR. It BUILDS ON but is INDEPENDENT of Plan 1 (agent socket + MCP removal): the files here (`internal/secret/vault.go`, a new `internal/opsession` package, `internal/daemon/admin.go`, `internal/cli`, `web/`) do not touch or depend on Plan 1's new packages. Assume Plan 1 may or may not be merged.

**Scope decision — deliberate, reasoned deviation from spec R7 (state it, record it in the ADR):** Spec R7 says desktop delivery of privileged verbs should move to **Wails native bindings** (so there is no `curl`-able HTTP endpoint at all on the desktop). The codebase currently has **NO project-authored Wails bindings** — the entire UI talks HTTP over `:8182/api` for both desktop-webview and headless. The security boundary that actually holds in every mode is the **master-password operator-session gate**, which works identically for desktop-webview-over-loopback and headless. Therefore **this plan delivers the master-password gate over HTTP for BOTH transports** (fully closing the self-approval hole), and the Wails-native-bindings migration — removing the HTTP endpoint entirely as *defense-in-depth* — is **DEFERRED to a future "Plan 3"**. No Wails bindings are implemented here. This deviation is recorded in the new ADR (0041) and flagged as the R7 remainder.

**Second deviation (D2), also recorded in the ADR:** the spec lists `POST /vault/init` under the gated routes. But `init` is the bootstrap that *establishes* the master password — before it runs there is no password and hence no operator session is possible, so gating it deadlocks first-run. Root-cause-correct design: `POST /vault/init` is **ungated** (it protects nothing — the vault is empty and locked) and **opens the operator session on success** (the operator just set the password, they are present). Every OTHER vault/privileged route stays gated. This is the one route where the spec's list can't be honored literally; the ADR explains why.

**Architecture:** Three seams.

1. **`internal/secret` — `Vault.Verify(password) error`**: re-verifies the master password against the persisted Argon2id salt/verifier **without** touching the vault's locked/unlocked state (never sets or clears the resident key). Same read+derive+open+compare as `Unlock` minus `v.mu.Lock(); v.key = key`. This lets the operator session confirm identity while the data plane keeps serving (and works while the vault is *locked*, which is what makes the existing-vault bootstrap possible).
2. **`internal/opsession` — the operator-session holder**: a small in-daemon `*Session` with an idle-TTL sliding window (`DefaultTTL = time.Hour`), an injected clock for tests, and `Open`/`Lock`/`Authorized`/`Status`. The daemon holds exactly one.
3. **`internal/daemon` — route split + gate on both transports**: `apiMux()` splits into an UNGATED group (read-only GETs, SSE, dry-run preview, launcher focus, and the new `/operator/session/*` control routes — the master-password entry) and a GATED group (every privileged mutation) wrapped per-route by `operatorGate`. Because both `AdminHandler()` (unix socket) and `UIHandler()` (`/api`) build from `apiMux()`, per-route wrapping gates BOTH transports uniformly. `csrfMiddleware` + `X-Outwall-CSRF` are removed — the operator gate replaces the CSRF-not-auth model.

The CLI gains a sudo-style helper (prompt for the master password on the TTY, open the session, retry once) for the operator's privileged commands; read-only commands are unchanged. The web UI drops the static CSRF header, gains operator-session helpers + a light master-password prompt driven off the 403 gate response, and an idle/"Lock now" control.

No schema change (the session is in-memory).

**Tech Stack:** Go (CGO-free server), `database/sql` + `modernc.org/sqlite` (pure-Go), `golang.org/x/crypto/argon2`, `golang.org/x/term` (already a dep, `go.mod:13`), `stretchr/testify`; React + TypeScript + zustand + vitest for `web/`.

## Global Constraints

- Module path is exactly `github.com/Sipaha/outwall` — in every import.
- No CGO in the server binary (`CGO_ENABLED=0`); SQLite via `modernc.org/sqlite`. The desktop Wails wrapper is the only CGO target.
- No panics / `log.Fatal` in library code — return `error` wrapped with `%w`. Panics only in `main`/tests.
- `log/slog` for logging; `stretchr/testify` for tests; `gofmt` (tabs); `go vet` clean.
- Add new deps at latest (`go get <mod>@latest`); do NOT bump existing deps. (This plan adds none — `golang.org/x/term` is already present.)
- **No `citeck`** strings / imports / branding in the code touched here (all core: `secret`, `opsession`, `daemon`, `cli`, `web`). The pre-existing sanctioned citeck blank-import + `"citeck"` profile-name in daemon *tests* stays as-is; add nothing new.
- TDD: write the failing test → run it and SEE it fail → minimal code → run it and SEE it pass → commit. Frequent small commits.
- Before each commit run the gate and fix anything that fails (never ignore):

  ```bash
  make fmt && make vet && make test
  ```

  For web-only steps also run the web unit tests: `cd web && npx vitest run`.
- Commits: no `Co-Authored-By`; no `git commit --amend`; new commits only. Branch `main`.
- Alpha: no storage-format compat to preserve — but this plan needs no schema change, so no DB reset.

## File structure

- **Modify** `internal/secret/vault.go` — add `Vault.Verify`.
- **Modify** `internal/secret/vault_test.go` — `TestVerify`.
- **Create** `internal/opsession/opsession.go` — `Session`, `New`, `Open`, `Lock`, `Authorized`, `Status`, `DefaultTTL`.
- **Create** `internal/opsession/opsession_test.go` — lifecycle + status + default-ttl tests (injected clock).
- **Modify** `internal/daemon/daemon.go` — `opsession *opsession.Session` field + wiring in `New`.
- **Modify** `internal/daemon/admin.go` — the three `/operator/session/*` handlers, `operatorGate`, the `apiMux` route split, `UIHandler` (drop csrf), delete `csrfMiddleware`, `hVaultInit` opens the session.
- **Modify** `internal/daemon/admin_test.go` — new gate/session/bootstrap tests; remove/replace the two CSRF tests.
- **Modify** `internal/cli/root.go` + a new `internal/cli/session.go` — `doPrivileged` sudo-style helper + `isSessionRequired`.
- **Modify** `internal/cli/approval.go`, `internal/cli/vault.go`, and the remaining privileged commands (`rule.go`, `upstream.go`, `cluster.go`, `audit.go`, `access.go`) — route privileged calls through the helper.
- **Create** `internal/cli/session_test.go` — `isSessionRequired` + a socket-backed retry test.
- **Modify** `web/src/lib/api.ts` — drop `CSRF_HEADER`; add operator-session helpers + `setSessionRequiredHandler`.
- **Modify** `web/src/lib/api.test.ts` — assert no `X-Outwall-CSRF`; assert `openOperatorSession` posts `/operator/session/open`.
- **Create** `web/src/lib/operatorSession.ts` + `web/src/components/OperatorSessionModal.tsx`; **modify** `web/src/App.tsx` — light prompt + "Lock now" wiring.
- **Create** `docs/architecture/decisions/0041-operator-session-master-password.md`; **modify** `docs/architecture/decisions/0005-control-api-sse.md` (amend note), `docs/architecture/overview.md`, `docs/INDEX.md`.

---

### Task 1: `secret.Verify` — master-password re-verification without unlocking

**Files:**
- Modify: `internal/secret/vault.go` (mirror `Unlock`, lines 92–111)
- Test: `internal/secret/vault_test.go`

**Interfaces:**
- Produces: `func (v *Vault) Verify(password string) error` — checks the master password against the persisted `vault_meta` salt/verifier and returns nil / `ErrBadPassword` / `ErrNotInitialized`, **without** changing `Locked()`.

- [ ] **Step 1: Write the failing test** — add to `internal/secret/vault_test.go`:

```go
func TestVerify(t *testing.T) {
	v := newVault(t)

	// Not initialized yet → ErrNotInitialized.
	require.ErrorIs(t, v.Verify("pw"), ErrNotInitialized)

	require.NoError(t, v.Init("correct horse"))
	require.False(t, v.Locked()) // Init leaves it unlocked.

	// Correct password verifies and leaves the vault UNLOCKED (state unchanged).
	require.NoError(t, v.Verify("correct horse"))
	require.False(t, v.Locked())

	// Wrong password → ErrBadPassword; state still unlocked.
	require.ErrorIs(t, v.Verify("nope"), ErrBadPassword)
	require.False(t, v.Locked())

	// While LOCKED, Verify still works (it needs no resident key) and leaves it LOCKED.
	v.Lock()
	require.True(t, v.Locked())
	require.NoError(t, v.Verify("correct horse"))
	require.True(t, v.Locked(), "Verify must NOT unlock a locked vault")
	require.ErrorIs(t, v.Verify("nope"), ErrBadPassword)
	require.True(t, v.Locked())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/secret/ -run TestVerify -v`
Expected: FAIL — `v.Verify undefined`.

- [ ] **Step 3: Write minimal implementation** — in `internal/secret/vault.go`, add directly after `Unlock` (after line 111):

```go
// Verify checks the master password against the stored Argon2id salt/verifier WITHOUT changing the
// vault's locked/unlocked state (it never sets or clears the resident key). The operator session
// uses it to confirm the operator's identity while the data plane keeps serving — and it works even
// while the vault is locked (it derives from the DB, not the resident key). Returns ErrNotInitialized
// when no vault exists and ErrBadPassword when the password is wrong (identical to Unlock).
func (v *Vault) Verify(password string) error {
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
	return nil
}
```

> `deriveKey`, `openWith`, `verifierPlaintext`, `ErrBadPassword`, `ErrNotInitialized` are all unexported/in-package, so `Verify` lives in package `secret`. No new imports (`database/sql`, `errors`, `fmt` are already imported).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/secret/ -race`
Expected: PASS (the new test + `TestInitUnlockRoundTrip`).

- [ ] **Step 5: Commit**

```bash
make fmt
git add internal/secret/
git commit -m "feat(secret): Vault.Verify re-checks master password without unlocking"
```

---

### Task 2: `internal/opsession` — the operator-session holder

**Files:**
- Create: `internal/opsession/opsession.go`
- Test: `internal/opsession/opsession_test.go`

**Interfaces:**
- Produces: `type Session`; `New(ttl time.Duration) *Session`; `(*Session).Open()`, `.Lock()`, `.Authorized() bool` (sliding idle window; refreshes on true), `.Status() (open bool, idleRemaining time.Duration)`; `const DefaultTTL = time.Hour`. Clock is an injectable unexported `now func() time.Time` field (default `time.Now`) — logic never calls `time.Now` directly.

- [ ] **Step 1: Write the failing test** — create `internal/opsession/opsession_test.go`:

```go
package opsession

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionLifecycle(t *testing.T) {
	now := time.Unix(1000, 0)
	s := New(time.Hour)
	s.now = func() time.Time { return now }

	require.False(t, s.Authorized()) // closed before Open

	s.Open()
	require.True(t, s.Authorized()) // authorized right after Open

	// Sliding window: a call just under the TTL refreshes lastUsed, so a further sub-TTL gap
	// still authorizes (idle time is measured from the LAST authorized call, not from Open).
	now = now.Add(59 * time.Minute)
	require.True(t, s.Authorized())
	now = now.Add(59 * time.Minute)
	require.True(t, s.Authorized(), "each Authorized() call slides the idle window")

	// A full TTL of idleness after the last call → not authorized.
	now = now.Add(time.Hour)
	require.False(t, s.Authorized())

	// Re-open, then Lock → not authorized.
	s.Open()
	require.True(t, s.Authorized())
	s.Lock()
	require.False(t, s.Authorized())
}

func TestSessionStatusDoesNotSlide(t *testing.T) {
	now := time.Unix(1000, 0)
	s := New(time.Hour)
	s.now = func() time.Time { return now }

	open, rem := s.Status()
	require.False(t, open)
	require.Zero(t, rem)

	s.Open()
	open, rem = s.Status()
	require.True(t, open)
	require.Equal(t, time.Hour, rem)

	// Status is a read-only peek: it must NOT refresh lastUsed, so remaining shrinks over time.
	now = now.Add(30 * time.Minute)
	open, rem = s.Status()
	require.True(t, open)
	require.Equal(t, 30*time.Minute, rem)

	// Past the TTL, Status reports closed.
	now = now.Add(31 * time.Minute)
	open, rem = s.Status()
	require.False(t, open)
	require.Zero(t, rem)
}

func TestDefaultTTL(t *testing.T) {
	require.Equal(t, time.Hour, DefaultTTL)
	require.Equal(t, DefaultTTL, New(0).ttl, "a non-positive ttl falls back to DefaultTTL")
	require.Equal(t, DefaultTTL, New(-time.Second).ttl)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/opsession/ -v`
Expected: FAIL — package/`Session`/`New` do not exist (build error).

- [ ] **Step 3: Write minimal implementation** — create `internal/opsession/opsession.go`:

```go
// Package opsession holds the operator session: a short-lived, master-password-gated window during
// which privileged operator mutations are permitted. It is deliberately SEPARATE from the vault's
// locked/unlocked state — the data plane keeps serving while the operator session is closed, and an
// idle-expiry / "Lock now" of the operator session does NOT lock the vault. The daemon holds exactly
// one *Session; a closed or idle-expired session makes every gated route return 403 until the
// operator re-enters the master password.
package opsession

import (
	"sync"
	"time"
)

// DefaultTTL is the default idle timeout of an operator session (sliding window).
const DefaultTTL = time.Hour

// Session is a single operator session guarded by an idle TTL. All methods are safe for concurrent
// use. The clock is injectable (now) so tests drive expiry deterministically without sleeping.
type Session struct {
	mu       sync.Mutex
	open     bool
	openedAt time.Time
	lastUsed time.Time
	ttl      time.Duration
	now      func() time.Time
}

// New returns a closed session with the given idle TTL. A non-positive ttl falls back to DefaultTTL.
func New(ttl time.Duration) *Session {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Session{ttl: ttl, now: time.Now}
}

// Open marks the session open, starting the idle window now.
func (s *Session) Open() {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now()
	s.open = true
	s.openedAt = t
	s.lastUsed = t
}

// Lock closes the session immediately.
func (s *Session) Lock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reset()
}

// Authorized reports whether the session is open AND within the idle TTL. On success it slides the
// idle window (refreshes lastUsed), so activity keeps the session alive; on expiry it closes the
// session and returns false.
func (s *Session) Authorized() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.open {
		return false
	}
	t := s.now()
	if t.Sub(s.lastUsed) >= s.ttl {
		s.reset()
		return false
	}
	s.lastUsed = t
	return true
}

// Status returns whether the session is open and the idle time remaining (0 when closed/expired). It
// is a read-only peek: it does NOT slide the window, so polling status can never keep a session alive.
func (s *Session) Status() (open bool, idleRemaining time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.open {
		return false, 0
	}
	rem := s.ttl - s.now().Sub(s.lastUsed)
	if rem <= 0 {
		return false, 0
	}
	return true, rem
}

// reset clears session state. Caller must hold s.mu.
func (s *Session) reset() {
	s.open = false
	s.openedAt = time.Time{}
	s.lastUsed = time.Time{}
}
```

> `openedAt` is retained for a future "opened at" display / audit and to keep the state coherent; `Status` intentionally reports off `lastUsed` (idle window), not `openedAt`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/opsession/ -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
make fmt
git add internal/opsession/
git commit -m "feat(opsession): operator-session holder with idle-TTL sliding window"
```

---

### Task 3: daemon — opsession field + wiring + the three `/operator/session/*` routes

**Files:**
- Modify: `internal/daemon/daemon.go` (`Daemon` struct ~91–110; `New` ~171–183)
- Modify: `internal/daemon/admin.go` (add the three handlers; register them ungated — done fully in Task 4's `apiMux` refactor, but register here so they exist)
- Test: `internal/daemon/admin_test.go`

**Interfaces:**
- Produces: `Daemon.opsession *opsession.Session` (wired to `opsession.New(opsession.DefaultTTL)` in `New`); handlers `hOperatorSessionOpen`, `hOperatorSessionLock`, `hOperatorSessionStatus`; routes `POST /operator/session/open`, `POST /operator/session/lock`, `GET /operator/session/status` (all UNGATED — they are the master-password entry point).
- Consumes: `secret.Vault.Verify` (Task 1), `opsession.Session` (Task 2).

> This task adds the field, the wiring, and the three session-control handlers, and registers them on `apiMux`. The full gated/ungated split of the *other* routes is Task 4. Doing session-control first means Task 4's tests can open/close the session.

- [ ] **Step 1: Write the failing test** — add to `internal/daemon/admin_test.go`:

```go
func TestOperatorSessionControlRoutes(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()

	// Fresh vault (uninitialized): opening a session → 400 "vault not initialized".
	require.Equal(t, http.StatusBadRequest, req(t, h, "POST", "/operator/session/open", `{"password":"pw"}`).Code)

	// Init (ungated bootstrap) sets the master password AND opens the session.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	ws := req(t, h, "GET", "/operator/session/status", "")
	require.Equal(t, http.StatusOK, ws.Code)
	require.Contains(t, ws.Body.String(), `"open":true`)

	// Lock closes it.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/operator/session/lock", "").Code)
	require.Contains(t, req(t, h, "GET", "/operator/session/status", "").Body.String(), `"open":false`)

	// Wrong password → 401 and it stays closed.
	require.Equal(t, http.StatusUnauthorized, req(t, h, "POST", "/operator/session/open", `{"password":"no"}`).Code)
	require.Contains(t, req(t, h, "GET", "/operator/session/status", "").Body.String(), `"open":false`)

	// Correct password → 200, open, with a positive idle_remaining_seconds.
	wo := req(t, h, "POST", "/operator/session/open", `{"password":"pw"}`)
	require.Equal(t, http.StatusOK, wo.Code)
	var st map[string]any
	require.NoError(t, json.Unmarshal(wo.Body.Bytes(), &st))
	require.Equal(t, true, st["open"])
	require.Greater(t, st["idle_remaining_seconds"].(float64), float64(0))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run TestOperatorSessionControlRoutes -v`
Expected: FAIL — routes 404 (`d.opsession` / handlers don't exist yet). Note the test also requires `hVaultInit` to open the session (implemented in this task).

- [ ] **Step 3: Write minimal implementation**

In `internal/daemon/daemon.go`, add the import (keep the block sorted):

```go
	"github.com/Sipaha/outwall/internal/opsession"
```

Add the field to the `Daemon` struct (after `oauthLogins`):

```go
	opsession   *opsession.Session
```

In `New`, add to the `d := &Daemon{...}` literal (after `oauthLogins: newOAuthLogins(),`):

```go
		opsession:   opsession.New(opsession.DefaultTTL),
```

In `internal/daemon/admin.go`, make `hVaultInit` open the operator session on success. Insert `d.opsession.Open()` right after the successful `d.vault.Init` (before `d.publish("vault.unlocked", …)`):

```go
	if err := d.vault.Init(body.Password); err != nil {
		adminErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Init is the bootstrap that ESTABLISHES the master password, so it cannot itself sit behind
	// the operator-session gate (there is no password yet). The operator just set the password and
	// is present, so open their session here — subsequent gated calls need no immediate re-prompt.
	d.opsession.Open()
```

Add the three session-control handlers to `admin.go` (near `hVaultStatus`):

```go
// hOperatorSessionOpen verifies the master password (WITHOUT unlocking the vault — the data plane is
// unaffected) and opens the operator session. This route is UNGATED: it IS the master-password entry
// point that authorizes the gated routes. Wrong password → 401; no vault yet → 400.
func (d *Daemon) hOperatorSessionOpen(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	switch err := d.vault.Verify(body.Password); {
	case err == nil:
		d.opsession.Open()
		open, idle := d.opsession.Status()
		writeJSON(w, http.StatusOK, map[string]any{"open": open, "idle_remaining_seconds": int(idle.Seconds())})
	case errors.Is(err, secret.ErrBadPassword):
		adminErr(w, http.StatusUnauthorized, "incorrect master password")
	case errors.Is(err, secret.ErrNotInitialized):
		adminErr(w, http.StatusBadRequest, "vault not initialized")
	default:
		adminErr(w, http.StatusInternalServerError, err.Error())
	}
}

// hOperatorSessionLock closes the operator session ("Lock now"). It does NOT lock the vault — the
// data plane keeps serving; only privileged operator mutations become unavailable until the next open.
func (d *Daemon) hOperatorSessionLock(w http.ResponseWriter, _ *http.Request) {
	d.opsession.Lock()
	writeJSON(w, http.StatusOK, map[string]bool{"open": false})
}

// hOperatorSessionStatus reports whether the operator session is open and the idle time remaining.
// Read-only peek — it does not slide the idle window.
func (d *Daemon) hOperatorSessionStatus(w http.ResponseWriter, _ *http.Request) {
	open, idle := d.opsession.Status()
	writeJSON(w, http.StatusOK, map[string]any{"open": open, "idle_remaining_seconds": int(idle.Seconds())})
}
```

Register the three routes on `apiMux()` (temporarily, alongside the existing routes — Task 4 reorganizes the whole mux). Add to `apiMux`:

```go
	mux.HandleFunc("POST /operator/session/open", d.hOperatorSessionOpen)
	mux.HandleFunc("POST /operator/session/lock", d.hOperatorSessionLock)
	mux.HandleFunc("GET /operator/session/status", d.hOperatorSessionStatus)
```

> `secret` and `errors` are already imported in `admin.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/daemon/ -run TestOperatorSessionControlRoutes -race -v`
Expected: PASS. Then `go test ./internal/daemon/ -race` — the pre-existing tests still pass (init now also opens the session, which is inert for them; nothing is gated yet).

- [ ] **Step 5: Commit**

```bash
make fmt && make vet
git add internal/daemon/
git commit -m "feat(daemon): operator-session holder + /operator/session/* control routes"
```

---

### Task 4: daemon — route split, `operatorGate` on BOTH transports, retire static CSRF

**Files:**
- Modify: `internal/daemon/admin.go` (`apiMux` 31–66; `UIHandler` 88–97; delete `csrfMiddleware` 99–117; add `operatorGate`)
- Test: `internal/daemon/admin_test.go` (new gate tests; remove/replace the two CSRF tests)

**Interfaces:**
- Produces: `func (d *Daemon) operatorGate(next http.Handler) http.Handler` — returns 403 JSON `{"error":"operator session required"}` (via `adminErr`) when `!d.opsession.Authorized()`, else calls `next`. `apiMux` registers the privileged routes wrapped by `operatorGate` and the read-only/session-control routes plainly; both `AdminHandler()` and `UIHandler()` build from `apiMux`, so BOTH transports are gated. `X-Outwall-CSRF` / `csrfMiddleware` are removed.

- [ ] **Step 1: Write the failing tests** — in `internal/daemon/admin_test.go`:

First, **remove** `TestUICSRFGate` (lines ~324–338 — there is no CSRF gate anymore) and **rename/retarget** `TestUISSEExemptFromCSRF` → `TestUISSEUngated` (SSE stays reachable with no header, because it is simply ungated now — update the comment, keep the assertions). The new SSE test body:

```go
func TestUISSEUngated(t *testing.T) {
	d := newDaemon(t)
	// SSE is read-only and ungated (no CSRF, no operator session). EventSource cannot set headers,
	// so /api/events must be reachable with none. Use a real server + client: the SSE handler
	// streams on its own goroutine; the client returns once headers arrive, and canceling the
	// request context unblocks the streaming handler.
	srv := httptest.NewServer(d.UIHandler())
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
}
```

Then add the gate tests:

```go
func TestOperatorGateSealsBothTransports(t *testing.T) {
	d := newDaemon(t)
	admin := d.AdminHandler() // unix-socket transport (the old CSRF-free full-admin path)
	ui := d.UIHandler()       // TCP /api transport (the old X-Outwall-CSRF path)

	// Bootstrap: init is ungated and opens the operator session.
	require.Equal(t, http.StatusOK, req(t, admin, "POST", "/vault/init", `{"password":"pw"}`).Code)

	// Ungated GET works with no session AND no CSRF header, on both transports.
	require.Equal(t, http.StatusOK, req(t, admin, "GET", "/vault/status", "").Code)
	require.Equal(t, http.StatusOK, req(t, ui, "GET", "/api/vault/status", "").Code)

	const ruleBody = `{"upstream_id":"u1","outcome":"allow","browse_methods":"GET","browse_path":"/**"}`

	// Close the session; a privileged mutation is now 403 over BOTH transports — the old
	// self-approval curl paths (unix socket AND /api) are sealed.
	require.Equal(t, http.StatusOK, req(t, admin, "POST", "/operator/session/lock", "").Code)
	require.Equal(t, http.StatusForbidden, req(t, admin, "POST", "/rules", ruleBody).Code)
	require.Equal(t, http.StatusForbidden, req(t, ui, "POST", "/api/rules", ruleBody).Code)
	require.Equal(t, http.StatusForbidden, req(t, admin, "POST", "/approvals/x/resolve", `{"approve":true}`).Code)
	require.Equal(t, http.StatusForbidden, req(t, ui, "POST", "/api/approvals/x/resolve", `{"approve":true}`).Code)

	// Wrong master password → 401 and the session stays closed (still 403).
	require.Equal(t, http.StatusUnauthorized, req(t, admin, "POST", "/operator/session/open", `{"password":"no"}`).Code)
	require.Equal(t, http.StatusForbidden, req(t, admin, "POST", "/rules", ruleBody).Code)

	// Correct master password opens the session; the gated mutation now succeeds on both transports.
	require.Equal(t, http.StatusOK, req(t, admin, "POST", "/operator/session/open", `{"password":"pw"}`).Code)
	require.Equal(t, http.StatusOK, req(t, admin, "POST", "/rules", ruleBody).Code)
	require.Equal(t, http.StatusOK, req(t, ui, "POST", "/api/rules", ruleBody).Code)
}

func TestVaultUnlockIsGatedByOperatorSession(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	d.vault.Lock()

	// Close the operator session → unlock is refused by the gate (403) and never reaches the vault.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/operator/session/lock", "").Code)
	require.Equal(t, http.StatusForbidden, req(t, h, "POST", "/vault/unlock", `{"password":"pw"}`).Code)
	require.True(t, d.vault.Locked(), "a gate-blocked unlock must not unlock the vault")

	// Opening the session works while the vault is LOCKED (Verify needs no resident key) — this is
	// the existing-vault bootstrap order: /operator/session/open (ungated) THEN /vault/unlock (gated).
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/operator/session/open", `{"password":"pw"}`).Code)
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/unlock", `{"password":"pw"}`).Code)
	require.False(t, d.vault.Locked())
}

func TestOperatorSessionLockKeepsVaultUnlocked(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	require.False(t, d.vault.Locked()) // init leaves the vault unlocked

	// Locking the operator session must NOT lock the vault — the data plane keeps serving.
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/operator/session/lock", "").Code)
	require.False(t, d.vault.Locked(), "operator-session lock must not lock the vault")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run 'TestOperatorGateSealsBothTransports|TestVaultUnlockIsGatedByOperatorSession|TestOperatorSessionLockKeepsVaultUnlocked|TestUISSEUngated' -v`
Expected: FAIL — nothing is gated yet, so the 403 assertions fail (routes return 200/404), and `operatorGate` does not exist. (`TestUICSRFGate` deletion means the old CSRF assertion is gone.)

- [ ] **Step 3: Write minimal implementation** — in `internal/daemon/admin.go`:

Replace the whole `apiMux` body (31–66) with the split mux. Ungated routes registered plainly; gated routes registered through a `gate(...)` closure that wraps each handler with `operatorGate`:

```go
// apiMux registers the admin API routes onto a fresh mux, split into an UNGATED group (read-only
// GETs, the SSE stream, the dry-run preset preview, the launcher focus hand-off, and the
// /operator/session/* control routes — the master-password entry point) and an OPERATOR-GATED group
// (every privileged mutation). Gated routes are wrapped per-route by operatorGate. Both transports —
// the unix socket (AdminHandler) and the TCP UI bind (UIHandler under /api) — build their handler
// from this same table, so the gate applies UNIFORMLY on both: a same-user process can no longer
// self-approve or self-grant over either. (ADR-0041; replaces the ADR-0005 X-Outwall-CSRF model.)
func (d *Daemon) apiMux() *http.ServeMux {
	mux := http.NewServeMux()

	// --- UNGATED: read-only + the operator-session control routes (they grant no power). ---
	mux.HandleFunc("GET /vault/status", d.hVaultStatus)
	// POST /vault/init is the bootstrap that ESTABLISHES the master password — it cannot sit behind
	// the gate (no password exists yet) and opens the session on success (see hVaultInit).
	mux.HandleFunc("POST /vault/init", d.hVaultInit)
	mux.HandleFunc("GET /upstreams", d.hUpstreamList)
	mux.HandleFunc("GET /oidc/redirect-uri", d.hOIDCRedirectURI)
	mux.HandleFunc("GET /agents", d.hAgentList)
	mux.HandleFunc("GET /rules", d.hRuleList)
	mux.HandleFunc("GET /profiles", d.hProfileList)
	mux.HandleFunc("POST /presets/preview", d.hPresetPreview) // dry-run, no state change
	mux.HandleFunc("GET /approvals", d.hApprovalList)
	mux.HandleFunc("GET /access-requests", d.hAccessRequestList)
	mux.HandleFunc("GET /audit", d.hAuditList)
	mux.HandleFunc("GET /audit/{id}", d.hAuditGet)
	mux.HandleFunc("GET /settings/audit-retention", d.hAuditRetentionGet)
	mux.HandleFunc("GET /events", sseHandler(d.bus))
	mux.HandleFunc("POST /desktop/focus", d.hDesktopFocus) // single-instance launcher hand-off
	mux.HandleFunc("POST /operator/session/open", d.hOperatorSessionOpen)
	mux.HandleFunc("POST /operator/session/lock", d.hOperatorSessionLock)
	mux.HandleFunc("GET /operator/session/status", d.hOperatorSessionStatus)

	// --- OPERATOR-GATED: privileged mutations. Each requires an open operator session, on BOTH
	//     transports (per-route wrapping is inherited by AdminHandler AND UIHandler). ---
	gate := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, d.operatorGate(h))
	}
	gate("POST /vault/unlock", d.hVaultUnlock)
	gate("POST /vault/lock", d.hVaultLock)
	gate("POST /upstreams", d.hUpstreamCreate)
	gate("DELETE /upstreams/{name}", d.hUpstreamDelete)
	gate("POST /upstreams/{name}/auth", d.hUpstreamSetAuth)
	gate("POST /upstreams/{name}/oauth/login", d.hOAuthLogin)
	gate("POST /oidc/discover", d.hOIDCDiscover)
	gate("POST /agents/register", d.hAgentRegister)
	gate("POST /clusters/import", d.hClustersImport)
	gate("POST /kubeconfig", d.hKubeconfig)
	gate("POST /rules", d.hRuleCreate)
	gate("DELETE /rules/{id}", d.hRuleDelete)
	gate("POST /rules/{id}/value-policy", d.hRuleSetVariablePolicy)
	gate("POST /approvals/{id}/resolve", d.hApprovalResolve)
	gate("POST /access-requests/{id}/resolve", d.hAccessRequestResolve)
	gate("POST /audit/prune", d.hAuditPrune)
	gate("PUT /settings/audit-retention", d.hAuditRetentionSet)
	return mux
}

// operatorGate wraps a privileged route so it is served only while the operator session is open
// (unlocked by the master password — a secret the same-user agent does not hold). Otherwise it
// returns 403 with {"error":"operator session required"}, which the CLI (sudo-style prompt) and the
// web UI (master-password modal) recognize to trigger a re-open. A successful Authorized() call
// slides the idle window (see internal/opsession).
func (d *Daemon) operatorGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !d.opsession.Authorized() {
			adminErr(w, http.StatusForbidden, "operator session required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

> Delete the three `mux.HandleFunc("POST /operator/session/...")` lines added in Task 3 if you register them here instead (they are already in the ungated group above — do not duplicate).

Update `UIHandler` (drop the CSRF wrapper) and delete `csrfMiddleware`:

```go
// UIHandler builds the desktop-UI handler served over the UIListen TCP bind: the embedded SPA at
// "/", the shared admin/SSE mux under "/api" (privileged routes gated by the operator session — see
// apiMux/operatorGate), and the OIDC browser-login redirect target. There is no CSRF wrapper: the
// operator-session gate (master password) replaced the X-Outwall-CSRF model (ADR-0041 amends
// ADR-0005). GET /api/events (SSE) stays reachable — it is ungated and read-only.
func (d *Daemon) UIHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", d.apiMux()))
	// The OIDC browser-login redirect target is served top-level (not under /api): a redirect from
	// the IdP cannot carry app headers, and the random state ties it to a login this daemon started
	// (see oauth.go). The POST that STARTS the login (/api/upstreams/{name}/oauth/login) is gated.
	mux.HandleFunc("/oauth/callback", d.hOAuthCallback)
	mux.Handle("/", staticUI())
	return mux
}
```

Delete `csrfMiddleware` entirely (the whole func 99–117 and its doc comment).

Also update the `apiMux`/`AdminHandler`/`UIHandler` doc comments that mention CSRF (the `AdminHandler` comment at 81–82 mentioning "CSRF-free" is now redundant — replace with a note that both transports are gated):

```go
// AdminHandler builds the admin API mux served over the unix socket for the local operator CLI.
// Privileged routes are gated by the operator session exactly as on the UI transport (apiMux).
func (d *Daemon) AdminHandler() http.Handler { return d.apiMux() }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/daemon/ -race`
Expected: PASS — the new gate tests + the pre-existing admin tests. The pre-existing tests init the vault first (which now opens the session), so their subsequent gated calls (`POST /upstreams`, `POST /rules`, `POST /approvals/{id}/resolve`, `POST /clusters/import`, …) are authorized. Confirm `TestDesktopFocusRoute` (ungated `POST /desktop/focus`) and the two SSE tests still pass.

> If any pre-existing test drives a gated route on a daemon that never `init`ed the vault (thus no open session), open a session in that test first: `require.Equal(t, http.StatusOK, req(t, h, "POST", "/operator/session/open", `{"password":"pw"}`).Code)` after an init, or add the init it was implicitly relying on. Do NOT weaken the gate.

- [ ] **Step 5: Full daemon build sanity + commit**

```bash
make fmt && make vet && make test
git add internal/daemon/
git commit -m "feat(daemon): gate privileged routes behind operator session on both transports; retire X-Outwall-CSRF"
```

---

### Task 5: CLI — sudo-style operator session for privileged commands

**Files:**
- Create: `internal/cli/session.go` (`doPrivileged`, `isSessionRequired`)
- Modify: `internal/cli/approval.go` (`resolve`), `internal/cli/vault.go` (`unlock`), then sweep the remaining privileged commands: `rule.go` (add/delete/value-policy), `upstream.go` (add/auth/delete/oauth), `cluster.go` (import), `audit.go` (prune, retention set), `access.go` (resolve)
- Test: `internal/cli/session_test.go`

**Interfaces:**
- Produces: `doPrivileged(gf *globalFlags, method, path string, body, out any) error` — runs the call; if the daemon answers "operator session required", prompts for the master password on the TTY (`promptPassword`, already in `vault.go`), POSTs `/operator/session/open`, and retries once. `isSessionRequired(err error) bool` — matches the gate's error string. Read-only CLI commands are unchanged.
- Consumes: `client.Client.Do` (surfaces the daemon error as `daemon: operator session required`).

- [ ] **Step 1: Write the failing test** — create `internal/cli/session_test.go`:

```go
package cli

import (
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/client"
)

func TestIsSessionRequired(t *testing.T) {
	require.False(t, isSessionRequired(nil))
	require.False(t, isSessionRequired(errors.New("daemon: something else")))
	// The daemon gate surfaces through client.Do as "daemon: operator session required".
	require.True(t, isSessionRequired(errors.New("daemon: operator session required")))
}

// TestDoPrivilegedOpensSessionAndRetries stands up a unix-socket daemon stub that rejects the first
// privileged call with 403 "operator session required", accepts /operator/session/open, then accepts
// the retry — proving doPrivileged prompts (stubbed), opens the session, and retries exactly once.
func TestDoPrivilegedOpensSessionAndRetries(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "d.sock")
	var opened, calls int32

	mux := http.NewServeMux()
	mux.HandleFunc("POST /operator/session/open", func(w http.ResponseWriter, _ *http.Request) {
		atomic.StoreInt32(&opened, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"open":true,"idle_remaining_seconds":3600}`))
	})
	mux.HandleFunc("POST /rules", func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 && atomic.LoadInt32(&opened) == 0 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"operator session required"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"r1"}`))
	})

	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	// Stub the TTY prompt so the test needs no terminal.
	restore := promptPasswordFn
	promptPasswordFn = func(string) (string, error) { return "pw", nil }
	t.Cleanup(func() { promptPasswordFn = restore })

	gf := &globalFlags{socket: sock}
	var out map[string]string
	require.NoError(t, doPrivileged(gf, "POST", "/rules", map[string]string{"x": "y"}, &out))
	require.Equal(t, "r1", out["id"])
	require.Equal(t, int32(1), atomic.LoadInt32(&opened), "session must have been opened")
	require.Equal(t, int32(2), atomic.LoadInt32(&calls), "the privileged call must run twice (initial 403 + retry)")

	// Sanity: a plain client with no session gets the raw gate error.
	require.True(t, isSessionRequired(client.New(sock).Do("POST", "/rules-does-not-exist", nil, nil)) == false)
	_ = os.Remove(sock)
}
```

> The test needs `promptPassword` to be stubbable. Task Step 3 introduces a package var `promptPasswordFn` that defaults to the existing `promptPassword`, so the test can swap it. (Keeps `promptPassword` itself intact for real use.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run 'TestIsSessionRequired|TestDoPrivilegedOpensSessionAndRetries' -v`
Expected: FAIL — `doPrivileged`, `isSessionRequired`, `promptPasswordFn` do not exist.

- [ ] **Step 3: Write minimal implementation** — create `internal/cli/session.go`:

```go
package cli

import (
	"strings"

	"github.com/Sipaha/outwall/internal/client"
)

// promptPasswordFn is the (stubbable) TTY password prompt used by the sudo-style session helper.
// It defaults to the real terminal prompt; tests swap it to avoid needing a TTY.
var promptPasswordFn = promptPassword

// isSessionRequired reports whether err is the daemon's operator-session gate response. client.Do
// surfaces the daemon's {"error":"operator session required"} body as "daemon: operator session
// required", so a substring match is the stable contract.
func isSessionRequired(err error) bool {
	return err != nil && strings.Contains(err.Error(), "operator session required")
}

// doPrivileged runs a privileged admin call and transparently opens an operator session when the
// daemon reports one is required (sudo-style). On the gate error it prompts for the master password
// on the TTY, POSTs /operator/session/open, and retries the call exactly once. A call that succeeds
// (an already-open session within the idle TTL) never prompts.
func doPrivileged(gf *globalFlags, method, path string, body, out any) error {
	c := newClient(gf)
	err := c.Do(method, path, body, out)
	if err == nil || !isSessionRequired(err) {
		return err
	}
	pw, perr := promptPasswordFn("Operator master password: ")
	if perr != nil {
		return perr
	}
	if oerr := c.Do("POST", "/operator/session/open", map[string]string{"password": pw}, nil); oerr != nil {
		return oerr
	}
	return c.Do(method, path, body, out)
}

var _ = client.New // keep the import used if the sweep temporarily removes direct client refs
```

> Drop the `var _ = client.New` line once `client` is actually referenced by the file (it is, via `newClient` indirectly — but `newClient` lives in `root.go`, so `session.go` does not import `client` unless referenced). Simplest: remove the `client` import and the `var _` line entirely — `session.go` needs only `strings` and the in-package `newClient`/`promptPassword`. Final `session.go` imports: just `"strings"`.

Correct final imports for `session.go`: only `"strings"`. Remove the `client` import + the `var _ = client.New` line. (The test file imports `client` for its own sanity assertion.)

Convert `internal/cli/vault.go` `unlock` — read the password once, open the session with it (which also satisfies the gate on `/vault/unlock`), then unlock. Replace the `unlockCmd` `RunE`:

```go
		RunE: func(c *cobra.Command, _ []string) error {
			pw, err := readPassword(c.InOrStdin(), unlockStdin, "Master password: ")
			if err != nil {
				return err
			}
			cl := newClient(gf)
			// Open the operator session with the same master password — this both authorizes the
			// gate on /vault/unlock AND avoids a second prompt (unlock is a gated route now).
			if err := cl.Do("POST", "/operator/session/open", map[string]string{"password": pw}, nil); err != nil {
				return err
			}
			return cl.Do("POST", "/vault/unlock", map[string]string{"password": pw}, nil)
		},
```

Convert `internal/cli/approval.go` `resolve` — route through `doPrivileged`:

```go
			if err := doPrivileged(gf, "POST", "/approvals/"+args[0]+"/resolve",
				map[string]bool{"approve": approve}, nil); err != nil {
				return err
			}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -race`
Expected: PASS (the two new tests + `TestReadPasswordFromStdin`).

- [ ] **Step 5: Commit**

```bash
make fmt && make vet
git add internal/cli/session.go internal/cli/session_test.go internal/cli/approval.go internal/cli/vault.go
git commit -m "feat(cli): sudo-style operator-session helper; wire approval resolve + vault unlock"
```

- [ ] **Step 6: Sweep the remaining privileged commands through `doPrivileged`**

Route every other privileged admin mutation through `doPrivileged(gf, …)` (they currently call `newClient(gf).Do(…)` directly). Read-only `list`/`status`/`get` commands stay on `newClient(gf).Do`. Convert:

- `internal/cli/rule.go` — `rule add` (`POST /rules`), `rule delete` (`DELETE /rules/{id}`), and any value-policy set (`POST /rules/{id}/value-policy`).
- `internal/cli/upstream.go` — `upstream add` (`POST /upstreams`), `upstream auth` (`POST /upstreams/{name}/auth`), `upstream delete` (`DELETE /upstreams/{name}`), `upstream oauth login` (`POST /upstreams/{name}/oauth/login`), and `oidc discover` if present.
- `internal/cli/cluster.go` — `cluster import` (`POST /clusters/import`), plus any create.
- `internal/cli/audit.go` — `audit prune` (`POST /audit/prune`), retention set (`PUT /settings/audit-retention`); leave `audit list`/`get`/retention-get unchanged.
- `internal/cli/access.go` — `access resolve` (`POST /access-requests/{id}/resolve`); leave `access list` unchanged.
- `internal/cli/kubeconfig.go` — if it POSTs `/kubeconfig` (a gated route), route through `doPrivileged`; if it is a read-only assembler over another path, leave it.

For each: replace `newClient(gf).Do("POST"/"DELETE"/"PUT", path, body, out)` with `doPrivileged(gf, "POST"/"DELETE"/"PUT", path, body, out)`. No behavior change beyond the transparent session prompt-and-retry.

Run: `go test ./internal/cli/ -race && CGO_ENABLED=0 go build ./...`
Expected: PASS/clean.

- [ ] **Step 7: Commit**

```bash
make fmt && make vet
git add internal/cli/
git commit -m "feat(cli): route remaining privileged commands through the operator-session helper"
```

---

### Task 6: web — drop static CSRF, add operator-session helpers + master-password prompt

**Files:**
- Modify: `web/src/lib/api.ts`
- Modify: `web/src/lib/api.test.ts`
- Create: `web/src/lib/operatorSession.ts`, `web/src/components/OperatorSessionModal.tsx`
- Modify: `web/src/App.tsx`

**Interfaces:**
- Produces: `openOperatorSession(password)`, `lockOperatorSession()`, `getOperatorSessionStatus()`, `setSessionRequiredHandler(fn)`, `OperatorSessionStatus` type; a `useOperatorSession` zustand store; an `OperatorSessionModal`. Removes `CSRF_HEADER` and the `X-Outwall-CSRF` header from every request.

- [ ] **Step 1: Write the failing test** — update `web/src/lib/api.test.ts`:

Change the first test to assert NO CSRF header, and update the GET-helpers test the same way; add an `openOperatorSession` test. Replace the file's three `it(...)` blocks with:

```ts
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, vaultUnlock, listAgents, openOperatorSession } from './api'

afterEach(() => {
  vi.restoreAllMocks()
})

describe('api client', () => {
  it('vaultUnlock posts to /api/vault/unlock with NO CSRF header and a password body', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ locked: false }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    const res = await vaultUnlock('hunter2')
    expect(res).toEqual({ locked: false })

    const [url, opts] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/vault/unlock')
    expect(opts.method).toBe('POST')
    // The static X-Outwall-CSRF model is retired — the operator-session gate replaced it.
    expect(opts.headers['X-Outwall-CSRF']).toBeUndefined()
    expect(opts.headers['Content-Type']).toBe('application/json')
    expect(JSON.parse(opts.body)).toEqual({ password: 'hunter2' })
  })

  it('openOperatorSession posts to /operator/session/open with the password body', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ open: true, idle_remaining_seconds: 3600 }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    const res = await openOperatorSession('hunter2')
    expect(res).toEqual({ open: true, idle_remaining_seconds: 3600 })
    const [url, opts] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/operator/session/open')
    expect(opts.method).toBe('POST')
    expect(opts.headers['X-Outwall-CSRF']).toBeUndefined()
    expect(JSON.parse(opts.body)).toEqual({ password: 'hunter2' })
  })

  it('throws ApiError with status + daemon error message on a 401', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: 'incorrect master password' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(vaultUnlock('wrong')).rejects.toMatchObject({
      name: 'ApiError',
      status: 401,
      message: 'incorrect master password',
    })
    await expect(vaultUnlock('wrong')).rejects.toBeInstanceOf(ApiError)
  })

  it('GET helpers send NO CSRF header and parse JSON arrays', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify([{ id: 'a1', name: 'claude', status: 'new' }]), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    const agents = await listAgents()
    expect(agents).toHaveLength(1)
    expect(agents[0].name).toBe('claude')
    const [url, opts] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/agents')
    expect(opts.headers['X-Outwall-CSRF']).toBeUndefined()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/lib/api.test.ts`
Expected: FAIL — `openOperatorSession` is not exported, and the CSRF assertions fail (the header is still sent).

- [ ] **Step 3: Write minimal implementation** — in `web/src/lib/api.ts`:

Delete the `CSRF_HEADER` const (lines ~21–26). In `request<T>()` (68–79), drop the CSRF spread and detect the gate error:

```ts
let sessionRequiredHandler: (() => void) | null = null

/**
 * Register a callback fired when the daemon rejects a privileged call with 403 "operator session
 * required", so the UI can prompt for the master password. Passing null clears it.
 */
export function setSessionRequiredHandler(fn: (() => void) | null): void {
  sessionRequiredHandler = fn
}

/**
 * Single transport: prefixes API_BASE, sets Content-Type, serializes the body, and converts every
 * non-2xx into an ApiError. When the daemon returns 403 "operator session required" (the operator
 * plane is sealed behind the master-password session — ADR-0041) it fires the registered handler so
 * the UI can prompt, then throws so the caller still sees the failure.
 */
async function request<T>(method: HttpMethod, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  let payload: BodyInit | undefined
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    payload = JSON.stringify(body)
  }
  const res = await fetchWithTimeout(API_BASE + path, { method, headers, body: payload })
  if (!res.ok) {
    const err = await extractApiError(res)
    if (err.status === 403 && err.message === 'operator session required') {
      sessionRequiredHandler?.()
    }
    throw err
  }
  const text = await res.text()
  return (text ? JSON.parse(text) : undefined) as T
}
```

In `importKubeconfigContent` (181–190), drop `...CSRF_HEADER` from its headers, leaving `{ 'Content-Type': 'application/yaml' }`.

Add the operator-session helpers + type (near the Vault section):

```ts
// --- Operator session (master-password gate; ADR-0041) ---

export interface OperatorSessionStatus {
  open: boolean
  idle_remaining_seconds: number
}

/** Open the operator session by verifying the master password (does NOT unlock the vault). */
export function openOperatorSession(password: string): Promise<OperatorSessionStatus> {
  return request('POST', '/operator/session/open', { password })
}

/** Close the operator session ("Lock now"). The vault stays unlocked; the data plane keeps serving. */
export function lockOperatorSession(): Promise<{ open: boolean }> {
  return request('POST', '/operator/session/lock')
}

/** Current operator-session state (open + idle seconds remaining). Read-only; does not slide the TTL. */
export function getOperatorSessionStatus(): Promise<OperatorSessionStatus> {
  return request('GET', '/operator/session/status')
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/lib/api.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/api.ts web/src/lib/api.test.ts
git commit -m "feat(web): drop static CSRF header; add operator-session api helpers + gate hook"
```

- [ ] **Step 6: Light prompt UI + Lock-now control** — create `web/src/lib/operatorSession.ts`:

```ts
import { create } from 'zustand'
import { getOperatorSessionStatus, lockOperatorSession, openOperatorSession } from './api'

interface OperatorSessionState {
  open: boolean
  idleRemaining: number
  promptOpen: boolean
  refresh: () => Promise<void>
  unlock: (password: string) => Promise<void>
  lockNow: () => Promise<void>
  requirePrompt: () => void
  dismissPrompt: () => void
}

/** Mirrors the daemon operator session so the shell can show a lock indicator + "Lock now", and the
 *  api transport can pop a master-password prompt when a privileged call hits the 403 gate. */
export const useOperatorSession = create<OperatorSessionState>((set) => ({
  open: false,
  idleRemaining: 0,
  promptOpen: false,
  async refresh() {
    try {
      const s = await getOperatorSessionStatus()
      set({ open: s.open, idleRemaining: s.idle_remaining_seconds })
    } catch {
      set({ open: false, idleRemaining: 0 })
    }
  },
  async unlock(password: string) {
    const s = await openOperatorSession(password)
    set({ open: s.open, idleRemaining: s.idle_remaining_seconds, promptOpen: false })
  },
  async lockNow() {
    await lockOperatorSession()
    set({ open: false, idleRemaining: 0 })
  },
  requirePrompt() {
    set({ promptOpen: true })
  },
  dismissPrompt() {
    set({ promptOpen: false })
  },
}))
```

Create `web/src/components/OperatorSessionModal.tsx` (reuse the existing `Modal`):

```tsx
import { useState } from 'react'
import { Modal } from './Modal'
import { ApiError } from '../lib/api'
import { useOperatorSession } from '../lib/operatorSession'

const inputClass =
  'w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary'

/** Master-password prompt shown when a privileged action hits the operator-session gate (or when the
 *  operator explicitly re-opens the session). Opening it does NOT unlock the vault — it authorizes
 *  privileged operator mutations for the idle-TTL window. */
export function OperatorSessionModal() {
  const promptOpen = useOperatorSession((s) => s.promptOpen)
  const dismiss = useOperatorSession((s) => s.dismissPrompt)
  const unlock = useOperatorSession((s) => s.unlock)
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    if (!password) {
      setError('Master password is required')
      return
    }
    setBusy(true)
    try {
      await unlock(password)
      setPassword('')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Request failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={promptOpen}
      title="Operator session"
      width="sm"
      onClose={dismiss}
      onSubmit={submit}
      footer={
        <button
          type="submit"
          disabled={busy}
          className="rounded bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
        >
          {busy ? '…' : 'Open session'}
        </button>
      }
    >
      <p className="text-xs text-muted-foreground">
        Privileged operator actions require your master password. This authorizes them for a short
        idle window and does not change the vault lock state.
      </p>
      <input
        type="password"
        autoFocus
        className={inputClass}
        value={password}
        onChange={(e) => setPassword(e.target.value)}
        aria-label="Master password"
      />
      {error && <div className="text-[11px] text-destructive">{error}</div>}
    </Modal>
  )
}
```

Wire into `web/src/App.tsx`: register the gate hook, render the modal, refresh session status on mount. Add imports:

```tsx
import { getVaultStatus, ApiError, setSessionRequiredHandler } from './lib/api'
import { useOperatorSession } from './lib/operatorSession'
import { OperatorSessionModal } from './components/OperatorSessionModal'
```

Inside `App()`, after the existing store selectors, add:

```tsx
  const requirePrompt = useOperatorSession((s) => s.requirePrompt)
  const refreshSession = useOperatorSession((s) => s.refresh)

  // Let the api transport pop the master-password prompt on a 403 "operator session required".
  useEffect(() => {
    setSessionRequiredHandler(() => requirePrompt())
    return () => setSessionRequiredHandler(null)
  }, [requirePrompt])

  // Reflect the daemon session state once the shell is up.
  useEffect(() => {
    if (unlocked) refreshSession()
  }, [unlocked, refreshSession])
```

Render `<OperatorSessionModal />` inside the shell (next to `<ToastContainer />`):

```tsx
      <ToastContainer />
      <OperatorSessionModal />
```

> Optional (nice-to-have, keep light): a small "Lock now" button + idle indicator in `Sidebar.tsx` driven by `useOperatorSession((s) => s.open)` / `.lockNow()`. Not required for the security guarantee; add only if trivial.

Run: `cd web && npx vitest run && npx tsc --noEmit`
Expected: PASS / no type errors.

- [ ] **Step 7: Commit**

```bash
git add web/src/lib/operatorSession.ts web/src/components/OperatorSessionModal.tsx web/src/App.tsx
git commit -m "feat(web): master-password operator-session prompt + gate wiring"
```

---

### Task 7: Docs — ADR-0041, amend ADR-0005, overview, INDEX

**Files:**
- Create: `docs/architecture/decisions/0041-operator-session-master-password.md`
- Modify: `docs/architecture/decisions/0005-control-api-sse.md`, `docs/architecture/overview.md`, `docs/INDEX.md`

> ADR numbering: the companion Plan 1 (agent socket + remove MCP) takes **ADR-0040**, so this plan is
> **ADR-0041**. Execute Plan 1 first. If for some reason this plan lands first, verify the next free
> number by listing `docs/architecture/decisions/` and renumber (0040) accordingly.

- [ ] **Step 1: Run the full gate**

```bash
make fmt && make vet && make test
cd web && npx vitest run && cd ..
CGO_ENABLED=0 go build ./...
```
Expected: all green.

- [ ] **Step 2: Write ADR-0041** — create `docs/architecture/decisions/0041-operator-session-master-password.md` (status accepted, date 2026-07-06). Cover:
  - **Problem:** the same-user agent could reach every privileged operator mutation over the CSRF-free unix socket AND the static-`X-Outwall-CSRF` TCP `/api` — self-approve, self-grant, unlock, set-auth. `0600` + loopback do not separate two same-user processes.
  - **Decision:** route split — read-only GETs + SSE + dry-run preview + launcher focus + the `/operator/session/*` control routes are UNGATED (grant no power); every privileged mutation is OPERATOR-GATED. The gate = an in-daemon `opsession.Session` opened by the **master password** verified via `secret.Vault.Verify` (Argon2id salt/verifier) WITHOUT unlocking the vault, with an idle-TTL **sliding window** (`DefaultTTL = 1h`) and an explicit "Lock now". `operatorGate` wraps gated routes per-route inside `apiMux`, so BOTH transports (unix socket + UI `/api`) are gated uniformly. The static `X-Outwall-CSRF` model + `csrfMiddleware` are removed.
  - **Bootstrap / vault-vs-session (Deviation D2):** `POST /vault/init` is ungated (it establishes the password; nothing to protect yet) and opens the session on success. For an existing vault the order is `/operator/session/open` (ungated; Verify works while the vault is locked) → `/vault/unlock` (gated). The operator session is distinct from the vault lock: idle-expiry / Lock-now do NOT lock the vault, so the data plane keeps serving.
  - **R7 deferral (Deviation D1):** spec R7 wants desktop privileged verbs delivered via Wails native bindings (no HTTP endpoint at all). The codebase has no project-authored Wails bindings; the master-password gate is the boundary that holds in every mode, so this ADR delivers it over HTTP for both transports and DEFERS the Wails-bindings migration (removing the HTTP endpoint as defense-in-depth) to a future Plan 3.
  - **Threat model (in/out of scope):** *in scope (defended)* — agent never sees upstream secrets; egress default-deny/filtered/audited; privileged operator actions require the master password (casual self-approval closed). *Out of scope (recorded limitation)* — a malicious same-user shell agent that ALSO obtains the master password (phishing/keylogging/driving the GUI); the only robust fix is a **separate OS identity** for the daemon (separate user / user namespace / systemd `hidepid`/`ProtectProc`, polkit-style) — the escalation path, not built now. `PR_SET_DUMPABLE=0` is optional Linux-only DiD, NOT load-bearing (leaks via re-`execve`'d WebKit subprocesses) — "raises the bar, not a boundary."
  - **Alternatives:** in-memory capability token over HTTP (B-cap) — rejected, leaks into the WebKitGTK renderer, security theater vs the master password; B-ipc with no password — GUI automation would approve without a secret; separate OS user — the real fix, deferred as the escalation.
  - **Consequences:** self-approval curl paths now 403 on both transports; one extra master-password prompt per idle window (sudo-style CLI, modal UI); no schema change. Link the spec, ADR-0005 (amended), ADR-0002 (approval model), ADR-0021 (OIDC login start now gated).

- [ ] **Step 3: Amend ADR-0005** — in `docs/architecture/decisions/0005-control-api-sse.md`, add an amendment note at the top (under the Status line) and a closing note:

  > **Amended by ADR-0041 (2026-07-06):** the `X-Outwall-CSRF` gate described here is **removed**. Loopback + CSRF is no longer the boundary for a same-user agent — the boundary is now the **master-password operator session** (ADR-0041), which gates every privileged mutation on BOTH the unix-socket and `/api` transports. The `UIListen` bind, the event bus, and the SSE stream are unchanged; `GET /events` stays ungated and read-only (it was CSRF-exempt before and is ungated now).

  Leave the rest of ADR-0005 intact (event bus, SSE design, taxonomy remain accurate).

- [ ] **Step 4: Update `docs/architecture/overview.md`** — refresh the two-plane picture note and the key invariants:
  - Add to **Key invariants**:
    - `- Privileged operator mutations (approve, grant, unlock, set-auth, prune) require an open **operator session**, unlocked by the master password, on every transport (unix socket + UI /api). A same-user agent cannot self-approve (ADR-0041).`
    - `- The operator session is distinct from the vault lock: it has an idle TTL + "Lock now"; idle-expiry does NOT lock the vault (the data plane keeps serving).`
  - Update the control-plane line / two-plane picture caption to note that the operator/admin surface is split into ungated read-only routes and master-password-gated mutations (and that the static CSRF header is retired). Keep it brief — the ADR holds the detail.

- [ ] **Step 5: Update `docs/INDEX.md`** — add the ADR line under `decisions/`:

  > `- [`0041-operator-session-master-password.md`](architecture/decisions/0041-operator-session-master-password.md) — seal the operator plane: route split (ungated read-only + session-control vs master-password-gated mutations), operator session (Verify-without-unlock, idle-TTL sliding window, Lock now) gating BOTH transports, retire static X-Outwall-CSRF; threat model (same-user boundary = master password; separate-OS-user = escalation; PR_SET_DUMPABLE = non-load-bearing DiD); R7 Wails-bindings deferred to Plan 3.`

  And add this plan under `plans/`:

  > `- Seal the operator plane: [`2026-07-06-seal-operator-plane.md`](superpowers/plans/2026-07-06-seal-operator-plane.md) (secret.Verify, internal/opsession, route split + operator gate on both transports, CLI sudo-style helper, web prompt; ADR-0041). Spec: [`2026-07-06-mcp-to-direct-socket-design.md`](superpowers/specs/2026-07-06-mcp-to-direct-socket-design.md).`

  If the spec is not yet linked under `specs/`, add it there too (it is already listed at INDEX line ~77 — verify and leave as-is if present).

- [ ] **Step 6: Commit + push**

```bash
git add docs/
git commit -m "docs(adr-0041): master-password operator session; amend ADR-0005; overview + INDEX"
git push origin main
```

---

## Self-review notes

- **Requirement coverage:**
  - **R5 (route split — privileged behind the session, read-only + SSE ungated):** Task 4 `apiMux` split + `operatorGate`.
  - **R6 (operator session = master password; Verify-without-unlock; idle-TTL; Lock now):** Task 1 (`secret.Verify`), Task 2 (`opsession` sliding window + `DefaultTTL`), Task 3 (`/operator/session/*` routes), Task 6 (Lock-now/prompt UI).
  - **R7 (desktop delivery):** delivered as the master-password gate over HTTP for both transports (Tasks 3–6); the Wails-native-bindings remainder is explicitly DEFERRED to Plan 3 and recorded in the intro + ADR-0041 (Deviation D1).
  - **R8 (retire CSRF-free full-admin behavior + static X-Outwall-CSRF):** Task 4 deletes `csrfMiddleware`, drops the header in Task 6; every privileged route now needs the session; the operator socket stays but is gated.
  - **Threat-model ADR:** Task 7 Step 2 (in/out scope, separate-OS-user escalation, `PR_SET_DUMPABLE` non-load-bearing).
- **Consistent names/types across tasks:** `secret.Verify`; `opsession.Session`/`New`/`Open`/`Lock`/`Authorized`/`Status`/`DefaultTTL`; `daemon.operatorGate`; routes `POST /operator/session/open`, `POST /operator/session/lock`, `GET /operator/session/status`; web `openOperatorSession`/`lockOperatorSession`/`getOperatorSessionStatus`/`setSessionRequiredHandler`; CLI `doPrivileged`/`isSessionRequired`/`promptPasswordFn`. Used identically in every task that references them.
- **Both-transports proof:** `TestOperatorGateSealsBothTransports` asserts the 403 on the gated route over BOTH `AdminHandler()` (the old unix-socket self-approval curl) AND `UIHandler()` `/api` (the old CSRF path); `TestVaultUnlockIsGatedByOperatorSession` proves unlock is gated and cannot bring up the data plane without a session; `TestOperatorSessionLockKeepsVaultUnlocked` proves the session/vault separation.
- **Bootstrap deadlock resolved (D2):** init is ungated + opens the session; Verify works while the vault is locked → existing-vault order `session/open` → `vault/unlock`. Documented in intro + ADR + asserted by `TestVaultUnlockIsGatedByOperatorSession` and `TestOperatorSessionControlRoutes`.
- **Pre-existing tests:** they init the vault first (init now opens the session), so their subsequent gated calls stay authorized — no weakening of the gate. The two CSRF tests are removed/retargeted (Task 4 Step 1) because CSRF is gone.
- **No new deps** (`golang.org/x/term` already present). **No schema change** (session is in-memory). **No CGO.** No `citeck` added.
- **Placeholder scan:** none — every code step is complete, copy-pasteable code with exact paths, commands, and expected output. The Task 5 Step 6 sweep enumerates each command file/route explicitly (mechanical `newClient(gf).Do` → `doPrivileged(gf, …)` swaps).
