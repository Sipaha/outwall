// Package cli defines the outwall command tree.
package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Sipaha/outwall/internal/client"
	"github.com/Sipaha/outwall/internal/version"
)

type globalFlags struct {
	socket string
	db     string
	listen string
}

func defaultDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "outwall")
	}
	return ".outwall"
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

	root.AddCommand(
		newServeCmd(gf),
		newVaultCmd(gf),
		newUpstreamCmd(gf),
		newAgentCmd(gf),
		newRuleCmd(gf),
		newApprovalCmd(gf),
	)
	return root
}

func newClient(gf *globalFlags) *client.Client { return client.New(gf.socket) }
