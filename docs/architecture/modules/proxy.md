# module: internal/proxy

The data plane: a localhost reverse proxy for `<METHOD> /<upstream>/<rest...>`. It halts when
the vault is locked (503), authenticates the calling agent's token (401) — from the `Authorization:
Bearer` header **or** an `outwall_token` cookie (the cookie lets a real browser, e.g. Playwright
opening an OIDC-protected site through the data plane, carry the token automatically; it is stripped
before forwarding, ADR-0033) — resolves the upstream by name (404), then evaluates the policy engine
on the upstream-relative path
(`/<rest>`):

- `deny` → 403 `access denied`.
- `require-approval` → blocks on `approval.Submit`; approved ⇒ continue, denied/timeout ⇒ 403, ctx-canceled ⇒ 504.
- `allow` → continue.

If the matched rule sets a rate limit, the in-memory `policy.Limiter` is consulted (keyed by
`agentID|ruleID`); over the limit ⇒ 429. It then strips the agent's `Authorization` **and the
`outwall_token` cookie** (other cookies pass through), applies the upstream authenticator obtained
from `authn.Manager` (so OIDC tokens are cached across requests), and forwards via
`httputil.ReverseProxy` (`Host` rewritten to the upstream, query preserved).

**H1 (operation enforcement — HTTP).** For an HTTP upstream the proxy builds
`policy.Input{Method, Path: <escaped upstream-relative path>, Query: r.URL.Query()}` and calls
`Decide` (ADR-0014). The path is taken from `r.URL.EscapedPath()` so a `%2F` inside one segment is
preserved (the operation-template matcher splits on real `/` only). A `require-approval` outcome
carrying `NewValues` (a not-yet-allowed text value) puts the matched rule id + the `(var,value)`
pairs + the template on `approval.Pending`; on approve the proxy calls
`policy.Registry.AddAllowedValue` for each pair — **extending the rule's value-set** — then the
request proceeds; on deny → 403. No template match → 403 (default-deny). The host upstream is
resolved by the first path segment as the host name (lazy host creation is H2).

**K1 (k8s clusters).** When the resolved target is `Kind=="k8s"`, the proxy parses the request
into the RBAC tuple (`k8s.Parse`) and evaluates `policy.Decide` with `Kind:"k8s"` +
namespace/resource/subresource/verb instead of method+path. Discovery/health paths
(`IsResource==false`, e.g. `/version`, `/api`, `/openapi/...`) are allowed for any agent that
holds ≥1 grant on the cluster (kubectl needs them), else denied. The per-cluster TLS transport
from `authn.Manager.Transport` is attached, and `FlushInterval=-1` is set so `logs -f` / `-w`
stream incrementally. The `approval.Pending` carries the k8s tuple (Namespace/Resource/Verb)
for the UI.

**K2 (mutating verbs gated by approval).** For a k8s mutating verb
(create/update/patch/delete/deletecollection) whose rule resolves to `require-approval`, the
proxy reads the agent's request body once **before** `approval.Submit`, stores a
`audit.BodyCap`-capped copy on `Pending.RequestBody` (the patch = the change the operator
sees), and replaces `r.Body` with a reader over the **full** bytes so the forwarded payload is
never truncated and the audit tee re-reads the same body — the underlying stream is read
exactly once. On approve the request proceeds and the agent gets the real API response; on
deny it returns 403 and the upstream is **not** called. The injected cluster credential is
added later (the `Rewrite` step), so it is never part of the captured/previewed body.

**Body variables (ADR-0020).** For non-k8s body-bearing methods (POST/PUT/PATCH/DELETE) the
proxy reads the request body once **before** `Decide`, passes it as `policy.Input.Body` (so the
policy engine can extract + gate JSON body variables), and restores `r.Body` over the same bytes
so the upstream and audit tee re-read the original payload. GET/HEAD bodies are never read. The
upstream credential is applied in `Rewrite`, so it is never part of the gated/forwarded body.

**K3 (exec / attach / cp / port-forward — connection upgrades).** When `ri.IsUpgrade()` (pod
subresources `exec`/`attach`/`portforward`; `cp` rides on `exec`), the request is an HTTP
connection upgrade carrying a duplex stream. Policy is evaluated with verb `create` +
`resource/subresource` (so `(ns, pods/exec, create)` grants it); `deny` ⇒ `403` **before** any
upgrade and `require-approval` blocks on `approval.Submit` **before** the `101`, same queue as
K2. On allow/approve the K1 transport is attached and `httputil.ReverseProxy`'s built-in
`Upgrade` handling performs the handshake and copies bytes both ways — outwall does **not**
interpret the WebSocket/SPDY stream. Body capture is **bypassed** for upgrades (a
`captureBodies = auditRecording && !isUpgrade` gate): installing `ModifyResponse` on a `101`
corrupts the duplex stream (`ReverseProxy` errors with a non-writable body ⇒ `502`). See
ADR-0010.

## Audit (optional)

When `Deps.Audit != nil` the handler records each request to the audit journal (see `audit.md`
and ADR-0004). The inbound request body is wrapped with a capped capture before forwarding; the
upstream response body is wrapped in `ModifyResponse`, and the audit row is written from that
capture's `onClose` — after the response has fully streamed to the agent. The entry carries the
agent/upstream id+name, method, upstream-relative path, query, status, duration, req/resp sizes,
the decision + matched rule id, the masked request headers, and both captured bodies. For an
HTTP operation request it also records the matched operation template (`Operation`) and the
variable values outwall extracted from the real request (`Vars`). Upstream
transport failures are recorded via the `ErrorHandler` as a `502`. The early policy outcomes are
recorded inline before returning: `deny → 403`, approval-denied `→ 403`, rate-limited `→ 429`. The
pre-policy guards (`401`/`404`/`503`) are not recorded. When `Audit == nil` the proxy behaves
exactly as in Plans 1–3 (no capture, no recording).

**Interactive session metadata (K3).** For an audited upgrade the proxy wraps the
`ResponseWriter` in a `hijackAuditWriter` (`upgrade.go`); when `ReverseProxy` hijacks the
client connection it returns a `countingConn` that tallies bytes each way and fires once on
`Close`. That writes a single metadata `audit.Entry` — cluster (`UpstreamName`), namespace +
pod (in `Path`), command + container (in `Query`), `101` status, duration, `ReqBytes`/
`RespBytes`, decision, masked headers — and **no** body blob. If the upgrade never completes
(no `101`, no hijack) no session record is written. See ADR-0010.

## Public API

- `Deps struct { Agents *agent.Registry; Upstreams *upstream.Registry; Policy *policy.Registry; Limiter *policy.Limiter; Approvals *approval.Queue; AuthManager *authn.Manager; Vault *secret.Vault; Audit *audit.Recorder; Logger *slog.Logger }` (`Audit` optional/nil-safe).
- `New(d Deps) http.Handler` — builds the data-plane handler (defaults `Logger` to `slog.Default()`).
