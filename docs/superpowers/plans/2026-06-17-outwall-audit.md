# outwall — Plan 4: Audit (request journal + body store)

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development or superpowers:executing-plans.

**Goal:** Record every data-plane request/response in SQLite: timestamp, agent, upstream,
method, path+query, status, duration, sizes, policy decision (+ rule, approval), masked request
headers, and the request/response **bodies up to 256 KB** (larger → truncated with the total
size noted; non-text by Content-Type → metadata only: content-type, declared size, sha256 of
stored bytes). Credentials are masked. Bodies live in a separate table so the journal lists
fast. Keep-all by default + manual prune. Surfaced via admin API + CLI.

**Architecture:** A new `internal/audit` package owns the `audit_log` + `audit_bodies` tables and
a `Recorder`. The data-plane proxy captures bodies via a **capped streaming tee** (no full-body
buffering): the request body is teed as the ReverseProxy forwards it; the response body is teed
in `ModifyResponse`, and the audit row is written when the response body is closed (after it has
streamed to the agent). Masking is applied to the stored request headers. The proxy gains an
optional `Audit *audit.Recorder` dep (nil-safe — Plans 1–3 behavior unchanged when absent).

**Tech Stack:** Plan 1–3 stack (no new deps).

## Global Constraints

(All prior constraints apply.) Plus:
- Body cap: **256 KiB** (`audit.BodyCap = 256 * 1024`).
- Text detection by Content-Type: store the body only when the media type is textual
  (`text/*`, `application/json`, `application/xml`, `application/x-www-form-urlencoded`,
  `application/*+json`, `application/*+xml`, or empty Content-Type on a non-empty body ≤ cap);
  otherwise store metadata only (no bytes).
- Masking: header values for `Authorization`, `Proxy-Authorization`, `Cookie`, `Set-Cookie`,
  and any header whose name contains `api-key`/`apikey`/`token`/`secret` (case-insensitive) →
  `"***"`. (Upstream creds are injected after capture so they never appear in the stored agent
  request, but mask defensively anyway.)
- `sha256` is computed over the **stored** bytes (what we captured, ≤ cap), not the full body —
  documented in ADR-0004.

## File Structure

```
Create:  internal/audit/recorder.go       # tables, Entry/Body models, Record/List/Get/Prune
         internal/audit/capture.go          # cappedCapture (streaming tee) + body classification + masking
         internal/audit/recorder_test.go
         internal/audit/capture_test.go
Modify:  internal/store/migrate.go          # + audit_log, audit_bodies tables
         internal/proxy/proxy.go            # capture req/resp bodies; record on response close + on error
         internal/proxy/proxy_test.go        # assert an entry is recorded (masked, captured, truncated, non-text)
         internal/daemon/daemon.go           # build audit.Recorder; pass into proxy.Deps
         internal/daemon/admin.go            # GET /audit, GET /audit/{id}, POST /audit/prune
         internal/daemon/admin_test.go
         internal/cli/root.go                # register audit cmd
         internal/cli/audit.go (new)         # `outwall audit list|show|prune`
```

---

### Task 1: audit tables + Recorder + masking

**Files:** Modify `internal/store/migrate.go`; create `internal/audit/recorder.go`, `internal/audit/capture.go` (masking + classification portion), `internal/audit/recorder_test.go`.

**Interfaces:**
- Tables:
```sql
CREATE TABLE IF NOT EXISTS audit_log (
	id            TEXT PRIMARY KEY,
	ts            TEXT NOT NULL,
	agent_id      TEXT NOT NULL DEFAULT '',
	agent_name    TEXT NOT NULL DEFAULT '',
	upstream_id   TEXT NOT NULL DEFAULT '',
	upstream_name TEXT NOT NULL DEFAULT '',
	method        TEXT NOT NULL DEFAULT '',
	path          TEXT NOT NULL DEFAULT '',
	query         TEXT NOT NULL DEFAULT '',
	status_code   INTEGER NOT NULL DEFAULT 0,
	duration_ms   INTEGER NOT NULL DEFAULT 0,
	req_bytes     INTEGER NOT NULL DEFAULT 0,
	resp_bytes    INTEGER NOT NULL DEFAULT 0,
	decision      TEXT NOT NULL DEFAULT '',
	rule_id       TEXT NOT NULL DEFAULT '',
	headers_json  TEXT NOT NULL DEFAULT '',
	error         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS audit_log_by_ts ON audit_log(ts);
CREATE TABLE IF NOT EXISTS audit_bodies (
	log_id       TEXT NOT NULL,
	kind         TEXT NOT NULL,            -- 'request' | 'response'
	content_type TEXT NOT NULL DEFAULT '',
	size         INTEGER NOT NULL DEFAULT 0,   -- total declared/observed size
	sha256       TEXT NOT NULL DEFAULT '',
	truncated    INTEGER NOT NULL DEFAULT 0,
	stored       BLOB,                      -- NULL when non-text / not stored
	PRIMARY KEY (log_id, kind)
);
```
- `audit.Entry struct { ID string; TS time.Time; AgentID, AgentName, UpstreamID, UpstreamName, Method, Path, Query string; StatusCode, DurationMs int; ReqBytes, RespBytes int64; Decision, RuleID, Error string; Headers map[string]string }`
- `audit.Body struct { Kind, ContentType string; Size int64; Sha256 string; Truncated bool; Stored []byte }` (Kind: `"request"`/`"response"`).
- `audit.NewRecorder(s *store.Store) *audit.Recorder`
- `(*Recorder).Record(e Entry, bodies ...Body) error` — assigns `e.ID` if empty, inserts the log row (Headers JSON-marshaled to `headers_json`) and each body row.
- `(*Recorder).List(limit int) ([]Entry, error)` — newest first (no bodies).
- `(*Recorder).Get(id string) (Entry, []Body, error)` — with bodies; `ErrNotFound` if absent.
- `(*Recorder).Prune(olderThan time.Time) (int64, error)` — delete rows with `ts < olderThan`, cascade their bodies; returns count.
- `audit.ErrNotFound`.
- In `capture.go`: `audit.MaskHeaders(h http.Header) map[string]string` (per Global Constraints).

- [ ] **Step 1: failing test** — `internal/audit/recorder_test.go`:
```go
package audit

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/store"
)

func newRec(t *testing.T) *Recorder {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "au.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return NewRecorder(s)
}

func TestRecordGetListPrune(t *testing.T) {
	rec := newRec(t)
	e := Entry{
		TS: time.Now().UTC(), AgentName: "claude", UpstreamName: "github", Method: "GET",
		Path: "/repos/x", StatusCode: 200, Decision: "allow",
		Headers: map[string]string{"Authorization": "***", "Accept": "application/json"},
	}
	require.NoError(t, rec.Record(e,
		Body{Kind: "request", ContentType: "application/json", Size: 3, Stored: []byte("{}!")},
		Body{Kind: "response", ContentType: "image/png", Size: 9000, Sha256: "abc", Stored: nil},
	))

	list, err := rec.List(50)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "github", list[0].UpstreamName)

	got, bodies, err := rec.Get(list[0].ID)
	require.NoError(t, err)
	require.Equal(t, "***", got.Headers["Authorization"]) // round-trips masked
	require.Len(t, bodies, 2)

	_, _, err = rec.Get("nope")
	require.ErrorIs(t, err, ErrNotFound)

	n, err := rec.Prune(time.Now().Add(time.Hour)) // everything older than now+1h
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	list, _ = rec.List(50)
	require.Empty(t, list)
}

func TestMaskHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer secret")
	h.Set("X-Api-Key", "k")
	h.Set("Accept", "application/json")
	m := MaskHeaders(h)
	require.Equal(t, "***", m["Authorization"])
	require.Equal(t, "***", m["X-Api-Key"])
	require.Equal(t, "application/json", m["Accept"])
}
```

- [ ] **Step 2: run** → FAIL. **Step 3: implement** `recorder.go` (mirror prior registries:
  `newID()`, `time.RFC3339Nano`; `Get` joins `audit_bodies`; `Prune` deletes bodies via
  `DELETE FROM audit_bodies WHERE log_id IN (SELECT id FROM audit_log WHERE ts<?)` then the log
  rows) and the `MaskHeaders` + classification helpers in `capture.go`. **Step 4: run** → PASS.
  **Step 5: commit** `feat(audit): journal + body tables, recorder, header masking`.

---

### Task 2: capped streaming capture + proxy integration

**Files:** add to `internal/audit/capture.go`; create `internal/audit/capture_test.go`; modify `internal/proxy/proxy.go`, `internal/proxy/proxy_test.go`.

**Interfaces:**
- `audit.NewCapture(src io.ReadCloser, cap int, onClose func(stored []byte, total int64, truncated bool)) io.ReadCloser` — a tee: reads pass through; up to `cap` bytes are retained; `total` counts all bytes; `onClose` fires once on `Close` with the retained bytes + total + `truncated=(total>cap)`. `onClose` may be nil.
- `audit.ClassifyBody(kind, contentType string, stored []byte, total int64, truncated bool) Body` — builds a `Body`: if `contentType` is non-text, returns metadata-only (`Stored=nil`, `Size=total`, `Sha256=sha256(stored)` only when stored non-empty); if text, `Stored=stored`, `Size=total`, `Truncated=truncated`, `Sha256=sha256(stored)`.
- `proxy.Deps` gains `Audit *audit.Recorder` (nil-safe). New behavior: when `Audit != nil`, wrap `r.Body` with a capture before proxying; in `ReverseProxy.ModifyResponse`, wrap `resp.Body` with a capture whose `onClose` writes the audit row (status, duration since request start, both captured bodies, masked headers, the decision + rule from policy). In `ErrorHandler`, record a minimal entry (status 502, the error string). Also record the non-proxied early outcomes (deny → decision "deny" status 403; rate-limited → 429; approval denied → 403) — record those inline before returning.

- [ ] **Step 1: failing test** — `internal/audit/capture_test.go`:
```go
package audit

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCaptureTeesAndCaps(t *testing.T) {
	var gotStored []byte
	var gotTotal int64
	var gotTrunc bool
	c := NewCapture(io.NopCloser(strings.NewReader("hello world")), 5,
		func(stored []byte, total int64, truncated bool) {
			gotStored, gotTotal, gotTrunc = stored, total, truncated
		})
	out, err := io.ReadAll(c)
	require.NoError(t, err)
	require.Equal(t, "hello world", string(out)) // full body passes through
	require.NoError(t, c.Close())
	require.Equal(t, "hello", string(gotStored)) // capped at 5
	require.Equal(t, int64(11), gotTotal)
	require.True(t, gotTrunc)
}

func TestClassifyNonText(t *testing.T) {
	b := ClassifyBody("response", "image/png", []byte("\x89PNG"), 9000, true)
	require.Nil(t, b.Stored)            // non-text → no bytes
	require.Equal(t, int64(9000), b.Size)
	require.NotEmpty(t, b.Sha256)
	b2 := ClassifyBody("request", "application/json", []byte("{}"), 2, false)
	require.Equal(t, []byte("{}"), b2.Stored)
}
```

- [ ] **Step 2: run** `go test ./internal/audit/` → FAIL. **Step 3: implement** `NewCapture` +
  `ClassifyBody` (see the cappedCapture sketch below). **Step 4: run** → PASS. **Step 5: commit**
  `feat(audit): capped streaming body capture + classification`.

`cappedCapture` sketch:
```go
type cappedCapture struct {
	src       io.ReadCloser
	cap       int
	buf       []byte
	total     int64
	onClose   func([]byte, int64, bool)
	closeOnce sync.Once
}

func NewCapture(src io.ReadCloser, capBytes int, onClose func([]byte, int64, bool)) io.ReadCloser {
	return &cappedCapture{src: src, cap: capBytes, onClose: onClose}
}

func (c *cappedCapture) Read(p []byte) (int, error) {
	n, err := c.src.Read(p)
	if n > 0 {
		c.total += int64(n)
		if room := c.cap - len(c.buf); room > 0 {
			take := n
			if take > room {
				take = room
			}
			c.buf = append(c.buf, p[:take]...)
		}
	}
	return n, err
}

func (c *cappedCapture) Close() error {
	c.closeOnce.Do(func() {
		if c.onClose != nil {
			c.onClose(c.buf, c.total, c.total > int64(c.cap))
		}
	})
	return c.src.Close()
}
```

- [ ] **Step 6: proxy integration test** — add to `internal/proxy/proxy_test.go` (the `build()`
  helper must now also construct an `audit.NewRecorder` on the same store and return it):
```go
func TestProxyRecordsAudit(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer backend.Close()
	h, ag, up, pol, _, rec := buildWithAudit(t) // returns the *audit.Recorder too
	_, token, _ := ag.Register("claude")
	u, _ := up.Create("be", backend.URL, upstream.AuthConfig{Type: "static", Header: "Authorization", Token: "Bearer up"})
	_, _ = pol.Create(policy.Rule{UpstreamID: u.ID, Method: "*", PathGlob: "/**", Outcome: policy.Allow})

	r := httptest.NewRequest(http.MethodPost, "/be/things?x=1", strings.NewReader(`{"hi":1}`))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	require.Eventually(t, func() bool { l, _ := rec.List(10); return len(l) == 1 }, time.Second, 10*time.Millisecond)
	list, _ := rec.List(10)
	e := list[0]
	require.Equal(t, "POST", e.Method)
	require.Equal(t, "/things", e.Path)
	require.Equal(t, "x=1", e.Query)
	require.Equal(t, 200, e.StatusCode)
	require.Equal(t, "***", e.Headers["Authorization"]) // agent token masked
	_, bodies, _ := rec.Get(e.ID)
	require.Len(t, bodies, 2)
}
```

- [ ] **Step 7: implement** the proxy wiring (capture req body; `ModifyResponse` captures resp
  + records on close; `ErrorHandler` records a 502; inline record for deny/429/approval-denied).
  Pass `agent`/`upstream` names by looking them up (the proxy already has `ag` + `up`).
- [ ] **Step 8: run** `go test ./internal/proxy/ ./internal/audit/` → PASS.
- [ ] **Step 9: commit** `feat(proxy): record audit entries with captured bodies`.

---

### Task 3: daemon wiring + admin API + CLI

**Files:** Modify `internal/daemon/daemon.go`, `internal/daemon/admin.go`, `internal/daemon/admin_test.go`, `internal/cli/root.go`; create `internal/cli/audit.go`.

**Behavior:**
- `daemon.New` builds `audit.NewRecorder(store)` and passes it into `proxy.Deps.Audit`.
- Admin API: `GET /audit?limit=N` → list (no bodies); `GET /audit/{id}` → entry + bodies (stored
  text bodies decoded to string; non-text → `{content_type,size,sha256,truncated}` only);
  `POST /audit/prune {older_than_rfc3339}` → `{deleted:N}`.
- CLI: `outwall audit list [--limit 50]`, `outwall audit show <id>`, `outwall audit prune --older-than <dur|date>` (accept a Go duration like `720h` → now−dur, or an RFC3339 date).

- [ ] **Step 1: failing test** — extend `internal/daemon/admin_test.go`:
```go
func TestAdminAuditEmptyOK(t *testing.T) {
	d := newDaemon(t)
	h := d.AdminHandler()
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/vault/init", `{"password":"pw"}`).Code)
	require.Equal(t, http.StatusOK, req(t, h, "GET", "/audit", "").Code)
	require.Equal(t, http.StatusOK, req(t, h, "POST", "/audit/prune", `{"older_than_rfc3339":"2020-01-01T00:00:00Z"}`).Code)
	require.Equal(t, http.StatusNotFound, req(t, h, "GET", "/audit/nope", "").Code)
}
```

- [ ] **Step 2: run** → FAIL. **Step 3: implement** (mirror prior admin handlers + CLI).
  **Step 4: run** `go test ./...` → PASS.
- [ ] **Step 5: full gate + commit**
```bash
gofmt -l . ; go vet ./... ; go test ./... > /tmp/outwall_plan4.txt 2>&1 ; grep -E "FAIL|panic|^ok" /tmp/outwall_plan4.txt ; make build
git add -A && git commit -m "feat: audit admin API + CLI (list/show/prune)"
```
- [ ] **Step 6: e2e smoke** — proxy a request through a live daemon (vault unlocked via socket),
  then `outwall audit list` shows it, `outwall audit show <id>` shows masked headers + the
  captured JSON body. Note any limitation.

---

## Self-Review

- **Spec coverage:** per-request journal with all listed fields (Task 1) ✓; bodies ≤256 KB with
  truncation + total size (Task 2) ✓; non-text → metadata-only + sha256 (Task 2) ✓; injected/agent
  creds masked (Tasks 1–2) ✓; bodies in a separate table (Task 1) ✓; keep-all + manual prune
  (Tasks 1, 3) ✓; nil-safe so prior behavior unchanged when audit absent ✓.
- **Deferred:** audit auto-prune (Phase 2); SSE streaming of the audit tail (Plan 5); MCP-call
  audit (the control plane) — out of scope here, data-plane only, noted in ADR-0004.
- **Type consistency:** `audit.Recorder.Record(Entry, ...Body)`, `NewCapture(src,cap,onClose)`,
  `ClassifyBody(...)→Body`, `proxy.Deps.Audit`. The proxy records on response-body close.

## ADR + module docs (finalize)

ADR-0004 (implementer writes it): the audit design — streaming capped capture (no full-body
buffering; sha256 over stored bytes), text-vs-binary classification, masking policy, separate
bodies table, record-on-response-close timing + the error/early-outcome cases, data-plane-only
scope, keep-all + manual prune (auto-prune deferred). Module doc `audit.md` (new); update
`proxy.md`, `daemon.md`, `store.md`.
