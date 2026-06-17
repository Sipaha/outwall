# Architecture overview

outwall is a single Go binary (daemon + CLI) with a Wails 3 desktop wrapper and an embedded
React UI. It sits between local AI agents and external HTTP APIs.

## Two planes

```
              control plane (MCP, streamable HTTP, localhost)
  agent  ───────────────────────────────────────────────►  outwall daemon
   │      "give me api.github.com? (purpose: …)"  ◄──── "yes → /github, token …"
   │
   │      data plane (reverse proxy, localhost)
   └──────────────────────────────────────────────────►  outwall ──► upstream API
          GET /github/repos/x   Authorization: Bearer <agent-token>      (TLS terminated
                                                                          here; upstream
                                                                          creds injected)
```

- **Control plane** — one MCP server. Agents discover what they may call, register
  dynamically, and receive a bearer token. Tools: `list_upstreams`, `request_access(host,
  purpose)`, `get_access`, `whoami`.
- **Data plane** — a reverse proxy. The agent sends ordinary HTTP to
  `http://localhost:PORT/<upstream>/<path>` with its bearer token. outwall identifies the
  agent, checks policy (default-deny), injects the upstream's credentials, forwards, audits.
  **Upstream secrets never reach the agent.**

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
| `mcp` | the control-plane MCP server. |
| `audit` | request/response journal + body store. |
| `daemon` | wires it all; serves data plane (TCP localhost) + admin API (unix socket). |
| `desktop` | Wails 3 wrapper supervising the daemon, rendering the embedded UI. |

In Plan 1, `policy`/`approval`/`mcp`/`audit`/`desktop` are not yet built; a flat `grant`
allow-list stands in for `policy`. See ADR-0001 and the current plan.

## Key invariants

- Secrets never leave outwall (the agent only ever holds its own token + a local path).
- Default-deny: a freshly registered agent can do nothing until access is granted.
- Vault locked ⇒ the whole data plane is halted (503) and access granting is blocked.
- No CGO in the server binary; pure-Go SQLite.
