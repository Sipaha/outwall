// Package cli defines the outwall command tree.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/Sipaha/outwall/internal/version"
)

// NewRootCmd builds the root cobra command.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "outwall",
		Short:         "Authenticating egress gateway for AI agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.String(),
	}
	return root
}
