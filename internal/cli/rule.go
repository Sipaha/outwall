package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newRuleCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "rule", Short: "Manage policy rules"}

	var (
		upstreamID, agentID, method, pathGlob, outcome string
		namespace, resource, verb                      string
		rate                                           int
	)
	add := &cobra.Command{
		Use:   "add",
		Short: "Add a policy rule",
		RunE: func(c *cobra.Command, _ []string) error {
			req := map[string]any{
				"upstream_id":        upstreamID,
				"subject_agent_id":   agentID,
				"method":             method,
				"path_glob":          pathGlob,
				"outcome":            outcome,
				"rate_limit_per_min": rate,
				"namespace":          namespace,
				"resource":           resource,
				"verb":               verb,
			}
			var out map[string]string
			if err := newClient(gf).Do("POST", "/rules", req, &out); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "created rule id=%s\n", out["id"])
			return nil
		},
	}
	add.Flags().StringVar(&upstreamID, "upstream", "", "upstream id (required)")
	add.Flags().StringVar(&agentID, "agent", "", "subject agent id (default: any agent)")
	add.Flags().StringVar(&method, "method", "*", "HTTP method or * for any")
	add.Flags().StringVar(&pathGlob, "path", "/**", "path glob (* within segment, ** across)")
	add.Flags().StringVar(&outcome, "outcome", "", "allow|deny|require-approval (required)")
	add.Flags().IntVar(&rate, "rate", 0, "rate limit per minute (0 = unlimited)")
	add.Flags().StringVar(&namespace, "namespace", "", "k8s namespace glob (k8s rules; use * for all)")
	add.Flags().StringVar(&resource, "resource", "", "k8s resource glob, e.g. pods or pods/log (k8s rules)")
	add.Flags().StringVar(&verb, "verb", "", "k8s verb, e.g. get|list|watch or * (k8s rules)")
	_ = add.MarkFlagRequired("upstream")
	_ = add.MarkFlagRequired("outcome")

	list := &cobra.Command{
		Use:   "list",
		Short: "List policy rules",
		RunE: func(c *cobra.Command, _ []string) error {
			var out []map[string]any
			if err := newClient(gf).Do("GET", "/rules", nil, &out); err != nil {
				return err
			}
			for _, r := range out {
				fmt.Fprintf(c.OutOrStdout(), "%v\t%v\t%v\t%v\t%v\t%v\trate=%v\n",
					r["id"], r["subject_agent_id"], r["upstream_id"], r["method"],
					r["path_glob"], r["outcome"], r["rate_limit_per_min"])
			}
			return nil
		},
	}

	del := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a policy rule",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := newClient(gf).Do("DELETE", "/rules/"+args[0], nil, nil); err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), "deleted")
			return nil
		},
	}

	cmd.AddCommand(add, list, del)
	return cmd
}
