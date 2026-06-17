package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newGrantCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "grant <agent_id> <upstream_id>",
		Short: "Grant an agent access to an upstream",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			if err := newClient(gf).Do("POST", "/grants",
				map[string]string{"agent_id": args[0], "upstream_id": args[1]}, nil); err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), "granted")
			return nil
		},
	}
}
