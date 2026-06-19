# ADR-0020: Request-body operation variables

- **Status:** accepted
- **Date:** 2026-06-19

## Context

Operation templates (ADR-0014) gated typed variables extracted from the URL **path** and **query**.
But many write operations carry their scope in the **JSON request body** — `POST /widgets
{"name":"…","spec":{"size":N}}`, `PATCH …/scale {"replicas":N}`. Without body variables an operator
can only allow/deny the whole endpoint; they cannot say "this agent may create widgets named `alpha`
with size ≤ 100". The same invariant as the rest of the model must hold: **parse the real request,
never trust a declaration**.

## Decision

Extend the operation template with a **body template** — a map of dotted JSON path → literal or
`{name:type}` placeholder — extracted from the real request body and gated by the existing
per-variable value policies (text-set / number-range / enum-closed-set / date, ADR-0017).

- **`optemplate`** — `ParseWithBody(method, path, query, body)` (the old `Parse` delegates with a nil
  body). `Template.ExtractBody(raw []byte) (vars, ok)`: a template with no body params accepts any
  body; otherwise the body MUST be a JSON object and each declared path must resolve to a scalar that
  passes its type check / literal match, else `ok=false` (the request does not match → default-deny).
  Dotted paths walk nested objects; arrays are not supported (documented). `Key()` includes the body
  section so a body shape is part of the rule identity.
- **`policy`** — `Rule.OpBodyTemplate map[string]string` (new `op_body_template` JSON column).
  `Input.Body []byte`. `evalHTTPRule` extracts body vars after the path/query match and merges them
  into the variable set before value gating — a missing/wrong-typed body var fails the match. Body
  vars use the SAME `op_value_policies` map keyed by variable name, so the UI value-set / range /
  enum editors work for them with no change.
- **`proxy`** — for non-k8s body-bearing methods (POST/PUT/PATCH/DELETE) the body is read once before
  `Decide`, passed as `Input.Body`, and restored (`io.NopCloser` + `ContentLength`) so the upstream
  and the audit tee receive the original bytes. GET/HEAD bodies are never read. The injected upstream
  credential is applied later (Rewrite), so it is never part of the gated/forwarded body.
- **MCP / approval / UI** — `request_access` carries a `BodyTemplate`; the approval `Pending` and
  `approveOperation` thread it through so an approved operation rule stores the body template; the
  Operations screen shows the body shape on the rule card.

## Alternatives considered

- **Trust an agent-declared body shape.** Rejected — the whole model's invariant is that outwall
  parses the actual bytes; a declared body could differ from what is sent.
- **A full JSONPath / JMESPath selector language.** Rejected for v1 — dotted object paths cover the
  common cases; arrays and predicates add a parser and ambiguity. Can be extended later behind the
  same `OpBodyTemplate` field.
- **Stream the body and extract incrementally.** Rejected — the gateway already buffers bodies for
  k8s approval + audit; reading the body once and restoring it is simpler and correct, and a
  localhost gateway is not memory-constrained for request bodies.

## Consequences

- Operators can scope write operations by their body content with the same value-policy controls as
  path/query variables; an out-of-policy body value is denied (or queued, per the variable type) at
  the gateway, and the agent never learns the gated secret.
- Body-bearing requests are now fully buffered in memory before forwarding (previously only k8s
  mutations / audit-captured ones were). Acceptable for the alpha localhost gateway; a size cap on
  pre-read could be added if needed.
- A storage column was added (`op_body_template`); alpha, no migration.
- Array/predicate selectors are a known limitation, documented in the `optemplate` module doc.
