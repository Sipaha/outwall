package cli

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Sipaha/outwall/internal/daemon"
)

func newServeCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the outwall daemon (data plane + admin socket)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := os.MkdirAll(filepath.Dir(gf.db), 0o700); err != nil {
				return fmt.Errorf("create data dir: %w", err)
			}
			d, err := daemon.New(daemon.Config{
				DBPath: gf.db, SocketPath: gf.socket, Listen: gf.listen,
				MCPListen: gf.mcpListen, UIListen: gf.uiListen,
			})
			if err != nil {
				return err
			}
			defer d.Close()
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			fmt.Fprintf(cmd.OutOrStdout(), "outwall serving: data plane %s, mcp %s, ui %s, admin %s\n",
				gf.listen, gf.mcpListen, gf.uiListen, gf.socket)
			return d.Serve(ctx)
		},
	}
}
