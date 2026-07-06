# AGENTS.md

> Guidance for AI coding agents working in this repository. Claude Code reads it through
> the `CLAUDE.md` symlink (which points here); other agents read `AGENTS.md` directly.
> Kept deliberately short — details live in `docs/`. Docs answer "what is" / "what will be";
> the record of how things changed lives in **git + the ADRs**, not in the docs.

---

## Project: outwall

outwall is a local **desktop** (Wails 3 + React) Go daemon that is an authenticating,
filtering, auditing **egress gateway** for AI agents calling external HTTP APIs. Goal:
let agents hit ordinary HTTP APIs **without ever seeing secrets** and **without risking
that they take down production**.

**This is a Sipaha project — there is NO connection to citeck/ECOS.** citeck-launcher
(`~/.spk/spk-editor/solutions/citeck-launcher`) is used ONLY as a source of architectural
patterns. The core stays **platform-agnostic**: no `citeck` strings, imports, or branding in
outwall code — with ONE contained exception (ADR-0034), the server-profile plugin package
`internal/serverprofile/citeck` and the persisted profile-name value `"citeck"`. Everywhere else
stays citeck-free.

## Where to start

1. `docs/INDEX.md` — the documentation map.
2. `docs/roadmap/current-phase.md` — what is being built now.
3. The relevant ADRs in `docs/architecture/decisions/`.
4. The design spec: `docs/superpowers/specs/2026-06-17-outwall-design.md`.
5. The active plan: `docs/superpowers/plans/2026-06-17-outwall-foundation.md`.

## Build & test commands

```bash
make run        # rebuild web + desktop and launch the Wails app (picks up all code changes)
make run-server # rebuild web + server and run the daemon (UI at http://127.0.0.1:8182/)
make build      # CGO_ENABLED=0 go build → dist/bin/outwall (server+CLI, web bundle embedded)
make install    # symlink the outwall CLI onto PATH (DESTBIN, default ~/.local/bin) so agents can run it
make build-desktop  # CGO+GTK Wails app → dist/bin/outwall-desktop (web bundle embedded)
make test       # go test ./...
make fmt        # gofmt -w .
make vet        # go vet ./...
make tidy       # go mod tidy
make lint       # ALL linters: golangci-lint (Go) + eslint + tsc (web)
make lint-web   # web only: eslint + tsc          make test-web  # web only: vitest
make check      # FULL pre-release gate — gofmt-check + vet + golangci-lint + go test + eslint + tsc
                # + vitest + build. Run this before every commit/merge/release.
```

## Architecture (one-liner per plane)

- **Control plane** — a plain HTTP/JSON handler on a `0600` unix socket (`agent.sock`), driven by the
  `outwall` CLI (`list-upstreams`, `request-host-access`/`request-access`/`request-preset`,
  `get-access`, `whoami`, `get-kubeconfig`); agents self-register (per-project token). Operator
  mutations are gated by a master-password session. MCP + go-sdk were removed (ADR-0040/0041).
- **Data plane** — reverse proxy `http://localhost:PORT/<upstream>/...` + agent bearer
  token. TLS terminated at outwall (no MITM). Secrets never leave outwall.
- **Vault** — Argon2id(master-password) → key; per-secret AES-256-GCM. Locked at start.
- **Policy** — default-deny; rules `(subject, upstream, method+path, rate-limit) →
  allow|deny|require-approval`. `require-approval` blocks the request until the operator decides.
- **Audit** — full req/resp bodies (≤256 KB), injected creds masked, SQLite.

Full design: the spec above. Package layout: the plan above.

## Hard rules (never break)

- ❌ Module path is **`github.com/Sipaha/outwall`** — exact, in every import.
- ⚠️ **No `citeck`** strings / imports / branding in the core. The ONE exception (ADR-0034) is the
  server-profile plugin `internal/serverprofile/citeck` and the persisted profile-name value
  `"citeck"`. Everywhere else (core, proxy, daemon, store, policy, UI chrome) stays citeck-free.
- ❌ **No CGO** in the server binary (`CGO_ENABLED=0`). SQLite via `modernc.org/sqlite`
  (pure-Go). The desktop Wails wrapper is the only CGO target, in its own `cmd/`.
- ❌ **No panics / `log.Fatal` in library code.** Return `error` (wrapped with `%w`).
  Panics only in `main`/tests.
- ❌ **No hacks / workarounds in place of a proper fix.** See Quality bar below.
- ✅ **Add new dependencies at their latest released version** (`go get <mod>@latest`) — never an old pin.
- ❌ **Don't bump *existing* dependency versions** mid-feature — deliberate upgrades are a dedicated "upgrade-X" task.
- ❌ **Don't commit code without updating the docs** (module doc / ADR / finding — whatever applies).
- ❌ **No `Co-Authored-By`** in commit messages. **No `git commit --amend`** / no rewriting
  existing commits — always new commits. (Per the user's global instructions.)

## Always, before committing

```bash
make fmt
make vet
make test
# (make lint once golangci-lint is wired in Plan 2+)
```

If something fails — fix it, don't ignore it.

## Quality bar — production product (alpha)

- **No workaround in place of the root-cause fix.** A sleep to dodge a race, a magic
  constant hiding a logic error, a copy-paste to avoid a refactor, a special-case around a
  broken abstraction — **not acceptable**. Find the root cause.
- **If the proper fix needs an architecture change you can't make cleanly in scope —
  escalate to the user**, don't ship the workaround quietly. Surface the trade-off. A
  *deliberate, reasoned* deviation recorded in an ADR is fine; a quiet shortcut is not.
- **Alpha**: no released schema/format compat to preserve. If breaking a storage format
  simplifies the code, **do it** (one-time DB reset is fine) rather than carrying a legacy
  fallback path. This flips at Beta.

## Code style (Go)

- `gofmt` (tabs). `go vet` clean. golangci-lint (mirror the launcher's v2 config when added).
- `log/slog` for logging. `stretchr/testify` for tests.
- Small, focused files/packages — one clear responsibility each.
- TDD: failing test → minimal code → green → commit. Frequent small commits.

## Autonomous development

The supervisor/sub-agent workflow lives in `docs/workflow/`:
- `agent-loop.md` — the six-phase routine for each task.
- `supervisor-mode.md` — autonomous phase development (read by the top-level supervisor only).
- `doc-discipline.md` — what doc to write when; ADRs vs git.
- `adr-template.md` — the ADR format.

## Git / push

Remote `origin` → `git@github.com-sipaha:Sipaha/outwall.git` (SSH alias `github.com-sipaha`,
identity Sipaha). Push only `main`. Don't force-push; if a push fails, report it.
