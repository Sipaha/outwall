# K8s Gateway — Plan K3 (exec / attach / cp / port-forward) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Let an agent run interactive workloads operations — `kubectl exec`, `attach`, `cp`
(which is exec+tar), and `port-forward` — through outwall, policy-gated and approval-capable,
with metadata audit of the session.

**Architecture:** These use HTTP connection **upgrade** (modern k8s = WebSocket
`*.channel.k8s.io`; older = SPDY/3.1). `httputil.ReverseProxy` already proxies `Upgrade`/101
bidirectional streams, so the data path largely reuses K1's proxy + per-cluster transport. The
work is: recognise the upgrade subresources in policy/parse, gate them (incl. approval-on-upgrade),
bypass the request/response **body capture** on 101 (it's a duplex stream, not req/resp bodies),
and write a **metadata** audit record (who/cluster/ns/pod/container/command/start-end/bytes).

**Tech Stack:** Go 1.26 stdlib (`httputil.ReverseProxy` upgrade support, `net/http` hijack).
No new deps; do **not** pull in `client-go`'s remotecommand — we proxy the bytes, not interpret them.

## Global Constraints

Same as Plan K1 (module path, no citeck, CGO_ENABLED=0 server, no panics in lib code, no
Co-Authored-By/amend, deps @latest, TDD, alpha schema-reset OK, author `Sipaha <sipahabk@gmail.com>`).

---

### Task 1: parse + classify exec/attach/portforward subresources

**Files:** Modify `internal/k8s/requestinfo.go` (already returns `Subresource`); add a helper
`func (ri RequestInfo) IsUpgrade() bool` (true for subresources `exec`,`attach`,`portforward`;
note `cp` rides on `exec`). Test in `requestinfo_test.go`.

- [ ] **Step 1:** Failing test: `POST /api/v1/namespaces/prod/pods/web-1/exec?command=sh` →
  `{resource:pods, subresource:exec}` with `IsUpgrade()==true`; `.../portforward` likewise;
  a plain `pods/log` is **not** an upgrade.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement `IsUpgrade()` + ensure verb for exec/attach/portforward maps sensibly
  (treat as verb `create` per k8s RBAC, which is what kube-apiserver uses for these subresources).
- [ ] **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit `feat(k8s): classify exec/attach/portforward upgrade subresources`.

---

### Task 2: proxy — gate + forward the upgrade, skip body capture

**Files:** Modify `internal/proxy/proxy.go`. Test `internal/proxy/proxy_k8s_exec_test.go`.

**Interfaces (consumes K1/K2):** for a k8s request where `ri.IsUpgrade()`:
- evaluate policy with verb `create` + `resource/subresource` (so a rule
  `(ns=prod, resource=pods/exec, verb=create)` grants it; `require-approval` blocks the upgrade
  via `approval.Submit` **before** the 101, same as any verb);
- set `rp.Transport` to the cluster transport (K1) — `ReverseProxy` performs the upgrade
  handshake and streams bidirectionally;
- **do not** install `ModifyResponse` body capture for a 101 response (it would corrupt the
  duplex stream) — branch the audit so on upgrade we record metadata only.

- [ ] **Step 1:** Failing test: a fake API server that accepts a WebSocket upgrade on
  `/api/v1/namespaces/prod/pods/web-1/exec` and echoes a byte; with an allow rule
  `(ns=prod, pods/exec, create)`, the proxy completes the upgrade and the echoed byte reaches the
  client; with no rule → 403 **before** upgrade. (Use `golang.org/x/net/websocket` or
  `nhooyr.io/websocket`/`coder/websocket` **only in the test** if needed, or hand-roll a minimal
  `Upgrade` handshake with `httptest` + raw hijack to avoid a runtime dep.)
- [ ] **Step 2:** Run `./internal/proxy/ -run K8sExec -v` → FAIL.
- [ ] **Step 3:** Implement the upgrade branch (policy gate + approval + transport + skip capture).
- [ ] **Step 4:** Run under `-race` → PASS.
- [ ] **Step 5:** Commit `feat(proxy): proxy k8s exec/attach/portforward upgrades, gated`.

---

### Task 3: metadata audit for interactive sessions

**Files:** Modify `internal/audit` (a session/metadata entry: no body, but
pod/container/command/duration/bytes-in-out) + the proxy upgrade branch to emit it on session
close. Test `internal/audit/..._test.go` + a proxy assertion.

- [ ] **Step 1:** Failing test: after an exec session closes, an audit entry exists with the
  cluster, namespace, pod (`web-1`), the `command` query value(s), a non-zero duration, and
  byte counters; **no** request/response body blob is stored.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement a counting wrapper around the hijacked conn (bytes in/out) and record
  on close; mask nothing agent-sent except never store the cluster credential.
- [ ] **Step 4:** Run under `-race` → PASS.
- [ ] **Step 5:** Commit `feat(audit): metadata records for k8s exec/attach sessions`.

---

### Task 4: ADR + docs + full gate

**Files:** Create `docs/architecture/decisions/0010-k8s-exec-attach.md` (status accepted, date
when implemented); update `proxy.md`, `audit.md`, `k8s.md`. **Do NOT** touch
`docs/INDEX.md` / `docs/roadmap/current-phase.md`.

- [ ] **Step 1:** Write ADR-0010 + module docs (record: upgrade proxying reuses ReverseProxy;
  body-capture bypass on 101; metadata-only audit rationale; SPDY-vs-WebSocket note — modern
  clusters negotiate WebSocket; if a target cluster only speaks SPDY, ReverseProxy's generic
  `Upgrade` hijack still streams it).
- [ ] **Step 2:** Full gate: `make fmt && make vet && go test ./... -race` (file+grep) `&& make build && make build-desktop`. All green.
- [ ] **Step 3:** Commit `docs(k8s): ADR-0010 + module docs for exec/attach/port-forward`.

## Self-Review

Covers spec §8 (exec/attach/cp/port-forward, upgrade transport, approval-on-upgrade,
metadata audit). Reuses K1 transport + K2 approval. No body capture on duplex streams (the one
real divergence from the req/resp model — recorded in the ADR). No `client-go` runtime dep.
