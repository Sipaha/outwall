# docs/INDEX.md — documentation map

> The project's bookshelf: what docs exist and where to find them. A development session
> starts here (after `AGENTS.md`/`CLAUDE.md`). When you add a new doc, link it here.
> This index is a **map, not a changelog** — phase-by-phase narrative lives in the ADRs + git.

## Orientation

outwall is a Go desktop daemon (Wails 3 + React UI) acting as an authenticating, filtering,
auditing egress gateway for AI agents calling external HTTP APIs. Sipaha project; no citeck.
Stage: alpha (pre-Plan-1).

**Where to start:**
- New session / what's being built now → [`roadmap/current-phase.md`](roadmap/current-phase.md)
- The architecture → [`architecture/overview.md`](architecture/overview.md)
- The design contract → [`superpowers/specs/2026-06-17-outwall-design.md`](superpowers/specs/2026-06-17-outwall-design.md)
- The active plan → [`superpowers/plans/2026-06-17-outwall-foundation.md`](superpowers/plans/2026-06-17-outwall-foundation.md)
- How we work autonomously → [`workflow/supervisor-mode.md`](workflow/supervisor-mode.md), [`workflow/agent-loop.md`](workflow/agent-loop.md)

## roadmap/ — what is being done, and next

- [`current-phase.md`](roadmap/current-phase.md) — the active phase + queued candidates (forward-only).

## architecture/ — what is built and how

- [`overview.md`](architecture/overview.md) — the overall system picture.
- `decisions/` — ADRs (one architectural decision each, `NNNN-slug.md`):
  - [`0001-stack-and-architecture.md`](architecture/decisions/0001-stack-and-architecture.md) — stack + two-plane gateway architecture.
  - [`0002-policy-and-approval.md`](architecture/decisions/0002-policy-and-approval.md) — rule precedence, blocking approval, rate limiter, OIDC-CC token cache.
  - [`0003-mcp-control-plane.md`](architecture/decisions/0003-mcp-control-plane.md) — MCP server, session=agent identity, SDK-free mcpsvc / thin adapter split, access-request intent log. **Superseded by ADR-0040.**
  - [`0004-audit.md`](architecture/decisions/0004-audit.md) — capped streaming body capture, text/binary classification, masking, record-on-close, data-plane-only scope.
  - [`0005-control-api-sse.md`](architecture/decisions/0005-control-api-sse.md) — non-blocking event bus, SSE, UIListen loopback bind + X-Outwall-CSRF gate. **Amended by ADR-0041: the X-Outwall-CSRF gate is removed, superseded by the master-password operator session.**
  - [`0006-web-ui-foundation.md`](architecture/decisions/0006-web-ui-foundation.md) — Vite→webdist go:embed, /api prefix + SPA serve, SSE CSRF exemption, dark theme tokens.
  - [`0007-desktop-wrapper.md`](architecture/decisions/0007-desktop-wrapper.md) — Wails 3 desktop wrapper runs the daemon in-process; server stays CGO-free via build tag.
  - [`0008-k8s-gateway-read.md`](architecture/decisions/0008-k8s-gateway-read.md) — k8s reverse-proxy; cluster=k8s-kind upstream; transport seam; (namespace,resource,verb) policy + namespace safety; token/client-cert/exec auth; local CA + kubeconfig.
  - [`0009-k8s-mutate-approval.md`](architecture/decisions/0009-k8s-mutate-approval.md) — mutating k8s verbs gated by the blocking approval queue; request body captured once (full forwarded, masked preview on the approval); tuple + masked body on approvals API/SSE.
  - [`0010-k8s-exec-attach.md`](architecture/decisions/0010-k8s-exec-attach.md) — exec/attach/cp/port-forward via ReverseProxy's native Upgrade; gated before the 101; ModifyResponse skipped on 101; metadata-only audit; stdlib-only (no websocket/client-go).
  - [`0011-k8s-clusters-ui-kubeconfig-import.md`](architecture/decisions/0011-k8s-clusters-ui-kubeconfig-import.md) — Clusters UI; yaml.v3 kubeconfig parser + idempotent import on unlock; color-scheme:dark form controls; insecure-skip-tls-verify mirrored from the operator kubeconfig (CA wins, warned).
  - [`0012-kubeconfig-import-scan-and-upload.md`](architecture/decisions/0012-kubeconfig-import-scan-and-upload.md) — import lists non-nil ([] not null); auto-import scans all ~/.kube files; file-picker upload (ImportContent); non-kubeconfig skipped on scan / 400 on upload.
  - [`0013-desktop-single-instance.md`](architecture/decisions/0013-desktop-single-instance.md) — single desktop instance via a flock lock + focus hand-off over the unix admin socket (POST /desktop/focus); gate before port-bind; Linux/GTK focus-stealing workaround. Launcher pattern, not Wails-native.
  - [`0014-operation-access-engine.md`](architecture/decisions/0014-operation-access-engine.md) — HTTP policy = operation templates with typed segment-bounded variables; enforce by parsing the real request + per-variable value-sets; new-value approval extends the set; replaces path-glob (no migration).
  - [`0015-operation-access-mcp-approval.md`](architecture/decisions/0015-operation-access-mcp-approval.md) — lazy host upstream + credential attach; MCP request_host_access + typed request_access (non-blocking, poll get_access); approval resolve creates/extends the operation rule + trust-any.
  - [`0016-operation-access-ui.md`](architecture/decisions/0016-operation-access-ui.md) — host/operation/new-value approval cards (example URL, trust-any, broad-placeholder warning); Operations + Hosts screens; POST /upstreams/{name}/auth + /rules/{id}/value-policy.
  - [`0017-number-enum-variable-types.md`](architecture/decisions/0017-number-enum-variable-types.md) — number (range) + enum (closed-set) operation-variable types; enum/number violation = hard deny vs text new-value = require-approval.
  - [`0018-audit-auto-prune-operational.md`](architecture/decisions/0018-audit-auto-prune-operational.md) — settings KV table + persisted audit retention + hourly background pruner; vault --password-stdin; headless mode documented.
  - [`0019-additional-auth-schemes.md`](architecture/decisions/0019-additional-auth-schemes.md) — mTLS (transport seam), AWS SigV4 (aws-sdk-go-v2, CGO-free), HMAC (documented canonical string) upstream auth.
  - [`0020-request-body-variables.md`](architecture/decisions/0020-request-body-variables.md) — operation variables extracted from the JSON request body (dotted paths, typed); parse-the-real-body enforcement.
  - [`0021-oidc-authorization-code.md`](architecture/decisions/0021-oidc-authorization-code.md) — OIDC authorization-code browser login (x/oauth2 PKCE), token persist-on-refresh, daemon login + /oauth/callback.
  - [`0022-additive-schema-migrations.md`](architecture/decisions/0022-additive-schema-migrations.md) — idempotent additive `ADD COLUMN` migration so additive schema growth no longer forces a DB reset; model breaks still reset in alpha.
  - [`0023-versioned-migration-runner.md`](architecture/decisions/0023-versioned-migration-runner.md) — PRAGMA user_version + ordered run-once migration steps (transactional); baseline step folds in the ADR-0022 reconcile; structural changes appended as later steps.
  - [`0024-deny-reason.md`](architecture/decisions/0024-deny-reason.md) — operator deny reason threaded through the approval queue (`Decision{Approved,Reason}`); surfaced to the agent on the data-plane 403 and via `get_access` (access_requests.reason); reason modal in the Approvals UI.
  - [`0034-server-profiles-and-citeck-plugin.md`](architecture/decisions/0034-server-profiles-and-citeck-plugin.md) — server-profile plugin mechanism (self-registering `Profile` registry, bundled at cmd; `upstreams.profile` + rule `profile_params`; `decide.go` delegates to a claiming profile, raw-http handles the rest); first plugin = Citeck Records (read/write × sourceId × workspace, ecosType gated, workspace-on-read/create only); scopes the "no citeck" rule to the plugin package.
  - [`0035-per-upstream-origin.md`](architecture/decisions/0035-per-upstream-origin.md) — per-upstream browser origin `https://<name>.outwall.localhost:<port>/` (Host-routed on the same listener; path-prefix unchanged); per-SNI cert (covers dotted names); browse-mode Location + Set-Cookie-Domain rewriting (no content rewriting); `browse_url` discovery; closes the ADR-0033 single-origin confused-deputy.
  - [`0036-browse-rule-and-readonly-preset.md`](architecture/decisions/0036-browse-rule-and-readonly-preset.md) — browse rule primitive (`BrowseMethods` comma-set × `BrowsePath` glob, coexists with operation templates, evaluated on raw-http path for non-Records Citeck paths too); Citeck ReadOnly preset = browse rule + citeck `op:read` rule (UI posts two ordinary rules, no backend preset endpoint); `recordsOp` suffix broadened to `/records/{op}` (covers `/gateway/records/query` variant); Upstreams page split into HTTP/Citeck/Kubernetes tabs.
  - [`0037-presets-first-class.md`](architecture/decisions/0037-presets-first-class.md) — presets as a first-class, agent-requested concept: plugin-defined (`Profile.Presets()` + core http catalog); expand via `Build` to profile-neutral `RuleTemplate`s (avoids `serverprofile↔policy` import cycle); `request_preset` MCP tool; `KindPreset` approval reusing `access_requests` + `approval.Queue`; fan-out maps each `RuleTemplate` to an agent-scoped `policy.Rule` (subject = requesting agent — fixes the ADR-0036 host-wide any-agent issue); operator may narrow slot bindings before approve, re-validated server-side; `POST /presets/preview` dry-run; v1 catalog: `Browse (GET)` (core), `ReadOnly`/`ReadWrite` (citeck, with `workspace.AllowAny=false` per ADR-0034 semantics).
  - [`0038-atomic-rule-fanout.md`](architecture/decisions/0038-atomic-rule-fanout.md) — atomic multi-rule fan-out: `policy.Registry.CreateMany` writes a batch of rules in one SQL transaction (all-or-nothing); `insertRule(rowExecutor, Rule)` helper centralizes the 18-column INSERT (shared by `Create` and `CreateMany`, eliminating column-order-drift hazard); both fan-out sites (`approvePreset`, `approveK8sAccess`) use `CreateMany`. Resolves the atomicity known limitation in ADR-0037.
  - [`0039-workspace-filtering-and-allow-any.md`](architecture/decisions/0039-workspace-filtering-and-allow-any.md) — `workspace: *` grants and mode-aware read-query workspace filtering: lifts `AllowAny=false` on the workspace slot so operators can grant all-workspace read access; introduces `Profile.Authorize` + `AuthResult{RewriteBody,Response}` seam; citeck plugin narrows multi-workspace Records queries to the agent's allowed set (browser: inject/narrow; API: deny-if-any-unallowed); proxy applies `RewriteBody`/synthetic `Response` without touching the upstream.
  - [`0040-agent-socket-control-plane.md`](architecture/decisions/0040-agent-socket-control-plane.md) — replaces the MCP control plane with `internal/agentapi` (plain HTTP/JSON over the `0600` unix socket `agent.sock`, no session cache) fronted by CLI subcommands (`list-upstreams`, `whoami`, `request-host-access`, `request-access` incl. `--var name:type` typed value-scoping, `request-preset`, `request-k8s-access`, `get-access`, `get-kubeconfig`); `internal/agentid` mints a per-project accountability token (keyed on the realpath of the git top-level, flock mint-once) that survives daemon restarts; supersedes ADR-0003.
  - [`0041-operator-session-master-password.md`](architecture/decisions/0041-operator-session-master-password.md) — seal the operator plane: route split (ungated read-only + session-control vs master-password-gated mutations), operator session (Verify-without-unlock, idle-TTL sliding window, Lock now) gating BOTH transports, retire static X-Outwall-CSRF; threat model (same-user boundary = master password; separate-OS-user = escalation; PR_SET_DUMPABLE = non-load-bearing DiD); R7 Wails-bindings deferred to Plan 3.
- `modules/` — per-package API docs: `secret`, `store`, `upstream`, `agent`, `authn`,
  `policy`, `approval`, `access`, `mcpsvc`, `agentapi`, `agentid`, `audit`, `events`, `proxy`, `daemon`, `client`, `cli`, `version`, `k8s`, `tlsca`, plus `webui` (the `web/` app).

## workflow/ — how we work

- [`agent-loop.md`](workflow/agent-loop.md) — the six-phase routine for each task.
- [`supervisor-mode.md`](workflow/supervisor-mode.md) — autonomous phase development (supervisor only).
- [`doc-discipline.md`](workflow/doc-discipline.md) — what doc to write when; ADRs vs git.
- [`adr-template.md`](workflow/adr-template.md) — the ADR format.

## superpowers/ — specs & plans

- `specs/` — brainstormed design specs.
  - [`2026-06-17-outwall-design.md`](superpowers/specs/2026-06-17-outwall-design.md) — Phase 1 (HTTP egress gateway).
  - [`2026-06-18-outwall-k8s-gateway-design.md`](superpowers/specs/2026-06-18-outwall-k8s-gateway-design.md) — Kubernetes gateway (cluster targets, namespace-scoped policy, exec auth, kubeconfig, streaming/exec).
  - [`2026-06-18-outwall-operation-access-design.md`](superpowers/specs/2026-06-18-outwall-operation-access-design.md) — operation-template + typed-variable HTTP access model (host=upstream, approve-on-request, value-set control, parse-from-request enforcement). Replaces HTTP path-glob rules.
  - [`2026-06-22-server-profiles-and-per-upstream-origin-design.md`](superpowers/specs/2026-06-22-server-profiles-and-per-upstream-origin-design.md) — server-profile plugin mechanism (core stays platform-agnostic; first plugin = Citeck Records read/write-per-sourceId/workspace policy) + per-upstream subdomain origin for browser browsing. ADRs 0034/0035.
  - [`2026-06-23-browse-policy-and-upstream-zones-design.md`](superpowers/specs/2026-06-23-browse-policy-and-upstream-zones-design.md) — browse rule primitive (allow-method × path-glob) for browsing; Citeck ReadOnly / Allow-GET presets; `recordsOp` path broadening; Upstreams page split into HTTP/Citeck/Kubernetes tabs. ADR-0036.
  - [`2026-06-24-workspace-filtering-and-allow-any-design.md`](superpowers/specs/2026-06-24-workspace-filtering-and-allow-any-design.md) — workspace filtering: `workspace: *` grant allowance, mode-aware narrowing of Records read queries (browser narrows/injects; API denies on unallowed workspace), `Profile.Authorize` + `AuthResult{RewriteBody,Response}` extension seam. ADR-0039.
  - [`2026-07-06-mcp-to-direct-socket-design.md`](superpowers/specs/2026-07-06-mcp-to-direct-socket-design.md) — replace the MCP control plane with a direct **agent socket + CLI** (per-project accountability token, stateless calls immune to start-order/restart); **seal the operator plane** behind a master-password operator session (idle-TTL, Wails-bindings delivery, route split); remove the go-sdk. Threat model made explicit (same-user boundary = master password; separate-OS-user recorded as escalation). Supersedes ADR-0003, amends ADR-0005.
- `plans/` — bite-sized implementation plans.
  - Server profiles: [`2026-06-22-server-profiles-citeck.md`](superpowers/plans/2026-06-22-server-profiles-citeck.md) (Parts A/B/C — shipped, ADR-0034), [`2026-06-22-per-upstream-origin.md`](superpowers/plans/2026-06-22-per-upstream-origin.md) (Part D — subdomain data plane, ADR-0035).
  - Browse policy + zones: [`2026-06-23-browse-policy-and-upstream-zones.md`](superpowers/plans/2026-06-23-browse-policy-and-upstream-zones.md) (browse rule, Citeck ReadOnly preset, recordsOp broadening, Upstreams tabs; ADR-0036).
  - Phase 1: the `2026-06-17-outwall-*` plans (foundation … wails-desktop), all shipped.
  - Phase 3 (operation-access): [`…-opaccess-h1-engine.md`](superpowers/plans/2026-06-18-outwall-opaccess-h1-engine.md) (active),
    [`…-opaccess-h2-mcp-approval.md`](superpowers/plans/2026-06-18-outwall-opaccess-h2-mcp-approval.md),
    [`…-opaccess-h3-ui.md`](superpowers/plans/2026-06-18-outwall-opaccess-h3-ui.md).
  - Phase 2 (k8s): [`…-k8s-k1-read.md`](superpowers/plans/2026-06-18-outwall-k8s-k1-read.md) (active),
    [`…-k8s-k2-mutate.md`](superpowers/plans/2026-06-18-outwall-k8s-k2-mutate.md),
    [`…-k8s-k3-exec.md`](superpowers/plans/2026-06-18-outwall-k8s-k3-exec.md).
  - Seal the operator plane: [`2026-07-06-seal-operator-plane.md`](superpowers/plans/2026-07-06-seal-operator-plane.md) (secret.Verify, internal/opsession, route split + operator gate on both transports, CLI sudo-style helper, web prompt; ADR-0041). Spec: [`2026-07-06-mcp-to-direct-socket-design.md`](superpowers/specs/2026-07-06-mcp-to-direct-socket-design.md).

## findings/ — non-obvious discoveries

- [`2026-06-vault-cli-needs-tty.md`](findings/2026-06-vault-cli-needs-tty.md) — `vault init`/`unlock` need a real TTY; add `--password-stdin` later.
