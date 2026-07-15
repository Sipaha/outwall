# module: internal/cli

The cobra command tree. `serve` runs the daemon; the operator commands are thin clients of the
admin socket, the agent-facing commands are thin clients of the agent socket. Persistent flags
`--socket` (admin), `--agent-socket` (agent plane, ADR-0040), `--db`, `--listen`, `--ui-listen`,
`--callback-listen`, `--browse-domain` default under the user config dir and are wired into
`daemon.Config` by `serve`. There is no `--mcp-listen` — the MCP control plane (ADR-0003) was
removed; see ADR-0040. `vault init`/`unlock` prompt for the master password via the terminal
(`golang.org/x/term`), or read it from stdin with `--password-stdin` (no TTY needed —
`printf 'pw' | outwall vault unlock --password-stdin`; ADR-0018).

**Operator commands:** `serve`, `vault init|unlock|status`, `upstream add|list`, `cluster
add|list|remove`, `kubeconfig`, `agent register|list`, `rule add|list|delete`, `approval
list|resolve`, `access list|resolve` (`resolve <id> --status granted|denied|dismissed`), `audit
list [--limit N]|show <id>|prune --older-than <dur|RFC3339>` (`--older-than` accepts a Go
duration like `720h` → now−dur, or an RFC3339 date). Privileged operator mutations (vault
unlock/lock, upstream/cluster/rule/agent create, resolve, audit prune, kubeconfig import) go
through `doPrivileged` (`session.go`): it calls the admin route directly, and only on a
`{"error":"operator session required"}` response (ADR-0041) does it prompt for the master
password on the TTY, `POST /operator/session/open`, and retry once — sudo-style, so an
already-open session (within its idle TTL) never prompts.

**Agent-facing commands** (over `--agent-socket`, ADR-0040): `list-upstreams`, `whoami`,
`request-host-access <host> --purpose`, `request-access <host> --method --path --var
name:type ... --value ... --purpose` (`--var` declares a typed operation variable, repeatable),
`request-preset <upstream> --preset --var ... --purpose`, `request-k8s-access <cluster>
--namespace --grant resource=verb1,verb2 --purpose`, `get-access <upstream>` (waits on a pending
decision), `get-kubeconfig <cluster>`. Each resolves (and mints once, via `internal/agentid`) the
calling project's bearer token before calling the agent socket.

`instructions` (`--json`) prints the always-current agent playbook for this daemon — the resolved
data-plane URL/port, the browser origin + `outwall_token` cookie, the CA path and the loopback
NO_PROXY gotcha, and the caller's own per-project identity — rendered from `GET /instructions`. It
lets `~/.claude/CLAUDE.md` (and other agent docs) shrink to a bootstrap reference instead of
hand-copied facts that drift.

**Stale-token self-heal (`doAgent`).** All agent commands route through `doAgent`, which mirrors the
operator-side `doPrivileged`: it runs the authenticated call and, if the daemon rejects the persisted
token with the stale-token gate (`missing or invalid agent token`, e.g. after a daemon DB reset),
drops the cached token (`agentid.Invalidate`), re-registers once (minting a fresh token), and retries
exactly once. A call that succeeds with the cached token never re-registers.

## Public API

- `NewRootCmd() *cobra.Command` — builds the root command with all subcommands registered.
