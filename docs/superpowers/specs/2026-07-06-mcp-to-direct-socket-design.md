# Design: replace MCP control plane with a direct agent socket + CLI; seal the operator plane

- **Date:** 2026-07-06
- **Status:** draft for review
- **Touches:** `internal/mcp` (removed), `internal/mcpsvc`, `internal/agent`,
  `internal/daemon` (`admin.go`, `daemon.go`, new agent-plane adapter + operator-session gate),
  `internal/cli`, `internal/secret`, `cmd/outwall-desktop`, `web/` (Wails native bindings for
  privileged verbs); supersedes ADR-0003, amends ADR-0005; new ADRs for the agent plane, the
  operator-session gate, and the threat model.

## Problem

Two distinct pain points, one ergonomic and one security.

**1. MCP's lifecycle is brittle.** The control plane is an MCP server (streamable HTTP,
`127.0.0.1:8181`; adapter `internal/mcp/server.go`). An agent harness binds the MCP tool set **at
session start**. So:

- If the agent starts *before* outwall, the tools never appear.
- If outwall is rebuilt/restarted mid-session, the tools go stale and don't reconnect.
- Identity is `session = agent` (ADR-0003): every reconnect mints a *new* agent record + token, so
  long-lived agents accrete records and any per-agent deny rule is trivially escaped by reconnecting.

The agent should be able to reach outwall through independent, stateless calls that don't care about
start order or daemon restarts.

**2. A same-user agent can self-approve.** The AI agent (e.g. Claude Code) runs as the **same OS
user** as the operator. The operator admin API (`internal/daemon/admin.go` `apiMux()`) is the full
privileged route table (`vault/*`, `upstreams`, `rules`, `approvals/{id}/resolve`,
`upstreams/{name}/auth`, `audit/prune`, `clusters/import`, `kubeconfig`) and is served over:

- the unix socket `~/.spk/outwall/outwall.sock` (`0600`) **CSRF-free**, and
- TCP `127.0.0.1:8182/api` behind a **static** `X-Outwall-CSRF: '1'` header (`web/src/lib/api.ts`).

`0600` and loopback do **not** separate two processes of the same user. So today the agent can:

```
curl --unix-socket ~/.spk/outwall/outwall.sock -XPOST http://unix/approvals/<id>/resolve -d '{"approve":true}'
curl -H 'X-Outwall-CSRF: x'  http://127.0.0.1:8182/api/rules            -d '{...allow...}'
```

— self-approve a pending request, or self-grant an allow-rule, then spend it through the data plane
with its own token. The upstream **credential never enters the agent context**, but its *effect*
(authenticated, un-approved egress) becomes fully available. This defeats the approval model. The
hole exists today, independent of the MCP redesign.

## Threat model (agreed, explicit)

The design is honest about what same-user software boundaries can and cannot do (a Fable design
review confirmed that fs-perms, loopback, and per-process memory tricks like `PR_SET_DUMPABLE` do
**not** isolate two processes of the same OS user — e.g. a cap token injected into the WebKitGTK
renderer leaks because that child process is re-`execve`'d back to dumpable and is same-user
readable).

- **In scope (defended):**
  - The agent never sees upstream secrets (encrypted at rest; key only in daemon memory).
  - Egress is default-deny, filtered by policy, and audited.
  - Privileged operator actions (approve, grant, unlock, set auth) require the operator's
    **master password** — a secret the agent does not hold. Casual/accidental self-approval paths
    are closed.
- **Out of scope (known limitation, recorded in an ADR):** a malicious shell agent running as the
  same OS user that *also* obtains the master password (phishing, keylogging, driving the GUI and
  entering the password). The only robust defense is a **separate OS identity** for the daemon
  (separate user / user namespace / systemd unit with `hidepid`/`ProtectProc`). That is a larger
  desktop re-architecture (GUI talks to a privileged daemon over an authenticated channel,
  polkit-style); it is recorded as the escalation path (option 2) and **not** built now.
- **`PR_SET_DUMPABLE=0`** is optional Linux-only defense-in-depth. It is **not** load-bearing (the
  review showed it leaks via WebKit subprocesses) and is labeled as "raises the bar, not a boundary."
  It may be deferred to its own small task.

The boundary that actually holds in every mode is the **master password**.

## Requirements (agreed)

- **R1.** Remove MCP entirely (`internal/mcp` + the go-sdk dependency). `mcpsvc.Service` and the
  registries it uses stay.
- **R2.** New **agent plane**: plain HTTP/JSON over a dedicated unix socket `~/.spk/outwall/agent.sock`,
  a thin adapter over the same `mcpsvc.Service` (mirrors the retired `mcp`↔`mcpsvc` split). It is
  **unprivileged by construction** — it cannot express approve/grant/unlock.
- **R3.** A **CLI** is the agent's face: `outwall list-upstreams | request-host-access |
  request-access | request-k8s-access | request-preset | get-access | whoami | get-kubeconfig`.
  The agent invokes it via Bash. Each call is independent — start order and daemon restarts are
  irrelevant. The CLI knows the socket path by default (no discovery step).
- **R4.** **Per-project identity.** The CLI persists the agent's `owa_...` token in a global store
  keyed by project: `~/.spk/outwall/agents/<hash>.token`, where `hash` = SHA-256 of the realpath of
  the git top-level (if inside a repo) else realpath(cwd). It is `0600` and explicitly
  **accountability-only** (a same-user process can read any project's token — this is not an
  isolation boundary; stated as such in the ADR). Survives daemon restart (the token hash is
  persisted; the plaintext lives in the CLI's file).
- **R5.** **Route split.** Privileged mutations move behind an **operator session**; read-only GETs
  and the SSE stream stay ungated (they grant no power).
- **R6.** **Operator session = master password.** Opening the session verifies the master password
  via the Argon2id verifier **without locking the vault** (the data plane keeps serving). The
  session has an idle-TTL (default ~1h) plus an explicit "Lock now". While open, privileged verbs
  are permitted; on idle-expiry or lock they require the master password again.
- **R7.** **Desktop delivery via Wails native bindings.** On the desktop, privileged verbs are
  invoked through Wails in-process bindings, **not** an HTTP route — there is no replayable
  `curl`-able endpoint and no long-lived bearer in the renderer. **Headless/CLI** delivery uses the
  socket but prompts for the master password (sudo-style). The master password is the single barrier
  in every mode.
- **R8.** Retire the CSRF-free full-admin *behavior*: the operator unix socket stays, but every
  privileged route now requires an open operator session, and the static `X-Outwall-CSRF` model is
  removed.

## Design

### Agent plane (`internal/daemon` agent adapter + `internal/cli`)

A new HTTP/JSON handler served on `agent.sock` exposes one route per `mcpsvc.Service` method
(`GET /upstreams`, `POST /access/host`, `POST /access/op`, `POST /access/k8s`, `POST /access/preset`,
`GET /access/{upstream}` long-poll, `GET /whoami`, `GET /kubeconfig`). It carries the agent's token
in `Authorization: Bearer <owa>`; identity resolution mirrors the retired `mcp/server.go:166-199`
but keyed on the presented token instead of the SDK session:

- **First call in a project** (CLI finds no token file): the CLI takes a `flock` on the token-file
  path, calls a `register` step (client name = basename of the project dir), receives the token
  **once**, writes it atomically, releases the lock. Concurrent Bash calls in a fresh project
  therefore mint exactly one agent (the flock serializes them; the winner writes, losers read).
- **Later calls**: the CLI reads the token file and presents it; the daemon authenticates by hash
  (`agent.Registry.Authenticate`) — valid across daemon restarts. `whoami` returns identity from the
  registry; the plaintext token comes from the CLI's own file, so the in-memory `sessions` cache is
  removed.

The CLI is a thin client over this HTTP contract (the same layering the project already uses for the
operator CLI over the admin socket). Output is agent-friendly (clean JSON or short human text).

The data plane is unchanged: the agent presents the same `owa_...` token as
`Authorization: Bearer` or the `outwall_token` cookie; policy decides; upstream creds are injected;
the token is stripped before forwarding upstream.

### Operator session + route split (`internal/daemon`, `internal/secret`)

`apiMux()` is split into two groups:

- **Ungated** (read-only, no power): `GET /vault/status`, `GET /upstreams`, `GET /agents`,
  `GET /rules`, `GET /approvals`, `GET /access-requests`, `GET /audit`, `GET /audit/{id}`,
  `GET /profiles`, `GET /settings/*`, `GET /events` (SSE), `GET /oidc/redirect-uri`,
  `POST /presets/preview` (dry-run), `POST /desktop/focus` (launcher).
- **Operator-gated** (privileged mutations): `POST /vault/init|unlock|lock`, `POST /upstreams`,
  `DELETE /upstreams/{name}`, `POST /upstreams/{name}/auth`, `POST /upstreams/{name}/oauth/login`,
  `POST /oidc/discover`, `POST /rules`, `DELETE /rules/{id}`, `POST /rules/{id}/value-policy`,
  `POST /approvals/{id}/resolve`, `POST /access-requests/{id}/resolve`, `POST /clusters/import`,
  `POST /kubeconfig`, `POST /audit/prune`, `PUT /settings/audit-retention`, `POST /agents/register`
  (operator-initiated registration; agent-plane self-register is separate and unprivileged).

**Operator session.** A small in-daemon session holder: `Open(masterPassword) → error` verifies the
password against the persisted Argon2id salt/verifier (as `secret/vault.go:93-111` does in `Unlock`,
but it does **not** set/replace the resident vault key — the vault stays unlocked so the data plane
is unaffected). It records an `openedAt`/`lastUsed` monotonic timestamp; `Authorized()` returns true
while within the idle-TTL; `Lock()` and idle-expiry clear it. Timeout is a persisted setting
(default ~1h) with a "Lock now" UI control.

**Delivery.**

- **Desktop:** privileged verbs are exposed as **Wails native binding methods** on a service object
  (Go methods called from the webview's JS via Wails' in-process bridge), each checking
  `session.Authorized()`. There is no HTTP endpoint for these verbs, so a same-user `curl` has
  nothing to hit; the operator enters the master password in the UI, which calls the `Open` binding.
  The webview still loads the read-only UI + SSE over the loopback UI listener (unchanged), but the
  **mutations** no longer traverse `/api`.
- **Headless / operator CLI:** the operator-gated routes remain reachable over the socket **only**
  when the daemon has an open operator session; the CLI prompts for the master password (sudo-style)
  only when a privileged call returns "session closed", opens the session, and retries — an already
  open session within the TTL needs no re-prompt. This is the same-user-safe replacement for the
  retired CSRF-free admin behavior.

The static `X-Outwall-CSRF` middleware and the CSRF-free full-admin `AdminHandler` are removed; the
UI listener serves the ungated read-only mux + SSE (still loopback, still read-only — acceptable
under the threat model since it grants no power), and static assets.

### Removing MCP

Delete `internal/mcp/` (adapter + tests) and the `mcpSrv` wiring in `daemon.go`; `go mod tidy` drops
`github.com/modelcontextprotocol/go-sdk`. `mcpsvc` and all registries remain. The MCP listener
(`:8181`, `--mcp-listen`) is removed; `agent.sock` replaces it. The daemon's listener set becomes:
data plane (`:8080` TLS), UI/SSE (`:8182` read-only), `agent.sock`, operator socket (gated),
on-demand OIDC callback.

### Docs

- New ADR: agent-plane identity (CLI over `agent.sock`, per-project accountability token,
  registration flock) — supersedes **ADR-0003**.
- New ADR: operator-session gate (master password, idle-TTL, Wails-bindings delivery, route split) —
  amends **ADR-0005** (states why loopback + CSRF is no longer the boundary for same-user).
- New ADR: threat model (in/out of scope; separate-OS-user as the escalation path; `PR_SET_DUMPABLE`
  as non-load-bearing DiD).
- Update `docs/architecture/overview.md` (two-plane picture, listener table) and the control-plane
  section of the design spec index.

## Testing

- **Unit:**
  - agent-plane adapter: identity mint/resolve by token; `whoami` from registry (no session cache);
    bootstrap flock serializes concurrent first-calls to one agent.
  - operator session: `Open` verifies password without locking the vault (data plane still serves);
    idle-TTL expiry and `Lock` revoke authorization; wrong password rejected.
  - route split: an operator-gated route without an open session → 401/403; ungated GET unaffected.
- **Integration:**
  - full cycle via CLI: `request-preset` → operator approves (session open) → `get-access` → data
    plane 200.
  - the old self-approval paths (`curl` the socket / `X-Outwall-CSRF`) → now rejected.
- **Regression:**
  - daemon restart: the CLI's persisted token still authenticates (hash persisted); data plane
    resumes.
  - vault stays unlocked across operator-session open/close; the data plane never 503s from an
    operator-session lock.

## Alternatives considered

- **B-cap (in-memory capability token gating the operator plane over HTTP)** — rejected. The Fable
  review showed the cap token, injected into the WebKitGTK renderer's JS, is readable by a same-user
  agent because the renderer is a separate `execve`'d child that returns to dumpable regardless of
  `PR_SET_DUMPABLE` on the daemon; it also had no headless story and broke the operator CLI. It would
  have been security theater — the master-password gate is the real boundary.
- **B-ipc alone, no master password** — insufficient: driving the webview (GUI automation) would then
  approve without any secret. The master password is what a same-user agent lacks.
- **Separate OS user for the daemon (true isolation)** — the only robust defense against a malicious
  same-user shell agent, but a large desktop re-architecture. Recorded as the escalation path, not
  built now.
- **Keep MCP alongside the new plane** — rejected: the user wants MCP gone; alpha has no compat to
  preserve, and carrying two control planes doubles the surface.
