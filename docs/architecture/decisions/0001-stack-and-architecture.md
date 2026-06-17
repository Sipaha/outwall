# ADR-0001: Stack and two-plane gateway architecture

- **Status:** accepted
- **Date:** 2026-06-17

## Context

outwall must let local AI agents call external HTTP APIs without ever handing them secrets
and without letting them damage production. It needs: credential injection (OIDC/Basic/API
key), encrypted secret storage under a master password, flexible per-agent allow/deny with
approval, and a full request/response audit. The UI is a desktop app (Wails 3 + React); the
backend is a Go daemon.

## Decision

**Stack.** Single Go binary (daemon + CLI), Go 1.26. Pure-Go SQLite (`modernc.org/sqlite`,
`CGO_ENABLED=0`) for storage. `net/http` (stdlib, Go 1.22 routing), `log/slog`, `spf13/cobra`,
`golang.org/x/crypto/argon2`, `stretchr/testify`. A separate Wails 3 (`v3.0.0-alpha.98`)
desktop wrapper (its own `cmd/`, CGO) supervises the daemon and renders an embedded React 19
/ Vite / Tailwind 4 / Zustand UI. Patterns inherited from citeck-launcher; **no citeck code**.
Module path `github.com/Sipaha/outwall`.

**Two-plane architecture.**
- *Control plane* — one MCP server (streamable HTTP, localhost): agents discover allowed
  APIs, register dynamically, receive a bearer token (`list_upstreams`, `request_access(host,
  purpose)`, `get_access`, `whoami`).
- *Data plane* — a reverse proxy: `http://localhost:PORT/<upstream>/<path>` + agent bearer
  token. TLS terminated at outwall; upstream credentials injected server-side; full audit.

**Security model.** Per-secret AES-256-GCM under an Argon2id-derived key; master password
never stored. Default-deny policy. Vault locked ⇒ data plane halted (503). Upstream secrets
never leave outwall. Desktop-only, localhost-only (server mode is a future option).

**Phasing.** Phase 1 is decomposed into 7 milestone plans (foundation → policy/approval/
OIDC-CC → MCP → audit → daemon API+SSE → web UI → Wails wrapper). OIDC authorization-code
(browser login), body filters, and headless server mode are Phase 2+.

## Alternatives considered

- **Forward proxy with MITM CA** instead of named-upstream reverse proxy — rejected for
  Plan 1: requires the agent to trust a custom CA and complicates body inspection. The
  named-upstream model keeps secrets server-side and bodies plaintext with no CA. (May be
  added as an optional mode in Phase 3.)
- **A zoo of per-API MCP servers** — rejected: the user explicitly wants ONE MCP that is a
  discovery/catalog control plane, with plain HTTP as the data plane.
- **CGO SQLite (`mattn/go-sqlite3`)** — rejected: keeps the server binary CGO-free and
  cross-compilable; pure-Go `modernc.org/sqlite` is the launcher-proven choice.

## Consequences

- The agent integration is trivial: discover via MCP, then plain HTTP with one bearer header.
- Full request/response visibility (no MITM) enables real filtering + honest audit.
- Per-secret encryption + locked-by-default vault means the data plane is inert until an
  operator unlocks — safe by default.
- Plan 1 uses a flat `grant` allow-list as a stand-in for the full `policy` engine (Plan 2);
  the proxy's allow/deny call site is the seam where `policy` replaces `grant`.
- A future forward-proxy/MITM mode or headless server mode would each warrant its own ADR.
