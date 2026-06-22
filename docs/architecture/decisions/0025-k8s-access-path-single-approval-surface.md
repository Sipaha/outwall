# ADR-0025: k8s access request path + single approval surface

- **Status:** accepted
- **Date:** 2026-06-22

## Context

The first live agent↔outwall test (read pod logs of `ecos-model` in namespace `enterprise-ecos24`
through a registered k8s cluster) surfaced three defects in the access model:

1. **The MCP control plane had no k8s-shaped request channel.** `request_host_access` (tier-1) and
   `request_access` (tier-2) are HTTP-shaped: tier-1 = "register host + attach a credential",
   tier-2 = "operation = method + path template". A k8s cluster is gated on `namespace/resource/verb`
   (ADR-0014 / `policy.Rule` k8s fields), so an agent could only `get_kubeconfig` and then hit the
   data plane — which **hard-denies** (default-deny ≠ require-approval, so no operator card is even
   raised) until the operator *manually* authors a k8s rule. There was no agent→operator "I want
   pods/log in enterprise-ecos24" loop for k8s the way there is for HTTP operations.

2. **`request_host_access` on a k8s cluster raised a nonsensical credential-attach card.** k8s
   clusters are pre-registered and already hold their real credentials (imported from kubeconfig,
   ADR-0011/0012). Asking the operator to type an `Authorization: Bearer …` for an
   already-credentialed cluster is meaningless and was the operator's first point of confusion.

3. **Two parallel "grant" surfaces for one request, only one of which works.** `request_host_access`
   writes BOTH an `access_requests` log row (rendered as the *Access requests* table, with
   Grant/Deny/Dismiss buttons) AND an `approval.Queue` card (the *Pending approvals* card). The
   table's **Grant** calls `access.Resolve`, which only flips the log row's status string — it has
   **zero** effect on policy. The operator clicked it and access "didn't work". Worse, a correct card
   **Approve** never marked the log row granted (only Deny synced, via `DenyLatest`), so the history
   and the real decision drifted.

## Decision

Treat these as one coherent access-model cleanup, in three parts.

### A. k8s access request channel (`request_k8s_access`)

New MCP tool `request_k8s_access(cluster, namespace, resource, verb, purpose)` →
`mcpsvc.RequestK8sAccess`. It validates the upstream is a k8s cluster, logs the intent
(`access.Create`, so deny-reason + history work), and enqueues an approval carrying a new
`approval.KindK8sAccess` with the `Namespace/Resource/Verb` tuple. On **approve**,
`applyApprovalSideEffects` creates an **agent-scoped** `allow` k8s `policy.Rule`
(`SubjectAgentID = requesting agent`) for that tuple — so the grant is scoped to the asker, not the
whole cluster. `get_access(cluster)` then reports `granted` (status is rule-derived;
`statusFor` already counts any allow rule). The agent uses `get_kubeconfig` + `kubectl`; the data
plane gates each request against the new rule.

### B. No host card for k8s / already-credentialed hosts

`request_host_access` short-circuits — **no card, no access-log row** — when the upstream needs no
operator credential action:
- **k8s cluster** → returns `granted` with a memo pointing at `get_kubeconfig` + `request_k8s_access`
  (the host tier is meaningless for k8s; the cluster is already credentialed).
- **HTTP host that already has a credential** → returns `granted` with a memo to `request_access`
  for the operation (the host is registered + credentialed; nothing for the operator to do).

A host card is raised **only** for a brand-new / credential-less HTTP host — the one case where the
operator genuinely must attach a credential. (The existing "already has an allow rule → granted"
short-circuit is kept.)

### C. Single actionable surface: cards act, the table is history

- On a successful card **Approve**, the daemon now also marks the matching `access_requests` row
  `granted` (`access.GrantLatest`, mirroring `DenyLatest`); Deny already synced. The log row and the
  real decision no longer drift.
- The *Access requests* UI table becomes **read-only history** — the misleading Grant/Deny buttons
  are gone; only a subtle **Dismiss** remains (to clear stale rows whose card timed out, since a
  Submit timeout never runs the resolve path). The `POST /access-requests/{id}/resolve` endpoint is
  retained for Dismiss only.

## Alternatives considered

- **Reuse `request_access` (method+path) for k8s by mapping verbs to HTTP methods.** Rejected — the
  k8s gate is `namespace/resource/verb` over a parsed `k8s.RequestInfo`, not a path template; forcing
  it through the HTTP shape would misrepresent the rule and the operator card.
- **Create the k8s rule as any-agent (`SubjectAgentID=""`), matching `approveOperation`.** Rejected
  for k8s — prod clusters warrant per-agent scoping; the requesting agent is known. (HTTP operation
  rules stay any-agent; revisiting that is a separate decision.)
- **Make the table's Grant trigger the real side effects.** Rejected — that duplicates the card's
  logic on a surface that lacks the credential/trust-any inputs; one actionable surface is simpler
  and less error-prone. The table is history.
- **Drop the `access_requests` log entirely and rely on the in-memory queue.** Rejected — the queue
  is in-memory (lost on restart) and the log is what surfaces deny reasons to a polling agent
  (ADR-0024) and gives the operator durable history.

## Consequences

- Agents have a first-class k8s request loop: `request_k8s_access` → operator approves → scoped
  allow rule → `get_kubeconfig` + `kubectl`. The `ecos-model`/`enterprise-ecos24` scenario works
  end-to-end through outwall.
- k8s and already-credentialed hosts no longer raise a confusing credential card; the operator only
  sees a card when there is genuinely a credential to attach.
- One place to act (cards) and one place to read history (the table); the two stay in sync.
- New approval kind `KindK8sAccess` and a new MCP tool — additive; no storage format change.
