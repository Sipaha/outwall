# outwall — Design Spec

**Date:** 2026-06-17
**Status:** Approved (brainstorming) — pending implementation plan

## 1. Purpose & Core Invariant

outwall is a local **desktop daemon** that acts as an authenticating, filtering, auditing
egress gateway for AI agents calling external HTTP APIs. The goal: give agents direct
access to ordinary HTTP APIs **without ever handing them secrets and without risking that
they take down production**.

Flow:

1. An agent asks the MCP server: "do you give me access to `api.github.com`?" and states
   **why** (purpose).
2. The MCP answers `granted` (+ a base path) / `pending-approval` / `denied`.
3. The agent then sends **ordinary HTTP requests** to `http://localhost:PORT/<upstream>/...`
   with its own bearer token. It never sees the upstream's credentials.

**Core invariant:** upstream secrets never leave outwall. The agent only ever sees a local
path and its own agent token. outwall terminates TLS itself, so request/response bodies are
fully visible for filtering and audit — **no MITM, no custom CA**.

## 2. Two Planes

### Control plane — MCP (streamable HTTP on localhost)

A single long-lived MCP HTTP endpoint (`http://localhost:PORT/mcp`) serves all agents.
Discovery, dynamic registration, token issuance.

MCP tools:

- `list_upstreams` — which APIs exist and their status *for the calling agent* (open /
  request-required / denied).
- `request_access(host_or_upstream, purpose)` — request access stating intent; returns
  `granted` (+ base path) / `pending-approval` / `denied`.
- `get_access(upstream)` — base path + short memo (allowed methods/paths, limits) for an
  already-granted access.
- `whoami` — agent id, status, active accesses.

**Dynamic registration:** first MCP contact auto-registers the agent (status `new`,
default-deny everything) and issues a unique agent bearer token. The agent appears in the
UI for the operator to act on.

### Data plane — reverse proxy (localhost)

- `http://localhost:PORT/<upstream>/<path>` + header `Authorization: Bearer <agent-token>`.
- Per request outwall: identifies the agent by token → matches policy → injects the
  upstream authenticator → proxies → writes audit.
- TLS terminated at outwall; bodies fully inspectable.

## 3. Policy Model (default-deny)

A **freshly registered agent can do nothing** until access is explicitly granted.

Rule shape: `(subject, upstream, method + path-glob, rate-limit) → allow | deny | require-approval`

- **subject:** a specific agent **or** "any agent" (a global per-upstream rule).
- **precedence:** specific-agent rule > global upstream rule > default-deny. A specific-agent
  rule can deny what is globally open, and vice versa.
- **dimensions (MVP):** upstream + method + path glob (prefix/`**`) + rate limit (N/min).
  Body/param filtering is Phase 2.
- **outcomes:**
  - `allow` — proxy immediately.
  - `deny` — reject (403 on data plane / "denied" in MCP).
  - `require-approval` — see below.
- **purpose:** mandatory on `request_access`; stored; shown at approval time and in audit.

### Approval behavior (blocking)

`require-approval` **blocks the data-plane request** via long-poll up to ~5 min. The operator
clicks allow/deny in the UI. On allow → the request proceeds to the upstream and the agent
receives the **real** response. On timeout → denied. A ticket/async fallback is designed into
the interface but implemented later (Phase 2).

## 4. Upstream Authentication (on the agent's behalf)

Per-upstream `Authenticator` behind a **pluggable interface** (so mTLS / SigV4 / HMAC / custom
can be added later without rework).

MVP authenticators:

- **none / passthrough**
- **static header / API key** (`Authorization: Bearer …` or `X-API-Key: …`)
- **basic** (`user:pass` → `Authorization: Basic …`)
- **OIDC client-credentials** (M2M: token endpoint, cache + refresh access token)

Phase 2:

- **OIDC authorization-code** (human in the loop): outwall opens the browser → user logs in
  at the IdP → local callback → exchange code → store access+refresh encrypted → auto-refresh
  → inject `Bearer`. The human authorizes once; agents ride on that token for the upstream.

## 5. Secrets & Master Password

- `Argon2id(master-password)` → key; **per-secret AES-256-GCM**. Only a verifier is stored;
  the password itself is never stored. Forgetting it = secrets are lost (by design).
- On daemon start the vault is **locked**. The Wails UI shows an unlock screen. While locked,
  **the entire data plane and all access granting are halted** — data plane returns
  `503 vault locked`; MCP discovery reports "vault locked". outwall is effectively "off" until
  unlocked (simplest and safest).

## 6. Audit

Per-request journal entry: timestamp, agent, upstream, method, path+query, status, duration,
sizes, policy decision (+ who approved / when).

- Request and response **bodies stored in full up to 256 KB**; larger → truncated with
  `[truncated, N bytes total]`. Non-text bodies (by Content-Type) → metadata only (type, size,
  sha256).
- Credentials injected by outwall are **masked** in the audit (`Authorization: ***`) so the log
  is not itself a leak.
- Bodies live in a separate table / on disk; the main journal stays light to list.
- Retention: keep everything by default + manual purge + optional auto-prune (by age / total
  size).

## 7. Stack & Structure (inherited from citeck-launcher)

- **Backend:** single Go binary (daemon + CLI). `log/slog`, `net/http` (Go 1.22 routing),
  `gopkg.in/yaml.v3`, golangci-lint v2 config mirrored from launcher.
- **Storage:** SQLite (`modernc.org/sqlite`, pure-Go, no CGO) — single file.
- **Desktop:** Wails 3 thin-wrapper supervises the daemon; control plane over a Unix socket
  (chmod 0600), as in launcher.
- **UI:** React 19 + Vite + TypeScript + Tailwind 4 + Zustand + lucide-react, embedded via
  `go:embed`; **SSE** for live events (new agents, approval queue, audit stream).

Draft package layout (`internal/`):

| Package | Purpose |
|---|---|
| `proxy` | data-plane reverse proxy |
| `mcp` | control-plane MCP server (streamable HTTP) |
| `policy` | rule matching engine |
| `authn` | upstream `Authenticator` implementations |
| `upstream` | upstream registry/config |
| `agent` | agent registry + token issuance/revocation |
| `secret` | vault: KDF, crypto, lock/unlock |
| `audit` | request journal + body storage |
| `approval` | blocking approval queue |
| `store` | SQLite backend |
| `daemon` | HTTP server, SSE, middleware |
| `desktop` | Wails wrapper (daemon supervisor) |
| `cli` | cobra commands |

## 8. UI Screens (MVP)

- **Unlock** — master password.
- **Dashboard** — agents + live approval queue.
- **Upstreams** — CRUD, auth method selection, secrets.
- **Agent detail** — status, accesses, rules, history.
- **Policies / Rules editor**.
- **Approvals** — incoming requests with purpose, allow/deny.
- **Audit** — journal with filters + request/response body viewer.
- **Settings**.

## 9. Phasing

**Phase 1 (MVP):** data plane + MCP discovery/tokens + default-deny policies
(upstream + method/path + rate limit) + static/basic/OIDC-client-credentials auth + vault +
blocking approval + audit + base UI.

**Phase 2:** OIDC authorization-code (browser) + body/param filters + ticket/async approval
fallback + audit auto-prune + additional authenticators.

**Phase 3+ (if needed):** headless server mode with mTLS; forward-proxy/MITM mode for
transparent compatibility.

> Note: OIDC authorization-code is in Phase 2 deliberately — the MVP is a working product
> without the browser flow, and auth-code slots into the same `Authenticator` interface with
> no rework.

## 10. Out of Scope (explicit YAGNI)

- Headless/server deployment (Phase 3).
- Forward-proxy / MITM with custom CA (Phase 3).
- Request body/param filtering (Phase 2).
- Non-OIDC advanced authenticators — mTLS/SigV4/HMAC (interface only, no implementation).
- Multi-tenant / network-exposed access; outwall is desktop-only, localhost-only.
