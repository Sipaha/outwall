# Finding: operator-UI (webview) security hardening + backlog

- **Date:** 2026-07-06
- **Relates to:** ADR-0041 (operator-session master password, threat model), ADR-0007 (Wails desktop wrapper), ADR-0005/0006 (UI listener + web foundation).

## Context

outwall's operator UI is a React SPA rendered in a **WebKitGTK webview** (Wails 3 desktop) and served by
the daemon over the loopback UI listener. Because outwall is a gateway that reaches **external
authenticated resources**, the webview is a security surface: the well-studied desktop failure mode is
*XSS in the UI → native API via the IPC bridge → RCE* (Bishop Fox, "Beyond Electron…", 2026; the Tauri
CVEs CVE-2024-35222 iframe→IPC and CVE-2025-31477 shell-open→RCE). Native toolkits remove the
*webview class* of attack but are "not magically safe"; the load-bearing mitigations are **architecture**
(headless core + untrusted UI client) and **secret discipline**, not the render technology.

An external comparison report (native vs web UI for outwall, 2025–2026) independently confirmed the
architecture outwall already follows and informed the items below.

## As-built posture (verified 2026-07-06)

Already good:
- **Operator webview loads only the local loopback SPA** (`http://127.0.0.1:8182/`,
  `internal/desktop/runner.go` `UIURL`) — never remote or upstream content. Upstream content flows
  through the **data plane** (the agent's browser, a separate per-upstream origin), not the operator
  window.
- **No `dangerouslySetInnerHTML` / `innerHTML` / `eval`** in `web/src` — React escapes by default, so
  even upstream-derived data shown in the UI (audit bodies) renders as inert text.
- **Minimal Wails IPC surface**: only the notifications service and `OpenURL: browser.Open` are
  exposed; no Go binding grants JS arbitrary FS-write/exec (unlike the Tauri CVE pattern).
- **Vault key is zeroized on lock** (`internal/secret/vault.go`) — as much secret-memory hygiene as Go
  allows; the report's Rust/`zeroize` argument is a reason the *real* fix is process/OS-user isolation,
  not in-process tricks.
- Privileged operator actions already require the **master password** (ADR-0041) — a secret the
  same-user agent lacks.

Applied by this finding:
- **Strict CSP on the built bundle** (`web/vite.config.ts` `cspMeta` build-only plugin →
  `<meta http-equiv="Content-Security-Policy">` in the embedded `webdist/index.html`):
  `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; …`.
  `script-src 'self'` (no `'unsafe-inline'`) is the key line; the anti-FOUC background was moved from an
  inline `<script>` to an inline `<style>` so the served document carries **no inline script**.
  Build-only so `vite dev` (HMR needs inline/eval) is unaffected — the webview only loads the built
  bundle.
- **Lint ban on `dangerouslySetInnerHTML`** (`web/eslint.config.js`, `no-restricted-syntax` on the JSX
  attribute) so the "no raw HTML" invariant can't silently regress.

## Same-user isolation — the sharp edge (do not overclaim)

The comparison report recommends "separate process + `SO_PEERCRED`/token on the socket." Against
outwall's actual adversary — an **AI agent running as the same OS user** — that is **insufficient**:
`SO_PEERCRED` cannot distinguish an identical UID, and any token file / process memory is same-user
readable (the same reason `PR_SET_DUMPABLE` was judged non-load-bearing in ADR-0041). The only robust
isolation is a **separate OS identity for the daemon** (separate user / user namespace / systemd
`hidepid`/`ProtectProc`). That remains the recorded **escalation path**, not built. Everything above is
defense-in-depth that *raises the bar*; it is not a substitute for the OS-user boundary.

## Backlog (deferred, proportionate — do NOT rewrite the UI toolkit)

The report's own guidance: architecture first, harden Wails, don't throw it away. Ordered:

1. **(escalation) Split the daemon into a headless core under its own OS identity**, with the UI as an
   untrusted client over the existing unix socket (peer-cred + the master-password session). This is the
   ADR-0041 escalation and the report's #1 recommendation; it is the only thing that truly isolates a
   malicious same-user agent.
2. **R7 / Plan 3 — deliver privileged verbs via Wails native bindings** (minimal exported methods, IPC
   as an explicit trust boundary) instead of the HTTP `/api` endpoint, removing the curl-able surface as
   defense-in-depth (already deferred in ADR-0041).
3. **Track the Wails v3 *alpha* pre-release risk** for a security product; keep WebKitGTK / WebView2
   current (OS-patched webview is a Wails advantage only if updated).

Explicitly **not** planned: migrating the UI toolkit (Slint/Qt/Fyne). Large rewrite, no security win the
above items don't already deliver; the report agrees.
