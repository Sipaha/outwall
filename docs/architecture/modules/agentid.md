# module: internal/agentid

CLI-side resolution and persistence of the per-project agent token used by the `outwall` CLI's
agent-facing subcommands (`list-upstreams`, `whoami`, `request-*`, `get-access`,
`get-kubeconfig`). New in ADR-0040 (replaces per-session MCP identity).

**Project key.** The realpath of the git top-level when `cwd` is inside a repo, else the realpath
of `cwd` itself — so a `cd` into a subdirectory of the same repo resolves to the same identity
(one agent per project, not per directory).

**Token path.** `<DataDir>/agents/<hex-sha256(projectKey)>.token`, `0600`.

**Mint-once via flock.** `LoadOrRegister` takes an exclusive flock on `<tokenpath>.lock` before
checking for an existing token, so concurrent first-calls from the same project serialize: the
flock winner calls the supplied `register` func (POST `/register` on the agent socket, see
`agentapi.md`) and writes the token atomically (temp file in the same dir + rename, `0600`);
losers block on the flock and then read the already-written file. The token survives daemon
restarts and CLI-process restarts (it is read straight off disk once minted).

**Accountability-only, not an isolation boundary.** Any same-user process can read any project's
token file — this identifies *which project* is acting, it does not gate *who* can act as it
(ADR-0040).

## Public API

- `TokenPath(cwd string) (string, error)` — the token-file path for the project containing `cwd`.
- `LoadOrRegister(cwd string, register func(name string) (id, token string, err error)) (string, error)`
  — returns the per-project token, minting it once (`register` receives the basename of the
  project key as the agent name).
