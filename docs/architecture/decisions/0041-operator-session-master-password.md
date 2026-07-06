# ADR-0041: Operator session gated by the master password (seal the operator plane)

- **Status:** accepted
- **Date:** 2026-07-06

## Context

ADR-0040 (sibling, same day) makes the agent plane a plain unprivileged HTTP/JSON socket. That
left the **operator** plane unsealed: every privileged mutation (approve/deny, grant a preset,
create/delete a rule, set-auth on an upstream, unlock/lock the vault, register an agent,
resolve an access request, prune audit) was reachable by ANY same-user process over either
transport —

- the unix admin socket (`AdminHandler`, `0600`, CSRF-free by design for the CLI), and
- the TCP `/api` bind (`UIHandler`), gated only by the **static** `X-Outwall-CSRF` header
  (ADR-0005) — a constant the operator's own browser JS also sends, so any local process can
  read/replay it.

`0600` + loopback separate *other users* and *other hosts*, not two same-user processes. A
malicious or compromised agent sharing the operator's account could curl the socket directly and
self-approve its own pending request, self-grant a preset, or read a credential — with nothing
but course, non-secret, defeatable gates in the way. Spec R5–R8 (2026-07-06 design) call for
closing this: split the surface into what genuinely needs no protection (read-only views, the
live SSE feed, a dry-run preset preview, the launcher focus hand-off) and what does (every
mutation that changes policy, grants, credentials, or vault state), and gate the latter behind a
secret only the human operator holds.

## Decision

**Route split.** `apiMux()` (`internal/daemon/admin.go`) now builds one table shared by both
transports, split into:

- **UNGATED** — `GET /vault/status`, `POST /vault/init` and `POST /vault/unlock` (both
  self-authenticating — see D2), `GET /upstreams`,
  `GET /oidc/redirect-uri`, `GET /agents`, `GET /rules`, `GET /profiles`,
  `POST /presets/preview` (dry-run, no state change), `GET /approvals`, `GET /access-requests`,
  `GET /audit[/{id}]`, `GET /settings/audit-retention`, `GET /events` (SSE), `POST /desktop/focus`
  (ADR-0013 launcher hand-off), and the three new `/operator/session/*` routes below — they
  grant no power, they are the entry point that *unlocks* power.
- **OPERATOR-GATED** — the 16 remaining mutations: vault lock, upstream create/delete/
  set-auth/oauth-login, OIDC discover, agent register, cluster import, kubeconfig assembly, rule
  create/delete/set-value-policy, approval resolve, access-request resolve, audit prune/
  retention-set. Each is wrapped individually by `operatorGate` inside `apiMux`, not by a
  blanket prefix — so the ungated reads and the gated writes live in one table with the split
  visible at the call site, and adding a new privileged route is a one-line `gate(...)` call
  that cannot be missed.

**The gate: an operator session opened by the master password.** `internal/opsession.Session`
holds one in-daemon session: `Open()` (start now), `Lock()` (close now — "Lock now"),
`Authorized()` (open AND within the idle TTL; **slides the window** on success — a read-only
`Status()` peek does not), `DefaultTTL = 1h`. The clock is injectable so tests drive expiry
without sleeping. `operatorGate` calls `Authorized()`; a closed/expired session returns 403
`{"error":"operator session required"}`.

Opening the session requires the master password, checked by **`secret.Vault.Verify(password)`**
— re-derives the Argon2id key from the stored salt/verifier and compares, exactly like `Unlock`,
but **without touching the vault's locked/unlocked state** (no resident key set or cleared). It
works even while the vault is locked, because it reads from the DB, not the in-memory key. This
is what makes the session a boundary orthogonal to the vault: `POST /operator/session/open` is
UNGATED (it IS the password checkpoint) and calls `Verify`, not `Unlock`.

**Both transports, uniformly.** Because `AdminHandler()` and `UIHandler()` (`/api`) both build
their mux from the same `apiMux()`, wrapping a route once gates it on the unix socket AND the TCP
bind — the old "curl the socket, skip the CSRF header" bypass and the old "replay the static
CSRF header" bypass are the same 403 now. `TestOperatorGateSealsBothTransports` asserts this
directly.

**Static CSRF retired.** `csrfMiddleware` and the `X-Outwall-CSRF` header are removed
(`UIHandler` no longer wraps `/api` in it; the web client drops the header). The master-password
operator session is now the boundary for a same-user process on the TCP bind (see the ADR-0005
amendment below); `GET /events` (SSE) stays ungated and read-only, as it always was (it was
CSRF-exempt before, for the same reason: `EventSource` cannot set custom headers).

**CLI: sudo-style.** `internal/cli.doPrivileged(gf, method, path, body, out)` runs the call; on a
403 whose body matches `isSessionRequired` (`"operator session required"` substring — the stable
contract with `client.Do`'s error surfacing) it prompts the master password on the TTY
(`promptPasswordFn`, stubbable in tests), opens the session via `POST /operator/session/open`,
and retries the original call exactly once. A session already open within its idle window never
prompts. Every privileged CLI command routes through this helper.

**Web: modal + gate hook.** The static CSRF header is dropped from the fetch wrapper. A
`operatorSession.ts` helper (`openOperatorSession`/`lockOperatorSession`/
`getOperatorSessionStatus`) plus an `OperatorSessionModal.tsx` component prompt for the master
password on the same 403 signal and retry, mirroring the CLI's sudo-style flow; a "Lock now"
action calls `/operator/session/lock`.

### D2 — Bootstrap and the vault-vs-session order

`POST /vault/init` **cannot** sit behind the gate — it is what *establishes* the master password;
there is nothing to verify yet. It stays ungated, and on success calls `d.opsession.Open()`
directly (the operator just set the password and is present at the keyboard), so the first
batch of gated calls after init needs no immediate re-prompt.

`POST /vault/unlock` is ungated for the **same** reason: it is *self-authenticating* — the vault
verifies the master password itself (`ErrBadPassword` otherwise), so an operator-session gate on
top is redundant and, worse, would prompt the operator for the same password twice (unlock 403 →
session modal → re-enter the same password). Instead unlock stays ungated and, on success, calls
`d.opsession.Open()` — the operator just proved the master password, so their session opens and
the first privileged action after unlock needs no separate prompt.
`TestVaultUnlockUngatedAndOpensSession` and `TestOperatorSessionControlRoutes` cover this.

The operator session and the vault lock are **deliberately independent state machines**:
`TestOperatorSessionLockKeepsVaultUnlocked` proves that closing the session (idle-expiry or
"Lock now") does NOT lock the vault — the data plane keeps serving proxied traffic; only the
*next privileged mutation* needs a fresh password.

### D1 — R7 (desktop delivery) deferred to Plan 3

Spec R7 envisioned privileged operator verbs delivered through Wails **native Go↔JS bindings**
with no HTTP endpoint at all for the desktop build — removing the network surface as
defense-in-depth. The codebase has no project-authored Wails bindings today (the desktop wrapper
only supervises the daemon and renders the embedded web bundle), and building that layer is a
separable, larger change than sealing the gate itself. This ADR delivers the master-password
gate over HTTP uniformly for **both** transports now (it is the actual boundary regardless of
transport); migrating the desktop build to native bindings — which would let `UIHandler`'s
privileged routes be dropped entirely for that build — is **deferred to a future Plan 3** and is
not required for the threat this ADR closes.

## Threat model

**In scope (defended):**
- Upstream secrets never enter agent context (unchanged, ADR-0001/0019).
- Egress stays default-deny, filtered by policy, and audited (unchanged, ADR-0002/0004).
- Every privileged operator mutation requires proof of the master password within the last
  idle-TTL window, on both transports — a same-user agent process cannot self-approve its own
  pending request, self-grant a preset, register a rogue agent, or read/rotate a credential
  without that password. This closes the casual self-approval hole described in Context.

**Out of scope (recorded limitation, not built here):** a malicious same-user shell agent that
**also** obtains the master password — by phishing the operator, keylogging, or driving the
desktop GUI directly (it runs as the same user and can see the same screen) — is not defended by
this ADR. No in-process gate can stop a process that already has the same OS identity and the
secret. The only robust fix is giving the daemon a **separate OS identity**: a dedicated service
user, a Linux user namespace, or an OS-level process-isolation primitive (systemd
`ProtectProc`/`hidepid`, a polkit-style broker) so a same-user agent process structurally cannot
reach the daemon's memory or its control sockets at all. That is a real architecture change,
escalated here rather than shipped as a quiet partial fix.

`PR_SET_DUMPABLE=0` (Linux `prctl`, blocks `ptrace`/core-dump of the daemon process) was
considered as a cheap addition. It is **not implemented and not load-bearing** even if added
later: it "raises the bar, not a boundary" — it does nothing against an attacker who already has
the master password, and it is trivially defeated by a re-`execve`'d child (e.g. a WebKitGTK
renderer subprocess spawned by the desktop webview) which does not inherit the flag. Recorded
here so a future contributor does not mistake it for a real isolation mechanism.

## Alternatives considered

- **In-memory capability token over HTTP (B-cap)** — mint a random token in the daemon, hand it
  to the desktop process, require it on every privileged call instead of a password. Rejected:
  for the Wails desktop build the token still has to reach the renderer to be attached to
  requests, and anything that reaches the WebKitGTK-hosted renderer is readable by the same-user
  agent that can already inspect that process — a same-user secret with no human proof-of-
  presence behind it, i.e. security theater relative to a password the agent does not know.
- **B-ipc without a password** — gate purely on "request came from the desktop GUI's IPC
  channel," no secret involved. Rejected: GUI automation (the same-user agent driving the
  desktop UI programmatically, e.g. via accessibility APIs or a scripted webview) would sail
  through with no proof a human decided anything — the whole point of the gate is a secret the
  automating process does not hold.
- **Separate OS user for the daemon** — the real fix for the out-of-scope threat above. Rejected
  for *this* ADR only because it is a genuine deployment/architecture change (packaging, IPC
  permissions, desktop-integration rework), not a quiet workaround; recorded as the escalation
  path a future ADR should pick up if the threat model needs to tighten further.

## Consequences

- The old self-approval curl paths — hitting the unix socket directly, or replaying the static
  `X-Outwall-CSRF` value — now return 403 on both transports.
- One extra prompt per idle window: the CLI's sudo-style retry, the web UI's master-password
  modal. A session already open and recently used never re-prompts.
- No schema change — the session is in-memory only (`internal/opsession`), lost on daemon
  restart (which is correct: a fresh process should require a fresh password).
- Supersedes the CSRF-as-boundary reasoning in ADR-0005 (amended below) for the TCP transport;
  amends nothing about the event bus / SSE design itself.
- Links: 2026-07-06-mcp-to-direct-socket-design.md (spec, R5–R8); ADR-0040 (sibling — agent
  socket, same design doc, R1–R4); ADR-0005 (amended — control API + SSE); ADR-0002 (approval
  model whose `resolve` route is now gated); ADR-0021 (OIDC authorization-code login start,
  `POST /upstreams/{name}/oauth/login`, is now a gated route).
