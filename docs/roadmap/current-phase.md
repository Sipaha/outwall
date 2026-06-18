# Current phase

> Forward-only. What's active now and what's queued next. Finished work lives in git.

## Active phase

**Phase 2 — Kubernetes egress gateway: COMPLETE.** All three milestones (K1 read+streaming, K2
mutate+approval, K3 exec/attach/cp/port-forward) shipped, merged, and pushed. Agents now get
controlled access to Kubernetes clusters (read logs/resources, change workloads, exec) through the
same request-rights + approval + audit flow — cluster credentials never reach the agent. Design
spec: `docs/superpowers/specs/2026-06-18-outwall-k8s-gateway-design.md`; ADR-0008/0009/0010.

**Active: Plan K5 — kubeconfig import fixes** (found in real use). Three root causes: the import
response encodes empty lists as JSON `null` → the UI's `res.added.length` throws → false "Failed to
import clusters" toast (import actually succeeds); auto-import reads only `~/.kube/config` so only 1 of
the user's clusters (spread across `~/.kube/*.yaml|*.conf`) is pulled; the "Import from kubeconfig"
button should be a **file picker**, not a fixed-path rescan. Plan:
`docs/superpowers/plans/2026-06-18-outwall-k8s-k5-import-fixes.md`. ADR-0012.

After K5: no active phase — pick with the user. Candidates below.

## Done

- **Plan K4 — Clusters UI + kubeconfig auto-import + dark form controls.** `color-scheme: dark`
  fixes the light native `<select>`/scrollbars (WebKitGTK UA theming); a `yaml.v3` kubeconfig parser
  (`internal/k8s/kubeconfigimport.go`, no client-go) + an idempotent skip-existing `Importer` run
  best-effort on vault unlock + `POST /clusters/import`; a **Clusters** UI screen (register/list/rm,
  "Import from kubeconfig", show-kubeconfig, red "insecure" badge) with the HTTP **Upstreams** list
  filtered to `kind=http`; `AuthConfig.K8sInsecureSkipVerify` honored only from an explicit
  `insecure-skip-tls-verify` in the operator kubeconfig (CA always wins, loud `slog.Warn`). All green
  (`-race`, web 18 tests + lint, CGO-free + desktop builds). ADR-0011.

- **Plan K3 — k8s exec / attach / cp / port-forward.** `RequestInfo.IsUpgrade()` +
  exec/attach/portforward → RBAC verb `create`; the proxy's upgrade branch reuses
  `httputil.ReverseProxy`'s native `Upgrade` handling + the per-cluster TLS transport, gates the
  upgrade through policy + the blocking approval queue **before** the `101`, and skips the
  `ModifyResponse` body-capture on a `101` (which would otherwise `502` a duplex stream);
  metadata-only audit (cluster/ns/pod/command/duration/bytes-in-out, no body blob) via a counting
  hijacked-conn wrapper. Stdlib-only — no websocket/client-go dependency. All green under `-race`,
  CGO-free + desktop builds. ADR-0010.

- **Plan K2 — k8s mutating verbs + approval.** Mutating k8s verbs (patch/update/create/delete/
  deletecollection) gated by the existing blocking approval queue; the proxy captures the request
  body once up-front (full bytes forwarded; a `BodyCap`-capped, credential-masked preview on
  `approval.Pending.RequestBody`); the approvals admin API + `approval.enqueued` SSE carry the
  `(namespace,resource,verb)` tuple + the masked body; `audit.MaskBody` redacts `Bearer`/
  `Authorization` secrets; Web UI gained a k8s-aware Rules editor + a patch-diff Approvals card.
  All green (`-race`, web vitest+lint, CGO-free + desktop builds). ADR-0009.

- **Plan K1 — Kubernetes read gateway.** `internal/k8s` RBAC path parser + agent kubeconfig;
  `internal/tlsca` local CA + TLS data plane (data plane is now HTTPS; clients trust the local
  CA); `Kind="k8s"` cluster targets with token/client-cert/**exec-plugin** auth + per-cluster TLS
  transport seam (`authn.Manager.Transport`); `policy` extended to `(namespace,resource,verb)`
  with the namespace-safety property (empty ns matches only `*`); proxy k8s routing + cluster
  token injection + log/watch **streaming**; `outwall cluster`/`kubeconfig` CLI; MCP cluster
  discovery + `get_kubeconfig`. All green under `-race`, CGO-free build. ADR-0008.
  (Note: `outwall kubeconfig` takes `--token` — agent tokens are write-once/SHA-only stored.)

- **Plan 1 — Foundation & data-plane skeleton.** Vault (Argon2id+AES-GCM), SQLite store,
  upstream/agent registries, none/static/basic authenticators, data-plane reverse proxy
  (default-deny + auth injection + 503-when-locked), daemon `serve` + unix-socket admin API +
  CLI. e2e verified against httpbin. ADR-0001.
- **Plan 2 — Policy + approval + OIDC-CC.** `policy` engine (rules subject×upstream×method×
  path-glob×rate-limit → allow/deny/require-approval; agent>any>default-deny, most-restrictive
  wins), in-memory fixed-window rate limiter, blocking `approval` queue (5-min long-poll),
  `oidc-client-credentials` authenticator + per-upstream token-cache Manager. `grant`
  package/table deleted (alpha, no legacy path). rules/approvals admin API + `rule`/`approval`
  CLI. e2e (approval + 429) verified. ADR-0002.
- **Plan 3 — MCP control plane.** Single streamable-HTTP MCP server (go-sdk v1.6.1) on its own
  localhost listener: tools `list_upstreams`/`request_access(host,purpose)`/`get_access`/`whoami`;
  session=agent auto-registration + bearer-token mint on first contact; access-request intent
  log (captures purpose); SDK-free `mcpsvc` + thin `mcp` adapter. e2e via live SDK client. ADR-0003.
- **Plan 4 — Audit.** Data-plane request journal (`audit_log` + separate `audit_bodies` tables)
  with capped streaming body capture (≤256 KB, truncation + total size; non-text → metadata-only
  + sha256), masked request headers, decision/rule recorded; entry written on response-body
  close; deny/429/approval-denied/502 early outcomes recorded. Admin API + `audit list|show|prune`
  CLI. Nil-safe (prior behavior unchanged when absent). e2e verified. ADR-0004.
- **Plan 5 — Control API + SSE.** In-process non-blocking event `Bus` (drop-on-full), publishers
  in approval/audit/mcpsvc + admin handlers (`agent.registered`, `upstream.created`, `rule.created`,
  `vault.unlocked`, `approval.enqueued/resolved`, `audit.recorded`, `access.requested`), `GET /events`
  SSE handler (heartbeat), and a `UIListen` loopback TCP bind serving the admin mux behind an
  `X-Outwall-CSRF` gate. e2e SSE verified. ADR-0005.
- **Plan 6A — Web UI foundation.** Embedded React 19 / Vite 8 / Tailwind 4 / Zustand UI built
  into `internal/daemon/webdist` (`go:embed`), served on `UIListen` with `/api` prefix + SPA
  static + SSE CSRF-exempt. Dark Darcula/Lens theme, typed `api.ts` (`X-Outwall-CSRF`), SSE store,
  app shell (sidebar), **Unlock** + **Dashboard** (agents + live approval queue). Live browser
  smoke verified. ADR-0006.

- **Plan 6B — Web UI screens.** Upstreams (CRUD + conditional auth form), Agents (+ detail), Rules
  editor (name-resolved + delete), Approvals (pending + access-request intents), Audit (journal +
  body viewer + masked headers), Settings (audit prune + vault lock; `POST /vault/lock` added). All
  six routes live. Live browser smoke verified all screens. (Within ADR-0006.)

- **Plan 7 — Wails 3 desktop wrapper.** `cmd/outwall-desktop` (`//go:build desktop`, CGO+GTK4)
  runs the daemon **in-process** (CGO-free `internal/desktop.Run` helper + readiness wait, unit
  tested) and renders the embedded UI in a Wails v3 (`v3.0.0-alpha2.103`) webview pointed at
  `UIListen`. Server binary stays CGO-free via the `desktop` build tag. `make build-desktop`
  (rebuilds the web bundle first so the real UI is embedded). xvfb launch healthy. ADR-0007.

## Phase 2+ (deferred by design)

OIDC authorization-code (browser login), request body/param filters, ticket/async approval
fallback, audit auto-prune, additional authenticators (mTLS/SigV4/HMAC), headless server mode.
