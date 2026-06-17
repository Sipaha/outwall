# ADR-0009: Kubernetes gateway — mutating verbs + approval (Plan K2)

- **Status:** accepted
- **Date:** 2026-06-18

## Context

Plan K1 (ADR-0008) routed Kubernetes requests through the policy engine and the existing
blocking approval queue, but only delivered the read half (get/list/watch + log/watch
streaming). K2 makes the **mutating verbs** (create/update/patch/delete/deletecollection)
first-class: an agent may change workloads in a granted namespace, with every mutating call
gated by the operator and the approval card showing **exactly what will change** (the request
body — for a `patch`, the body *is* the change).

Forces shaping the implementation:

- The data plane already carries the transport (K1's reverse proxy + per-cluster TLS); no new
  transport work is needed. What was missing is surfacing the agent-sent body to the operator
  *before* the request is forwarded.
- The audit body capture (`audit.NewCaptureRef`) is a streaming tee read *after* the proxy
  decision, in `ModifyResponse`'s `onClose`. The approval card needs the body **before**
  `approval.Submit`, so the same stream cannot simply be reused at the existing point — the
  body must be read once, up front, for a mutating-verb approval.
- The previewed body must never leak a credential. The agent's body is captured *before* the
  cluster credential is injected (injection happens in the proxy `Rewrite` step), so the
  cluster credential is structurally absent; but an agent could embed its own secret in the
  body, so the surfaced preview is additionally **masked**.

## Decision

**Mutating verbs gate on the existing blocking approval queue.** No new mechanism: a k8s
rule with `verb ∈ {create,update,patch,delete,deletecollection}` and outcome
`require-approval` parks the request in `approval.Submit` exactly as the HTTP path does. On
approve it proceeds and the agent receives the **real** API response; on deny it returns
`403` and the upstream is **never** called. K1's generic `verbMatches` already accepts these
verbs, so no policy change was required — the plan's "validate mutating verbs" reduces to the
verbs already being recognized by `internal/k8s.Parse` + matched by `policy.Decide`.

**Body capture for the approval card.** In the proxy, when the decision is `require-approval`
**and** the target is a k8s cluster **and** the verb is mutating **and** a body is present,
the proxy reads the request body once into memory, stores a `audit.BodyCap`-capped, masked
copy on the new `approval.Pending.RequestBody` field, and replaces `r.Body` with a reader over
the **full** bytes so the proxy forwards the complete payload and the audit tee re-reads the
same body downstream. The forwarded body is never truncated (only the preview is capped); the
underlying stream is read exactly once.

**API + SSE surface.** `GET /approvals` and the `approval.enqueued` SSE event both carry the
parsed tuple (`namespace`, `resource`, `verb`) and a `request_body` preview produced by
`audit.MaskBody` — a pure string masker that redacts `Bearer <token>` literals and JSON-ish
`"…(authorization|token|secret|api-key|password)…": "…"` values. The injected cluster
credential is structurally absent (captured pre-injection); the masker is defense-in-depth
against an agent-embedded secret.

**Web console.** The Rules editor renders namespace/resource/verb inputs (verb a `<select>`
over the RBAC verbs) when the selected upstream's `kind == "k8s"`, sending the tuple instead
of method+path. The Approvals card renders the tuple + the masked patch body (`<pre>`).

## Alternatives considered

- **Reuse the existing audit tee in place (read body only at `ModifyResponse`)** — rejected:
  the approval card must show the body *before* the proxy forwards, which is strictly earlier
  than the tee's `onClose`. A mutating-verb approval reads the body up front instead.
- **A separate "k8s approval" queue / event** — rejected: the Phase-1 blocking queue
  generalizes one-to-one; only a new display field (`RequestBody`) was added. A parallel
  mechanism would duplicate timeout/cancel/resolve logic.
- **Surface the raw body unmasked** ("it can't contain the cluster credential anyway") —
  rejected: the operator console is the one place the body is shown verbatim; masking an
  agent-embedded secret there is cheap insurance and matches the Phase-1 masking posture.

## Consequences

- An operator can safely allow an agent to mutate workloads: the change is shown, gated, and
  audited (the `patch` body is captured by the existing audit path as well).
- `approval.Pending` gained `RequestBody []byte`; `internal/approval` now imports
  `internal/audit` for `MaskBody` (no import cycle — audit does not import approval).
- The approvals admin DTO changed from `map[string]string` to `map[string]any` to carry the
  body and tuple. Alpha: no compat shim.
- K3 will add Upgrade (WebSocket/SPDY) proxying for exec/attach/port-forward, with
  approval-on-upgrade reusing this same queue and metadata-only audit (ADR-0010).
- Non-mutating k8s approvals (a `require-approval` rule on a read verb) still park, but carry
  no `RequestBody` (read requests have no meaningful body to preview).
