# ADR-0029: `request_k8s_access` takes multiple resource/verb grants per call

- **Status:** accepted
- **Date:** 2026-06-22
- **Extends:** ADR-0025 (the k8s access request channel).

## Context

`request_k8s_access` (ADR-0025) took a single `(namespace, resource, verb)` tuple. Reading a pod's
logs with `kubectl logs` actually needs three permissions — `pods get` + `pods list` (kubectl
resolves the pod) and `pods/log get` (the log subresource) — because in Kubernetes RBAC `get`
(one named object) and `list` (the whole collection) are distinct verbs, and `pods/log` is a distinct
subresource. With one tuple per call, the agent had to make several calls and could still miss a verb
(it requested `pods list` + `pods/log get` but not `pods get`, so `kubectl logs` was denied). Each
call also produced its own approval card.

We considered auto-granting `pods get` whenever `pods/log get` is requested, and rejected it:
`pods/log get` alone is sufficient to read logs via the raw API; `pods get` is only the `kubectl
logs` wrapper's need and it exposes the whole pod object (env vars, secret references, images). Silent
coupling would be a least-privilege violation. The agent should ask for exactly the bundle it needs.

## Decision

`request_k8s_access` now takes, for one namespace, a list of grants — `grants: [{resource, verbs[]}]`
(e.g. `[{resource: pods, verbs: [get, list]}, {resource: pods/log, verbs: [get]}]`). The service
flattens them into unique `(namespace, resource, verb)` tuples and:

- **drops tuples an existing allow rule already covers** (exact match for the agent or any-subject),
  so a re-request only asks for what is still missing;
- if nothing remains → returns `granted`;
- otherwise enqueues **one** `KindK8sAccess` approval carrying **all** the missing tuples
  (`Pending.K8sGrants`), de-duplicated against an identical pending request (same tuple set).

On approve, `approveK8sAccess` creates one agent-scoped allow rule per tuple, skipping any whose rule
already exists. The approval card lists every tuple; the operator approves the whole bundle once. The
tool description tells the agent the bundle needed for `kubectl logs`.

## Alternatives considered

- **Auto-grant `pods get`/`list` from a `pods/log get` request.** Rejected — a silent over-grant
  (`pods get` exposes the full pod object); `pods/log get` alone suffices for the raw log API. The
  agent requests the bundle explicitly instead.
- **Cross-product of `resources[] × verbs[]`.** Rejected — it over-grants asymmetric cases (it would
  add `pods/log list`, which is meaningless). The per-resource `{resource, verbs[]}` shape is precise.
- **Keep one tuple per call.** Rejected — that is the friction this fixes (multiple calls, multiple
  cards, easy to miss a verb).

## Consequences

- One `request_k8s_access` call covers everything an agent needs (e.g. read logs) and raises a single
  approval card listing all tuples; re-requests ask only for the still-missing tuples.
- `approval.Pending` gains `K8sGrants []K8sGrant`; the approvals API/card render the list; the legacy
  single `Namespace/Resource/Verb` fields are still set to the first tuple for the
  notification/display paths. Covered by `TestRequestK8sAccessMultiGrant`.
- No change to the least-privilege model: the agent still names each verb; nothing is auto-expanded.
