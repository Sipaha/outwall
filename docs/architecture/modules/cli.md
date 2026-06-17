# module: internal/cli

The cobra command tree. `serve` runs the daemon; all other commands are thin clients of the
admin socket. Persistent flags `--socket`, `--db`, `--listen`, `--mcp-listen` default under the
user config dir (`--mcp-listen` defaults to `127.0.0.1:8181` and is wired into `daemon.Config`
by `serve`). `vault init`/`unlock` prompt for the master password via the terminal
(`golang.org/x/term`).

Commands: `serve`, `vault init|unlock|status`, `upstream add|list`, `agent register|list`,
`rule add|list|delete`, `approval list|resolve`, `access list|resolve` (`resolve <id>
--status granted|denied|dismissed`), `audit list [--limit N]|show <id>|prune --older-than
<dur|RFC3339>` (`--older-than` accepts a Go duration like `720h` → now−dur, or an RFC3339 date).

## Public API

- `NewRootCmd() *cobra.Command` — builds the root command with all subcommands registered.
