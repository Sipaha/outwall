# ADR-0010: Kubernetes gateway — exec / attach / cp / port-forward (Plan K3)

- **Status:** accepted
- **Date:** 2026-06-18

## Context

Plans K1 (ADR-0008) and K2 (ADR-0009) delivered the read half and the mutating-verb +
approval half of the Kubernetes gateway over the ordinary request/response model. The
remaining live behaviours — `kubectl exec`, `attach`, `cp` (which is exec + tar), and
`port-forward` — are different: they negotiate an HTTP **connection upgrade** (modern
clusters speak WebSocket `*.channel.k8s.io`; older ones SPDY/3.1) and then carry a
**bidirectional duplex stream**, not a request body followed by a response body.

Forces shaping the implementation:

- The data path already exists. `httputil.ReverseProxy` natively recognises
  `Connection: Upgrade` + `Upgrade:` request headers and a `101 Switching Protocols`
  upstream response, hijacks the client connection, and copies bytes both ways. K1's
  per-cluster TLS transport carries the upgrade unchanged. We must **not** interpret the
  stream (no `client-go remotecommand`, no WebSocket framing library at runtime) — we proxy
  raw bytes.
- The K2 audit body-capture (`audit.NewCaptureRef` on the request, a capped tee on the
  response inside `ModifyResponse`) is fundamentally a request/response concept. Installing
  `ModifyResponse` on a `101` makes `ReverseProxy` fail with *"101 switching protocols
  response with non-writable body"* and the upgrade returns `502` — it corrupts the duplex
  stream. Body capture must be bypassed on upgrades.
- An interactive session still needs an audit trail, but a full transcript is the wrong unit:
  it is a live terminal, potentially huge and binary. The audit must be **metadata** — who,
  which cluster/namespace/pod/container, the command (from the query), start/end (duration),
  and bytes streamed each way — with **no** body blob.
- RBAC: kube-apiserver authorizes exec/attach/portforward as the `create` verb regardless of
  the wire method (WebSocket exec is a `GET`, SPDY exec a `POST`). A policy rule
  `(namespace, pods/exec, create)` must grant the upgrade.

## Decision

**Classify upgrade subresources in `internal/k8s`.** `RequestInfo.IsUpgrade()` is true for
subresources `exec`, `attach`, `portforward` (`cp` rides on `exec`). `verbFor` maps these
subresources to the `create` verb up front, before the method switch, so the verb is `create`
for both the WebSocket (`GET`) and SPDY (`POST`) handshakes. A rule
`(ns, pods/exec, create)` therefore grants exec/attach/port-forward.

**Gate, then let `ReverseProxy` perform the upgrade.** The proxy evaluates policy exactly as
for any k8s verb. `deny` ⇒ `403` **before** any upgrade; `require-approval` blocks on the
existing `approval.Submit` **before** the `101` (same blocking queue as K2), so the upgrade
proceeds only after the operator approves and is refused with `403` on deny — the upstream is
never contacted. On `allow`/approve the K1 per-cluster transport is attached and
`httputil.ReverseProxy` does the handshake and bidirectional copy. No new transport code.

**Bypass body capture on upgrades.** A new `captureBodies = auditRecording && !isUpgrade`
gate replaces the unconditional audit-capture wiring: for an upgrade neither the request-body
`NewCaptureRef` nor the `ModifyResponse` response tee is installed, so the `101` duplex stream
is never wrapped.

**Metadata audit on session close.** When auditing an upgrade, the proxy wraps the
`ResponseWriter` in a `hijackAuditWriter`. When `ReverseProxy` hijacks the client connection,
the wrapper returns a `countingConn` that tallies bytes read (agent → upstream) and written
(upstream → agent) and fires once on `Close` — the session lifecycle. That callback writes a
single `audit.Entry` carrying cluster (`UpstreamName`), namespace + pod (in `Path`), command +
container (in `Query`), `StatusCode` (101), `DurationMs`, `ReqBytes`/`RespBytes`, decision, and
masked headers — and **no** `audit.Body`. The existing `audit_log` schema already holds every
field; no schema change was needed.

## Alternatives considered

- **Capture the full exec transcript** (optionally capped) — rejected for now: a live terminal
  is binary/huge and the duplex framing is opaque without interpreting the WebSocket/SPDY
  channels. Metadata is the right default; a future capped-capture mode can be added without a
  format break.
- **Interpret the stream with `client-go`'s `remotecommand`** — rejected: pulls a heavy runtime
  dependency and defeats the "proxy bytes, don't interpret" principle. `ReverseProxy`'s generic
  `Upgrade` hijack streams both WebSocket and SPDY transparently.
- **Record the session at request time (before the stream)** — rejected: duration and byte
  counts are only known at close. Recording on the hijacked conn's `Close` is the one point with
  the full session shape.

## Consequences

- An operator can allow an agent to `exec`/`attach`/`port-forward` (and `cp`) into a granted
  namespace, gated and approval-capable exactly like any other verb, with a metadata audit of
  every session.
- Audit gains a session-shaped record (no body blob) for upgrades; the journal schema is
  unchanged. `internal/proxy` gains `upgrade.go` (`hijackAuditWriter` + `countingConn`).
- We add **no** runtime WebSocket/SPDY dependency. The exec test drives a real upgraded
  connection by hand-rolling the `101` handshake over a raw `httptest` hijack (no test WS dep
  either), proving a byte round-trips through the proxy.
- SPDY-only clusters still work: `ReverseProxy`'s generic `Upgrade` hijack streams whatever
  protocol the client and apiserver negotiate; outwall does not parse it.
- A future change wanting a full or capped transcript would add an opt-in capture around the
  `countingConn` byte stream; nothing here forecloses it.
