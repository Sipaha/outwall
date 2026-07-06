package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newAgentCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "agent", Short: "Manage agents"}

	cmd.AddCommand(&cobra.Command{
		Use:   "register <name>",
		Short: "Register an agent and print its token (shown once)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			var out map[string]string
			if err := doPrivileged(gf, "POST", "/agents/register", map[string]string{"name": args[0]}, &out); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "agent id=%s\ntoken=%s\n", out["id"], out["token"])
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List agents",
		RunE: func(c *cobra.Command, _ []string) error {
			var out []map[string]string
			if err := newClient(gf).Do("GET", "/agents", nil, &out); err != nil {
				return err
			}
			for _, a := range out {
				fmt.Fprintf(c.OutOrStdout(), "%s\t%s\t%s\n", a["id"], a["name"], a["status"])
			}
			return nil
		},
	})
	return cmd
}
