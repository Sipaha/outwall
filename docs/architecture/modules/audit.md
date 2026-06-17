# module: internal/audit

The request/response journal for the data plane (see ADR-0004). Owns two SQLite tables —
`audit_log` (the fast-to-list journal) and `audit_bodies` (a separate body store) — plus a
`Recorder` and the streaming-capture/masking/classification helpers.

Bodies are captured by a **capped streaming tee** (`NewCapture` / `NewCaptureRef`): reads pass
through to the consumer unchanged, at most `BodyCap` (256 KiB) bytes are retained, a `total`
counts every byte, and `truncated = total > cap`. No full body is buffered. `ClassifyBody` stores
bytes only for textual Content-Types (`text/*`, `application/json`, `application/xml`,
`application/x-www-form-urlencoded`, `application/*+json`, `application/*+xml`, or empty
Content-Type on a non-empty body); otherwise it keeps metadata only (`Stored = nil`). `sha256` is
computed over the **stored** bytes only. `MaskHeaders` flattens an `http.Header` to a single-value
map with `Authorization`, `Proxy-Authorization`, `Cookie`, `Set-Cookie`, and any
`api-key`/`apikey`/`token`/`secret` header value replaced by `"***"`.

The proxy records on response-body close (data plane only); see `proxy.md` and ADR-0004 for
timing and early-outcome handling.

## Public API

- `BodyCap = 256 * 1024`; `KindRequest = "request"`, `KindResponse = "response"`; `ErrNotFound`.
- `Entry struct { ID; TS time.Time; AgentID, AgentName, UpstreamID, UpstreamName, Method, Path, Query string; StatusCode, DurationMs int; ReqBytes, RespBytes int64; Decision, RuleID, Error string; Headers map[string]string }`.
- `Body struct { Kind, ContentType string; Size int64; Sha256 string; Truncated bool; Stored []byte }`.
- `NewRecorder(s *store.Store) *Recorder`.
- `(*Recorder).Record(e Entry, bodies ...Body) error` — assigns `ID`/`TS` if empty.
- `(*Recorder).List(limit int) ([]Entry, error)` — newest first, no bodies (`limit ≤ 0 → 50`).
- `(*Recorder).Get(id string) (Entry, []Body, error)` — with bodies; `ErrNotFound` if absent.
- `(*Recorder).Prune(olderThan time.Time) (int64, error)` — delete rows with `ts < olderThan` (+ their bodies); returns count.
- `MaskHeaders(h http.Header) map[string]string`.
- `NewCapture(src io.ReadCloser, capBytes int, onClose func([]byte, int64, bool)) io.ReadCloser`.
- `NewCaptureRef(src io.ReadCloser, capBytes int) (io.ReadCloser, *Capture)`; `(*Capture).Captured() ([]byte, int64, bool)`.
- `ClassifyBody(kind, contentType string, stored []byte, total int64, truncated bool) Body`.
