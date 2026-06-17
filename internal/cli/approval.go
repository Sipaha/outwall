package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newApprovalCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "approval", Short: "Manage pending approvals"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List requests awaiting approval",
		RunE: func(c *cobra.Command, _ []string) error {
			var out []map[string]string
			if err := newClient(gf).Do("GET", "/approvals", nil, &out); err != nil {
				return err
			}
			for _, p := range out {
				fmt.Fprintf(c.OutOrStdout(), "%s\t%s\t%s\t%s\t%s\n",
					p["id"], p["agent_id"], p["upstream_id"], p["method"], p["path"])
			}
			return nil
		},
	}

	var approve, deny bool
	resolve := &cobra.Command{
		Use:   "resolve <id>",
		Short: "Approve or deny a pending request",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if approve == deny {
				return fmt.Errorf("specify exactly one of --approve or --deny")
			}
			if err := newClient(gf).Do("POST", "/approvals/"+args[0]+"/resolve",
				map[string]bool{"approve": approve}, nil); err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), "resolved")
			return nil
		},
	}
	resolve.Flags().BoolVar(&approve, "approve", false, "approve the request")
	resolve.Flags().BoolVar(&deny, "deny", false, "deny the request")

	cmd.AddCommand(list, resolve)
	return cmd
}
