# module: internal/cli

The cobra command tree. `serve` runs the daemon; all other commands are thin clients of the
admin socket. Persistent flags `--socket`, `--db`, `--listen` default under the user config
dir. `vault init`/`unlock` prompt for the master password via the terminal
(`golang.org/x/term`).

Commands: `serve`, `vault init|unlock|status`, `upstream add|list`, `agent register|list`,
`rule add|list|delete`, `approval list|resolve`.

## Public API

- `NewRootCmd() *cobra.Command` — builds the root command with all subcommands registered.
