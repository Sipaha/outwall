# Operation-Access — Plan H3 (UI) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Give the operator the UI to control operation access: host approval (+ attach credential),
operation approval cards (form, typed variables, concrete example URL, broad-placeholder warning,
approve / approve+trust-any / deny), the data-plane new-value card, and an Operations screen listing
templates with their value-sets (add/remove values, trust-any toggle).

**Architecture:** Evolve the embedded React console (the Approvals / Rules / Upstreams screens) to the
operation model, consuming the H2 admin API + SSE. The HTTP "Rules" screen becomes an "Operations"
view of templates + value-sets; "Upstreams" becomes the host list with credential status.

**Tech Stack:** React 19 / Vite / Tailwind 4 / Zustand / Vitest (the existing console). No new deps.

## Global Constraints

Same as H1/H2 (no citeck; CGO-free server unaffected; no Co-Authored-By/amend; deps at latest only if
truly needed; TDD via Vitest; author `Sipaha <sipahabk@gmail.com>`). After `make build-desktop`,
restore `internal/daemon/webdist/index.html`. **Don't** edit INDEX / current-phase.

---

### Task 1: approval cards — host, operation, new-value

**Files:** Modify `web/src/pages/Approvals.tsx` (+ `.test.tsx`), `web/src/lib/types.ts`/`api.ts`.

**Behavior:** render three approval kinds:
- **host**: agent + host + purpose; Approve (with an optional credential field: auth type + token) / Deny.
- **operation**: agent + purpose + the operation form with fixed vs `{variable:type}` segments
  visually distinct + a **concrete example URL** built from the requested values; per-text-variable a
  "trust any value" checkbox; a **warning banner** when a variable is `any`/broad. Approve / Deny.
- **new-value**: agent + the operation template + the new `(variable, value)`; Approve / Approve+trust-any / Deny.

- [ ] **Step 1:** Vitest: each card kind renders its fields from a fixture (operation card shows the
  example URL + the trust-any checkbox + the broad-placeholder warning when a var is `any`); approve
  posts the resolve with the chosen `trust_any` vars.
- [ ] **Step 2:** `pnpm test` → FAIL.
- [ ] **Step 3:** Implement the three cards; keep the Darcula/Lens theme + Zustand/api patterns.
- [ ] **Step 4:** `pnpm test` + `pnpm lint` → PASS.
- [ ] **Step 5:** Commit `feat(ui): host / operation / new-value approval cards`.

---

### Task 2: Operations screen (templates + value-sets) + Hosts list

**Files:** Modify `web/src/pages/Rules.tsx` → Operations (+ `.test.tsx`), `web/src/pages/Upstreams.tsx`
→ Hosts (+ test), `web/src/App.tsx`/`Sidebar.tsx` labels.

**Behavior:** Operations lists each host's operation templates with, per text variable, its allowed
value-set (add/remove a value, toggle "any"); date vars shown as auto. Hosts lists registered hosts
with credential status (set / none) and lets the operator set/replace the credential and remove a host.

- [ ] **Step 1:** Vitest: Operations renders a template fixture with its value-sets and supports
  add/remove + trust-any toggle (posts to the API); Hosts shows credential status.
- [ ] **Step 2:** `pnpm test` → FAIL.
- [ ] **Step 3:** Implement; reuse DataTable/FormField/Select/Modal.
- [ ] **Step 4:** `pnpm test` + `pnpm lint` → PASS; `make build-desktop` embeds the rebuilt bundle
  (then restore `webdist/index.html`).
- [ ] **Step 5:** Commit `feat(ui): Operations (templates + value-sets) + Hosts screens`.

---

### Task 3: ADR + docs + full gate

**Files:** `docs/architecture/decisions/0016-operation-access-ui.md` (accepted, date when implemented);
update `webui` module doc. **Don't** edit INDEX / current-phase.

- [ ] **Step 1:** ADR-0016 + webui doc.
- [ ] **Step 2:** Full gate: `make fmt && make vet && go test ./... -race` (file+grep) `&&
  (cd web && pnpm test && pnpm lint) && make build && make build-desktop` (restore index.html). All green.
- [ ] **Step 3:** Commit `docs(opaccess): ADR-0016 + webui doc for the operation-access UI`.

## Self-Review

Covers spec §4 (host/operation/new-value cards + broad-placeholder warning + example URL) and the
Operations screen (templates + value-sets, add/remove, trust-any). Evolves the existing screens; no
new dep; server stays CGO-free.
