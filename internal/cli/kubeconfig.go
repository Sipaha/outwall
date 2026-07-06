package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newKubeconfigCmd(gf *globalFlags) *cobra.Command {
	var token string
	cmd := &cobra.Command{
		Use:   "kubeconfig <cluster>",
		Short: "Print an agent kubeconfig for a cluster (uses the agent's own outwall token)",
		Long: "Assembles a kubeconfig pointing at the outwall data plane for <cluster>. The\n" +
			"agent's own outwall bearer token is the only credential; the cluster's real\n" +
			"credentials never appear. Pass the token via --token (obtained from `agent register`).",
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			req := map[string]any{"cluster": args[0], "token": token}
			var out map[string]string
			if err := doPrivileged(gf, "POST", "/kubeconfig", req, &out); err != nil {
				return err
			}
			fmt.Fprint(c.OutOrStdout(), out["kubeconfig"])
			return nil
		},
	}
	cmd.Flags().StringVar(&token, "token", "", "the agent's outwall bearer token (required)")
	_ = cmd.MarkFlagRequired("token")
	return cmd
}
