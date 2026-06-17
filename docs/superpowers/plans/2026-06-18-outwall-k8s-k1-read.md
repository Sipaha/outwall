# K8s Gateway — Plan K1 (cluster targets + read access + streaming) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an agent read Kubernetes resources and stream logs/watches in granted
namespaces of a registered cluster, through the existing outwall data plane — without ever
holding the cluster's real credentials.

**Architecture:** A cluster is an `upstream.Upstream` of `Kind="k8s"` whose `BaseURL` is the
real API-server URL and whose encrypted `AuthConfig` carries the cluster CA + cluster auth
(token / client-cert / exec-plugin). The data-plane proxy, on a k8s-kind target, parses the
raw API path+method+query into the RBAC tuple `(namespace, resource[/subresource], verb)`,
evaluates the **extended** `policy` engine on that tuple, sets a per-cluster TLS transport,
and forwards. A local CA issues the data-plane server cert so kubectl validates honestly; a
helper prints the agent's kubeconfig.

**Tech Stack:** Go 1.26, `net/http/httputil.ReverseProxy`, `crypto/tls` + `crypto/x509`,
`modernc.org/sqlite`, `stretchr/testify`. No new heavy deps; **no** `k8s.io/client-go`
(we parse paths ourselves — client-go is a CGO-free but very large dep we don't need).

## Global Constraints

- Module path is **`github.com/Sipaha/outwall`** — exact, in every import.
- **No `citeck`** strings / imports / branding anywhere.
- **No CGO** in the server binary (`CGO_ENABLED=0`); SQLite via `modernc.org/sqlite`.
- **No panics / `log.Fatal`** in library code — return wrapped errors (`%w`). Panics only in `main`/tests.
- **No `Co-Authored-By`** in commits; **no `git commit --amend`** / no rewriting commits.
- New deps at `@latest`; don't bump existing deps.
- `gofmt` (tabs), `go vet` clean, `log/slog` logging, `stretchr/testify` tests. TDD.
- **Alpha:** changing the SQLite schema is fine (one-time DB reset); no legacy fallback path.
- Author every commit as `Sipaha <sipahabk@gmail.com>`.

---

### Task 1: k8s request-path parser (`internal/k8s` package)

**Files:**
- Create: `internal/k8s/requestinfo.go`
- Test: `internal/k8s/requestinfo_test.go`

**Interfaces:**
- Produces:
  ```go
  package k8s
  // RequestInfo is the RBAC-relevant decomposition of a k8s API request.
  type RequestInfo struct {
      IsResource  bool   // false for non-resource paths (/healthz, /version, /openapi/...)
      Namespace   string // "" = cluster-scoped or all-namespaces
      APIGroup    string // "" = core group
      Resource    string // e.g. "pods", "deployments"
      Subresource string // e.g. "log", "exec", "scale", "status" (else "")
      Name        string // resource name, "" for collections
      Verb        string // get|list|watch|create|update|patch|delete|deletecollection
  }
  // Parse decomposes a raw k8s API request (path already stripped of the /<cluster> prefix).
  func Parse(method, path string, query url.Values) RequestInfo
  ```

**Behavior (mirror kube-apiserver's RequestInfoResolver):**
- `/api/v1/...` → core group (`APIGroup=""`); `/apis/<group>/<version>/...` → `APIGroup=<group>`.
- `.../namespaces/<ns>/<resource>[/<name>[/<subresource>]]` → namespaced; the literal
  `namespaces` resource itself (`/api/v1/namespaces/<name>`) is cluster-scoped with
  `Resource="namespaces"`.
- No `namespaces/<ns>` segment after the version → cluster-scoped (`Namespace=""`).
- Verb: `GET` on collection ⇒ `list`, on a named resource ⇒ `get`; `?watch=true` (or
  `?follow=true` on the `log` subresource) ⇒ `watch`; `POST`⇒`create`; `PUT`⇒`update`;
  `PATCH`⇒`patch`; `DELETE` named ⇒ `delete`, `DELETE` collection ⇒ `deletecollection`.
- Non-`/api`,`/apis` paths (`/healthz`,`/version`,`/openapi/v2`,`/api`,`/apis`) ⇒
  `IsResource=false` (these are discovery/health; policy treats them separately, see Task 5).

- [ ] **Step 1:** Write a table-driven failing test `TestParse` covering, at minimum:
  `GET /api/v1/namespaces/prod/pods` → {list, ns=prod, resource=pods};
  `GET /api/v1/namespaces/prod/pods/web-1` → {get, name=web-1};
  `GET /api/v1/namespaces/prod/pods/web-1/log?follow=true` → {watch, subresource=log};
  `GET /api/v1/pods?watch=true` → {watch, ns="", resource=pods}  (all-namespaces watch);
  `PATCH /apis/apps/v1/namespaces/prod/deployments/web` → {patch, group=apps, resource=deployments};
  `DELETE /api/v1/namespaces/prod/pods` → {deletecollection};
  `GET /api/v1/nodes` → {list, ns="", resource=nodes} (cluster-scoped);
  `GET /api/v1/namespaces/prod` → {get, resource=namespaces, ns=""} (cluster-scoped self);
  `GET /healthz` → {IsResource:false}.
- [ ] **Step 2:** Run `go test ./internal/k8s/ -run TestParse -v` → FAIL (undefined).
- [ ] **Step 3:** Implement `Parse` per the behavior above (pure string/segment logic; no deps beyond `net/url`,`strings`).
- [ ] **Step 4:** `go test ./internal/k8s/ -run TestParse -v` → PASS.
- [ ] **Step 5:** Commit `feat(k8s): RBAC request-info parser`.

---

### Task 2: extend `upstream` with `Kind="k8s"` + cluster auth config

**Files:**
- Modify: `internal/store/migrate.go` (add `kind` column to `upstreams`; extend allowed columns)
- Modify: `internal/upstream/registry.go` (add `Kind` to `Upstream`; k8s fields to `AuthConfig`; persist/scan `kind`)
- Test: `internal/upstream/registry_test.go`

**Interfaces:**
- `upstream.Upstream` gains `Kind string` (`""`/`"http"` = http; `"k8s"` = cluster).
- `upstream.AuthConfig` gains (all `omitempty`):
  ```go
  // Kubernetes cluster connection (when the owning upstream Kind=="k8s"):
  CABundle    string `json:"ca_bundle,omitempty"`     // PEM, trusts the API server
  K8sAuth     string `json:"k8s_auth,omitempty"`      // token | client-cert | exec
  ClientCert  string `json:"client_cert,omitempty"`   // PEM (client-cert auth)
  ClientKey   string `json:"client_key,omitempty"`    // PEM
  ExecCommand string `json:"exec_command,omitempty"`  // exec auth: binary
  ExecArgs    []string `json:"exec_args,omitempty"`
  ExecEnv     map[string]string `json:"exec_env,omitempty"`
  ```
  (`Token` reuses the existing field for `K8sAuth=="token"`.)
- `Registry.Create` keeps its signature but also persists `Kind`; add
  `func (r *Registry) CreateKind(name, baseURL, kind string, auth AuthConfig) (*Upstream, error)`
  and have `Create` delegate with `kind="http"`. `GetByName`/list scan `kind`.

- [ ] **Step 1:** Failing test: create a `k8s` upstream with a CABundle + `K8sAuth="token"`,
  reload via `GetByName`, assert `Kind=="k8s"` and the decrypted `AuthConfig.CABundle`/`Token` round-trip.
- [ ] **Step 2:** `go test ./internal/upstream/ -v` → FAIL.
- [ ] **Step 3:** Add the `kind` column (`kind TEXT NOT NULL DEFAULT 'http'`) to the `upstreams`
  CREATE TABLE; add the struct fields; thread `kind` through Create/CreateKind/scan. (Alpha:
  existing dev DBs are reset — note it in the REPORT; do not write an ALTER fallback.)
- [ ] **Step 4:** `go test ./internal/upstream/ -v` → PASS.
- [ ] **Step 5:** Commit `feat(upstream): k8s cluster kind + cluster auth config`.

---

### Task 3: cluster auth — token / client-cert / exec, with transport + header seam

**Files:**
- Create: `internal/authn/k8s.go` (cluster transport + token providers)
- Create: `internal/authn/exec.go` (exec-plugin token provider + cache)
- Modify: `internal/authn/authn.go` / wherever `Manager` lives (add `Transport`)
- Test: `internal/authn/k8s_test.go`, `internal/authn/exec_test.go`

**Interfaces:**
- Extend `authn.Manager` (don't break existing methods):
  ```go
  // Transport returns the RoundTripper to reach this target's real backend.
  // For Kind=="k8s": a transport whose tls.Config trusts AuthConfig.CABundle and, for
  // client-cert auth, presents the client cert. For http upstreams: returns nil (proxy
  // uses the default transport). Cached per target like Authenticator, keyed on config fingerprint.
  func (mgr *Manager) Transport(up *upstream.Upstream) (http.RoundTripper, error)
  ```
- The header step for k8s: `Authenticator.Apply` injects `Authorization: Bearer <token>` where
  the token is the static `Token` (`K8sAuth=="token"`), the exec-plugin output (`"exec"`), or
  **absent** for `"client-cert"` (mTLS carries identity). Build these in `Manager.build` by
  branching on `up.Kind=="k8s"` + `AuthConfig.K8sAuth`.
- exec provider (`internal/authn/exec.go`):
  ```go
  // execTokenSource runs the operator-configured plugin and caches its short-lived token.
  type execTokenSource struct { /* command, args, env, mu, cached token+expiry */ }
  func (e *execTokenSource) Token(ctx context.Context) (string, error) // runs once, caches to expiry
  ```
  Parse the plugin's stdout as the k8s `ExecCredential` JSON
  (`{"status":{"token":"...","expirationTimestamp":"..."}}`); cache until ~30s before expiry;
  bounded `exec.CommandContext` timeout (e.g. 30s); **no agent-influenced argv/env** — only the
  operator's stored `ExecCommand/Args/Env` plus the inherited process env. On non-zero exit,
  return a wrapped error (never panic).

- [ ] **Step 1:** Failing tests:
  (a) `TestTransportK8sTrustsCA` — build a `Transport` for a k8s upstream whose CABundle is a
  self-signed test CA; assert the returned transport's `TLSClientConfig.RootCAs` verifies a cert
  signed by that CA (and rejects an unrelated cert).
  (b) `TestExecTokenSourceCachesUntilExpiry` — a fake plugin (a tiny shell/`go run` script, or
  inject a command that echoes a fixed `ExecCredential` JSON with a future expiry) is invoked
  **once** across two `Token()` calls; assert the token value + single invocation (use a temp
  file the script appends to as the call counter).
  (c) `TestK8sTokenAuthInjectsBearer` — `Apply` on a token-auth k8s target sets
  `Authorization: Bearer <token>`; client-cert target sets **no** Authorization header.
- [ ] **Step 2:** Run the three → FAIL.
- [ ] **Step 3:** Implement `k8s.go` (tls.Config from PEM CABundle + optional client cert →
  `&http.Transport{TLSClientConfig: ...}`), `exec.go` (ExecCredential runner+cache), and the
  `Manager.Transport` + k8s branch in `build`. Reuse the existing per-target cache map + fingerprint
  (extend `fingerprint` to include the new k8s fields).
- [ ] **Step 4:** Run all `./internal/authn/...` → PASS.
- [ ] **Step 5:** Commit `feat(authn): k8s cluster transport + token/client-cert/exec auth`.

---

### Task 4: extend `policy` to the k8s RBAC tuple

**Files:**
- Modify: `internal/policy/rule.go` (k8s fields on `Rule`), `internal/policy/decide.go`
  (k8s-aware match in `Input`/`Decide`)
- Modify: `internal/store/migrate.go` (k8s columns on `rules`)
- Modify: `internal/policy` registry load/save (persist/scan the new columns)
- Test: `internal/policy/decide_test.go`

**Interfaces:**
- `policy.Rule` gains (k8s rules; empty on http rules):
  ```go
  Namespace string // glob: "", "prod", "prod-*", "*"  (k8s rules)
  Resource  string // glob over "resource" or "resource/subresource", e.g. "pods", "pods/log", "deployments", "*"
  Verb      string // "", "*", or one verb: get/list/watch/create/update/patch/delete/deletecollection
  ```
- `policy.Input` gains `Kind string` + `Namespace, Resource, Subresource, Verb string`.
  When `Input.Kind=="k8s"`, `Decide` matches a rule by:
  `verbMatches(rule.Verb, in.Verb) && nsMatches(rule.Namespace, in.Namespace) &&
   resourceMatches(rule.Resource, in.Resource, in.Subresource)` — **instead of**
  `methodMatches + MatchGlob(path)`. The tier/precedence resolution (`resolveTier`,
  agent>any>default-deny, most-restrictive-wins) is reused **unchanged**.
- `nsMatches`: glob like the existing `MatchGlob` segment rules but over a single namespace
  token; **`in.Namespace==""` matches only `rule.Namespace=="*"`** (the all-namespaces / cluster-scoped safety property — a `prod` rule must NOT match an empty namespace).
- `resourceMatches`: compare against `resource` and, when a subresource is present, against
  `resource/subresource`; support `*` and `resource/*`.

- [ ] **Step 1:** Failing tests in `decide_test.go`:
  (a) a `(ns=prod, resource=pods, verb=list)` allow rule matches `Input{Kind:k8s, ns:prod, resource:pods, verb:list}` but NOT `verb:patch`, NOT `ns:staging`, NOT `ns:""`;
  (b) `(ns=prod, resource=pods/log, verb=get)` matches a `pods` `log` subresource get;
  (c) `(ns=*, resource=*, verb=get)` matches a cluster-scoped `nodes` get (ns="");
  (d) precedence: an agent-specific `deny` outranks an any-subject `allow` on the same tuple;
  (e) default-deny when nothing matches.
- [ ] **Step 2:** Run `./internal/policy/ -run TestDecideK8s -v` → FAIL.
- [ ] **Step 3:** Add fields/columns; implement the k8s match branch + `nsMatches`/`resourceMatches`/`verbMatches`; thread columns through load/save.
- [ ] **Step 4:** Run `./internal/policy/...` (incl. existing http tests) → PASS.
- [ ] **Step 5:** Commit `feat(policy): k8s (namespace, resource, verb) rule matching`.

---

### Task 5: wire k8s into the data-plane proxy (routing, parse, decide, transport, stream)

**Files:**
- Modify: `internal/proxy/proxy.go`
- Test: `internal/proxy/proxy_k8s_test.go`

**Interfaces (consumes Tasks 1–4):**
- In `ServeHTTP`, after `up, err := h.Upstreams.GetByName(name)`:
  - **http kind** → existing code path unchanged.
  - **k8s kind** →
    1. `ri := k8s.Parse(r.Method, relPath, r.URL.Query())`;
    2. for `ri.IsResource==false` (discovery/health like `/version`,`/api`,`/apis`,`/openapi`)
       — apply a fixed minimal policy: allow only if the agent has **any** allow rule on this
       cluster (kubectl needs discovery to function), else deny. (Keep it simple: allow GET
       discovery for any agent that holds ≥1 rule on the cluster.)
    3. `dec := h.Policy.Decide(policy.Input{AgentID: ag.ID, UpstreamID: up.ID, Kind: "k8s",
       Namespace: ri.Namespace, Resource: ri.Resource, Subresource: ri.Subresource, Verb: ri.Verb})`;
    4. same Deny / RequireApproval / rate-limit handling as today (approval.Pending should carry
       the k8s tuple for the UI — extend `approval.Pending` with optional `Namespace/Resource/Verb`
       used only for display; K2 makes mutating verbs use it);
    5. `rp.Transport = <from h.AuthManager.Transport(up)>` when non-nil;
    6. forward; **streaming**: ensure watch/log responses flush — `httputil.ReverseProxy`
       flushes immediately when `resp.ContentLength == -1` (k8s chunked streams); set
       `rp.FlushInterval = -1` to be explicit so `logs -f`/`-w` stream live.
  - The audit `Entry` for k8s records the parsed tuple in `Path`/new fields (reuse `Path`=relPath,
    `Query`; optionally set `Decision`, `RuleID` as today). Bodies captured as today (the 256 KB
    cap naturally bounds an unbounded log stream; the entry is written on stream close).
- Verb→audit method stays `r.Method` (raw); add the tuple to the log line via slog.

- [ ] **Step 1:** Failing integration-style test `proxy_k8s_test.go`: stand up an `httptest.Server`
  with TLS (its own cert) acting as the fake API server returning a pods list on
  `/api/v1/namespaces/prod/pods`; register a k8s upstream pointing at it with `CABundle` = the
  test server's CA and token auth; add an allow rule `(ns=prod,pods,list)`; drive a request
  through the proxy handler with a valid agent token at
  `/<cluster>/api/v1/namespaces/prod/pods`; assert 200 + body forwarded + the **agent's** bearer
  was replaced by the cluster token upstream (the fake server asserts it saw `Bearer <clustertok>`).
  Add a second case: `(ns=prod)` grant does NOT allow `/<cluster>/api/v1/namespaces/staging/pods`
  → 403.
- [ ] **Step 2:** Run `./internal/proxy/ -run K8s -v` → FAIL.
- [ ] **Step 3:** Implement the k8s branch in `ServeHTTP` + the transport wiring + FlushInterval.
- [ ] **Step 4:** Run `./internal/proxy/...` (full, incl. existing http tests) under `-race` → PASS.
- [ ] **Step 5:** Add a **streaming** test: fake API server streams 3 chunked log lines with a
  delay; assert the proxy delivers them incrementally (read with a deadline, see line 1 before
  the server writes line 3). PASS. Commit `feat(proxy): k8s routing, policy, transport, streaming`.

---

### Task 6: local CA + data-plane server cert + agent kubeconfig helper

**Files:**
- Create: `internal/k8s/kubeconfig.go` (assemble the agent kubeconfig YAML)
- Create: `internal/tlsca/ca.go` (local CA: load-or-create, issue server cert) — or fold into `internal/config`
- Modify: data-plane listener bootstrap (daemon) to serve TLS with the issued cert
- Modify: `internal/cli` — add `outwall kubeconfig <agent> <cluster>` and cluster CRUD verbs
- Test: `internal/k8s/kubeconfig_test.go`, `internal/tlsca/ca_test.go`

**Interfaces:**
- `tlsca`:
  ```go
  func LoadOrCreateCA(dir string) (*CA, error)          // persists ca.crt/ca.key under data-dir (0600)
  func (c *CA) ServerCert(hosts ...string) (tls.Certificate, error) // for 127.0.0.1 / localhost
  func (c *CA) CAPEM() []byte
  ```
- `k8s.Kubeconfig`:
  ```go
  func Kubeconfig(serverURL, clusterName, caPEM, agentToken string) ([]byte, error) // YAML bytes
  ```
- CLI: `outwall kubeconfig <agent-name> <cluster-name>` prints the YAML to stdout (looks up the
  agent's token + the local CA + the data-plane URL). `outwall cluster add|list|rm` for cluster
  CRUD (mirrors the existing `upstream`/`rule` CLI shape).

- [ ] **Step 1:** Failing tests: (a) `LoadOrCreateCA` is idempotent (second call loads the same CA);
  its `ServerCert("127.0.0.1")` verifies against `CAPEM()`. (b) `Kubeconfig(...)` emits valid YAML
  that round-trips through `clientcmd`-style parsing — or, to avoid a heavy dep, unmarshal with
  `yaml.v3` and assert `clusters[0].cluster.server`, `...certificate-authority-data` (base64 of
  caPEM), and `users[0].user.token` equal the inputs.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement `tlsca` (crypto/x509 self-signed CA, ECDSA P-256, persisted PEM,
  0600), `k8s.Kubeconfig` (yaml.v3), CLI verbs, and switch the data-plane listener to
  `http.Server` with `TLSConfig` using `CA.ServerCert(...)` (the HTTP data plane can keep plain
  HTTP for existing upstreams OR also move under TLS — keep both reachable; the k8s kubeconfig
  uses the `https://` URL). Wire CA dir = `config.DataDir()`.
- [ ] **Step 4:** Run all new tests + `make build` (CGO-free) → PASS.
- [ ] **Step 5:** Commit `feat(k8s): local CA, TLS data plane, kubeconfig + cluster CLI`.

---

### Task 7: MCP cluster discovery + kubeconfig tool

**Files:**
- Modify: `internal/mcpsvc/...` (the SDK-free service) + `internal/mcp` adapter
- Test: `internal/mcpsvc/..._test.go`

**Interfaces:**
- `list_upstreams` includes k8s targets with `kind:"k8s"` + the agent's per-cluster status.
- `request_access(target, purpose)` accepts `cluster` or `cluster/namespace` targets (the
  existing intent log records purpose unchanged).
- New MCP tool `get_kubeconfig(cluster)` returns the assembled kubeconfig YAML (via Task 6's
  `k8s.Kubeconfig`, the calling agent's token, the local CA).

- [ ] **Step 1:** Failing test: an agent session lists upstreams and sees a registered k8s
  cluster with `kind=k8s`; `get_kubeconfig(cluster)` returns YAML whose `users[0].user.token`
  equals the calling agent's token.
- [ ] **Step 2:** Run `./internal/mcpsvc/... -v` → FAIL.
- [ ] **Step 3:** Implement (reuse Task 6 `k8s.Kubeconfig`; do not duplicate assembly).
- [ ] **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit `feat(mcp): k8s cluster discovery + get_kubeconfig tool`.

---

### Task 8: ADR + docs + full gate

**Files:**
- Create: `docs/architecture/decisions/0008-k8s-gateway-read.md` (per `docs/workflow/adr-template.md`,
  status `accepted`, date 2026-06-18). Record: reverse-proxy model, cluster=k8s-kind upstream,
  transport seam extension (the §4.1 finding), namespace-safety property, exec-plugin trusted-input
  boundary + hardening, local-CA decision.
- Create/Modify: `docs/architecture/modules/k8s.md`, `tlsca.md`; update `authn.md`, `policy.md`,
  `upstream.md`, `proxy.md`, `mcpsvc.md` for the changed public API.
- **Do NOT** edit `docs/INDEX.md` / `docs/roadmap/current-phase.md` (the supervisor finalizes those).

- [ ] **Step 1:** Write ADR-0008 + module docs.
- [ ] **Step 2:** Full gate from the project dir:
  `make fmt && make vet && go test ./... -race > /tmp/k1.txt 2>&1; grep -E "FAIL|panic|^ok|^---" /tmp/k1.txt && make build`.
  All green; binary CGO-free.
- [ ] **Step 3:** Commit `docs(k8s): ADR-0008 + module docs for the k8s read gateway`.

## Self-Review (done while writing)

- **Spec coverage:** §2 model→Task 5; §3 routing→Task 5; §4 cluster target + §4.1 transport
  seam→Tasks 2,3; §5 policy + parser + namespace safety→Tasks 1,4; §6 auth incl. exec→Task 3;
  §7 kubeconfig + local CA→Task 6; §8 streaming→Task 5; §9 MCP→Task 7; §10 audit→Task 5 (reuses
  Phase-1 recorder); ADR→Task 8. (Mutating/approval = K2; exec/attach = K3 — out of this plan.)
- **Namespace-safety** (`in.Namespace==""` matches only `*`) is explicitly tested in Tasks 4 & 5.
- **Type consistency:** `RequestInfo`/`policy.Input` k8s fields, `Manager.Transport`,
  `k8s.Kubeconfig`, `tlsca.CA` names are used identically across tasks.
