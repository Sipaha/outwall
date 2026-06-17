# K8s Gateway — Plan K2 (mutating verbs + approval) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Let an agent change workloads (create/update/patch/delete) in granted namespaces,
with every mutating call gated by the existing **blocking operator approval**, and the
approval card showing exactly what will change.

**Architecture:** K1 already routes k8s requests through the policy engine and the approval
queue; K2 makes mutating verbs first-class: rules with mutating verbs, an approval card
enriched with the parsed tuple + the request-body (patch) diff, and audit that highlights the
applied change. No new transport work — K1's data plane carries it.

**Tech Stack:** Go 1.26; reuses `internal/approval`, `internal/policy`, `internal/k8s`,
`internal/proxy`, the React console. No new deps.

## Global Constraints

Same as Plan K1 (module path `github.com/Sipaha/outwall`, no citeck, CGO_ENABLED=0 server,
no panics in lib code, no Co-Authored-By/amend, new deps @latest, TDD, alpha schema-reset OK,
commit author `Sipaha <sipahabk@gmail.com>`).

---

### Task 1: mutating-verb rules + approval tuple plumbing

**Files:** Modify `internal/policy` (validate mutating verbs), `internal/approval/queue.go`
(carry the k8s tuple already added in K1), `internal/proxy/proxy.go` (pass the tuple to
`approval.Pending` for k8s mutating decisions). Test in the respective `_test.go`.

**Interfaces (consumes K1):** `policy.Input{Kind:"k8s", Verb:"patch"|"update"|"create"|"delete"|"deletecollection", ...}`;
`approval.Pending` carries `Namespace, Resource, Subresource, Verb string` (display-only) plus,
new in K2, `RequestBody []byte` (the captured patch/apply body, capped at `audit.BodyCap`) so the
operator sees the change. The blocking `Submit(ctx, Pending) (bool, error)` contract is unchanged.

- [ ] **Step 1:** Failing test: a k8s request with verb `patch` matching a `require-approval`
  rule blocks on `approval.Submit`; on approve it proceeds and the upstream sees the patch;
  on deny it returns 403 and the upstream is **not** called. Assert the `Pending` handed to the
  queue carries `Verb=="patch"`, the right `Namespace/Resource`, and the patch body bytes.
- [ ] **Step 2:** Run `./internal/proxy/ -run K8sApproval -v` → FAIL.
- [ ] **Step 3:** Capture the request body before forwarding (reuse K1's `audit.NewCaptureRef`
  tee so the body is read once for both approval display and audit), populate `Pending`, and gate
  the proxy on the approval result for mutating k8s verbs exactly as the existing http path does.
- [ ] **Step 4:** Run under `-race` → PASS.
- [ ] **Step 5:** Commit `feat(k8s): mutating verbs gated by blocking approval`.

---

### Task 2: approval API + SSE event carry the k8s tuple + patch body

**Files:** Modify the approvals admin handler + the approval SSE event payload (`internal/daemon`
or wherever `approval.enqueued` is published) to include `namespace/resource/verb` + a
size-capped, masked `request_body` preview. Test the handler JSON.

- [ ] **Step 1:** Failing test: the approvals list endpoint returns a pending k8s approval whose
  JSON includes `namespace`,`resource`,`verb`, and a `request_body` preview (masked of any
  `Authorization`); the `approval.enqueued` SSE event carries the same.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Extend the DTO + event publisher (do not leak the cluster credential; only the
  agent-sent body is shown).
- [ ] **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit `feat(api): expose k8s tuple + patch body on approvals`.

---

### Task 3: Web UI — k8s-aware Rules editor + enriched approval card

**Files:** Modify the React Rules screen (namespace/resource/verb inputs when the target is a
cluster) and the Approvals card (show namespace/resource/verb + the patch body, syntax-plain).
Vitest for the new rendering + a Playwright smoke (optional, supervisor verifies live).

- [ ] **Step 1:** Vitest: the approval card renders `ns/resource/verb` and the patch body for a
  k8s approval fixture; the Rules editor shows k8s fields when the selected target kind is `k8s`.
- [ ] **Step 2:** Run `pnpm test` → FAIL.
- [ ] **Step 3:** Implement; keep the existing Darcula/Lens theme + Zustand store patterns.
- [ ] **Step 4:** `pnpm test` + `pnpm lint` → PASS; `make build-desktop` embeds the rebuilt web bundle.
- [ ] **Step 5:** Commit `feat(ui): k8s rules editor + patch-diff approval card`.

---

### Task 4: ADR + docs + full gate

**Files:** Create `docs/architecture/decisions/0009-k8s-mutate-approval.md` (status accepted,
date when implemented); update `approval.md`, `proxy.md`, `policy.md`, `webui` module docs.
**Do NOT** touch `docs/INDEX.md` / `docs/roadmap/current-phase.md`.

- [ ] **Step 1:** Write ADR-0009 + module docs.
- [ ] **Step 2:** Full gate: `make fmt && make vet && go test ./... -race` (capture to file, grep)
  `&& (cd web && pnpm test && pnpm lint) && make build && make build-desktop`. All green.
- [ ] **Step 3:** Commit `docs(k8s): ADR-0009 + module docs for mutating verbs + approval`.

## Self-Review

Covers spec §1(b), §8 (approval/mutating), §10 (patch body = the change, masked). exec/attach
(§8 upgrade) is K3. Namespace-safety inherited from K1's policy. Approval reuses the Phase-1
blocking queue — no parallel mechanism.
