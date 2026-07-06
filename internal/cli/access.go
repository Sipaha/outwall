package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newAccessCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "access", Short: "Inspect and resolve access-request intents"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List access-request intents (newest first)",
		RunE: func(c *cobra.Command, _ []string) error {
			var out []map[string]string
			if err := newClient(gf).Do("GET", "/access-requests", nil, &out); err != nil {
				return err
			}
			for _, r := range out {
				name := r["agent_name"]
				if name == "" {
					name = r["agent_id"]
				}
				up := r["upstream_name"]
				if up == "" {
					up = r["upstream_id"]
				}
				fmt.Fprintf(c.OutOrStdout(), "%s\t%s\t%s\t%s\t%s\n",
					r["id"], name, up, r["status"], r["purpose"])
			}
			return nil
		},
	}

	var status string
	resolve := &cobra.Command{
		Use:   "resolve <id>",
		Short: "Record a decision on an access request (granted|denied|dismissed)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if status == "" {
				return fmt.Errorf("--status is required (granted|denied|dismissed)")
			}
			if err := doPrivileged(gf, "POST", "/access-requests/"+args[0]+"/resolve",
				map[string]string{"status": status}, nil); err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), "resolved")
			return nil
		},
	}
	resolve.Flags().StringVar(&status, "status", "", "decision: granted|denied|dismissed")

	cmd.AddCommand(list, resolve)
	return cmd
}
