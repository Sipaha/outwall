# Architecture overview

outwall is a single Go binary (daemon + CLI) with a Wails 3 desktop wrapper and an embedded
React UI. It sits between local AI agents and external HTTP APIs.

## Two planes

```
              control plane (agent socket, HTTP/JSON over unix, localhost)
  agent  ───────────────────────────────────────────────►  outwall daemon
   │      "give me api.github.com? (purpose: …)"  ◄──── "yes → /github, token …"
   │
   │      data plane (reverse proxy, localhost)
   └──────────────────────────────────────────────────►  outwall ──► upstream API
          GET /github/repos/x   Authorization: Bearer <agent-token>      (TLS terminated
                                                                          here; upstream
                                                                          creds injected)
```

- **Control plane** — a plain HTTP/JSON handler on a `0600` unix socket (`agent.sock`), driven by the
  `outwall` CLI. Agents discover what they may call, self-register (per-project token), and receive a
  bearer token. Commands: `list-upstreams`, `request-host-access`/`request-access`/`request-preset`,
  `get-access`, `whoami`, `get-kubeconfig`.
- **Data plane** — a reverse proxy. The agent sends ordinary HTTP to
  `http://localhost:PORT/<upstream>/<path>` with its bearer token. outwall identifies the
  agent, checks policy (default-deny), injects the upstream's credentials, forwards, audits.
  **Upstream secrets never reach the agent.**

The **admin/operator surface** (unix admin socket + the desktop UI's TCP `/api` bind) is itself
split in two: ungated read-only views/SSE/dry-run preview, and master-password-gated privileged
mutations (approve, grant, unlock, set-auth, prune, …) — see ADR-0041. The old static
`X-Outwall-CSRF` header (ADR-0005) is retired; the operator session is the boundary now, on both
transports.

## Subsystems

| Subsystem | Responsibility |
|---|---|
| `secret` (vault) | Argon2id(master-password) → key; per-secret AES-256-GCM; lock/unlock. |
| `store` | SQLite (modernc.org/sqlite, no CGO) persistence. |
| `upstream` | named external APIs + their encrypted auth config. |
| `agent` | dynamic agent registration + bearer tokens (SHA-256 hashed at rest). |
| `authn` | pluggable `Authenticator` injecting upstream creds (none/static/basic/OIDC…). |
| `policy` | default-deny rules (subject×upstream×method×path×rate-limit → allow/deny/approval). |
| `approval` | blocking approval queue for `require-approval` outcomes. |
| `proxy` | the data-plane reverse proxy. |
| `agentapi` | the control-plane HTTP/JSON adapter over `mcpsvc.Service`, served on the agent socket. |
| `agentid` | the CLI's per-project agent-token store (realpath-of-git-top-level keyed, flock mint-once). |
| `audit` | request/response journal + body store. |
| `opsession` | operator session: idle-TTL sliding window, gates privileged admin mutations. |
| `daemon` | wires it all; serves data plane (TCP localhost) + admin API (unix socket). |
| `desktop` | Wails 3 wrapper supervising the daemon, rendering the embedded UI. |

In Plan 1, `policy`/`approval`/`audit`/`desktop` are not yet built; a flat `grant`
allow-list stands in for `policy`. See ADR-0001 and the current plan.

## Key invariants

- Secrets never leave outwall (the agent only ever holds its own token + a local path).
- Default-deny: a freshly registered agent can do nothing until access is granted.
- Vault locked ⇒ the whole data plane is halted (503) and access granting is blocked.
- Privileged operator mutations (approve, grant, unlock, set-auth, prune) require an open
  **operator session**, unlocked by the master password, on every transport (unix socket + UI
  /api). A same-user agent cannot self-approve (ADR-0041).
- The operator session is distinct from the vault lock: it has an idle TTL + "Lock now";
  idle-expiry does NOT lock the vault (the data plane keeps serving).
- No CGO in the server binary; pure-Go SQLite.
