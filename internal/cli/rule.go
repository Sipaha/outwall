package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newRuleCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "rule", Short: "Manage policy rules"}

	var (
		upstreamID, agentID, outcome string
		opMethod, opPath             string
		opValues                     []string
		namespace, resource, verb    string
		rate                         int
	)
	add := &cobra.Command{
		Use:   "add",
		Short: "Add a policy rule (HTTP operation rule, or a k8s rule with --namespace/--resource/--verb)",
		RunE: func(c *cobra.Command, _ []string) error {
			req := map[string]any{
				"upstream_id":        upstreamID,
				"subject_agent_id":   agentID,
				"outcome":            outcome,
				"rate_limit_per_min": rate,
				"namespace":          namespace,
				"resource":           resource,
				"verb":               verb,
			}
			isK8s := namespace != "" || resource != "" || verb != ""
			if !isK8s {
				// HTTP operation rule: method + path-template + per-variable value policies.
				policies, err := parseOpValues(opValues)
				if err != nil {
					return err
				}
				req["op_method"] = opMethod
				req["op_path_template"] = opPath
				req["op_value_policies"] = policies
			}
			var out map[string]string
			if err := doPrivileged(gf, "POST", "/rules", req, &out); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "created rule id=%s\n", out["id"])
			return nil
		},
	}
	add.Flags().StringVar(&upstreamID, "upstream", "", "upstream id (required)")
	add.Flags().StringVar(&agentID, "agent", "", "subject agent id (default: any agent)")
	add.Flags().StringVar(&outcome, "outcome", "", "allow|deny|require-approval (required)")
	add.Flags().IntVar(&rate, "rate", 0, "rate limit per minute (0 = unlimited)")
	add.Flags().StringVar(&opMethod, "op-method", "GET", "HTTP method for an operation rule")
	add.Flags().StringVar(&opPath, "op-path", "", "operation path-template, e.g. /projects/{project_path:text}/pipelines")
	add.Flags().StringArrayVar(&opValues, "op-value", nil, "text-var allowed value: var=value (repeatable); 'var=*' trusts any value")
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
				fmt.Fprintf(c.OutOrStdout(), "%v\t%v\t%v\t%v %v\t%v\trate=%v\n",
					r["id"], r["subject_agent_id"], r["upstream_id"], r["op_method"],
					r["op_path_template"], r["outcome"], r["rate_limit_per_min"])
			}
			return nil
		},
	}

	del := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a policy rule",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := doPrivileged(gf, "DELETE", "/rules/"+args[0], nil, nil); err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), "deleted")
			return nil
		},
	}

	cmd.AddCommand(add, list, del)
	return cmd
}

// valuePolicy mirrors policy.ValuePolicy for the JSON request body (CLI does not import policy).
type valuePolicy struct {
	Type   string   `json:"type"`
	Mode   string   `json:"mode"`
	Values []string `json:"values"`
}

// parseOpValues turns repeated "var=value" flags into per-variable text value policies. A value
// of "*" sets the variable to mode "any" (trust any value); otherwise the value is added to the
// variable's allowed-set.
func parseOpValues(pairs []string) (map[string]valuePolicy, error) {
	out := map[string]valuePolicy{}
	for _, p := range pairs {
		name, val, ok := strings.Cut(p, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("invalid --op-value %q (want var=value)", p)
		}
		vp := out[name]
		vp.Type = "text"
		if val == "*" {
			vp.Mode = "any"
		} else {
			if vp.Mode != "any" {
				vp.Mode = "set"
			}
			vp.Values = append(vp.Values, val)
		}
		out[name] = vp
	}
	return out, nil
}
