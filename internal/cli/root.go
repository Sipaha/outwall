// Package cli defines the outwall command tree.
package cli

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Sipaha/outwall/internal/client"
	"github.com/Sipaha/outwall/internal/config"
	"github.com/Sipaha/outwall/internal/version"
)

type globalFlags struct {
	socket    string
	db        string
	listen    string
	mcpListen string
	uiListen  string
}

func defaultDir() string {
	return config.DataDir()
}

// NewRootCmd builds the root cobra command.
func NewRootCmd() *cobra.Command {
	gf := &globalFlags{}
	root := &cobra.Command{
		Use:           "outwall",
		Short:         "Authenticating egress gateway for AI agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.String(),
	}
	dir := defaultDir()
	root.PersistentFlags().StringVar(&gf.socket, "socket", filepath.Join(dir, "outwall.sock"), "admin unix socket path")
	root.PersistentFlags().StringVar(&gf.db, "db", filepath.Join(dir, "outwall.db"), "database path")
	root.PersistentFlags().StringVar(&gf.listen, "listen", "127.0.0.1:8080", "data-plane listen address")
	root.PersistentFlags().StringVar(&gf.mcpListen, "mcp-listen", "127.0.0.1:8181", "MCP control-plane listen address")
	root.PersistentFlags().StringVar(&gf.uiListen, "ui-listen", "127.0.0.1:8182", "desktop-UI control API + SSE listen address")

	root.AddCommand(
		newServeCmd(gf),
		newVaultCmd(gf),
		newUpstreamCmd(gf),
		newClusterCmd(gf),
		newKubeconfigCmd(gf),
		newAgentCmd(gf),
		newRuleCmd(gf),
		newApprovalCmd(gf),
		newAccessCmd(gf),
		newAuditCmd(gf),
	)
	return root
}

func newClient(gf *globalFlags) *client.Client { return client.New(gf.socket) }
