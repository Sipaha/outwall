package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Sipaha/outwall/internal/upstream"
)

func newClusterCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "cluster", Short: "Manage Kubernetes clusters"}

	var (
		apiURL, caFile, k8sAuth, token string
		clientCertFile, clientKeyFile  string
		execCommand                    string
		execArgs                       []string
	)
	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Register a Kubernetes cluster (kind=k8s)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			auth := upstream.AuthConfig{Type: "none", K8sAuth: k8sAuth}
			if caFile != "" {
				b, err := os.ReadFile(caFile)
				if err != nil {
					return fmt.Errorf("read ca file: %w", err)
				}
				auth.CABundle = string(b)
			}
			switch k8sAuth {
			case "token":
				auth.Token = token
			case "client-cert":
				cert, err := os.ReadFile(clientCertFile)
				if err != nil {
					return fmt.Errorf("read client cert: %w", err)
				}
				key, err := os.ReadFile(clientKeyFile)
				if err != nil {
					return fmt.Errorf("read client key: %w", err)
				}
				auth.ClientCert, auth.ClientKey = string(cert), string(key)
			case "exec":
				auth.ExecCommand = execCommand
				auth.ExecArgs = execArgs
			default:
				return fmt.Errorf("unknown --auth %q (want token|client-cert|exec)", k8sAuth)
			}
			req := map[string]any{
				"name": args[0], "base_url": apiURL, "kind": upstream.KindK8s, "auth": auth,
			}
			var out map[string]string
			if err := newClient(gf).Do("POST", "/upstreams", req, &out); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "registered cluster %s (id=%s)\n", args[0], out["id"])
			return nil
		},
	}
	add.Flags().StringVar(&apiURL, "api-url", "", "Kubernetes API server URL (required)")
	add.Flags().StringVar(&caFile, "ca", "", "PEM file trusting the API server")
	add.Flags().StringVar(&k8sAuth, "auth", "token", "cluster auth: token|client-cert|exec")
	add.Flags().StringVar(&token, "token", "", "bearer/ServiceAccount token (token auth)")
	add.Flags().StringVar(&clientCertFile, "client-cert", "", "client cert PEM (client-cert auth)")
	add.Flags().StringVar(&clientKeyFile, "client-key", "", "client key PEM (client-cert auth)")
	add.Flags().StringVar(&execCommand, "exec-command", "", "credential plugin binary (exec auth)")
	add.Flags().StringArrayVar(&execArgs, "exec-arg", nil, "credential plugin arg (repeatable)")
	_ = add.MarkFlagRequired("api-url")

	list := &cobra.Command{
		Use:   "list",
		Short: "List registered clusters",
		RunE: func(c *cobra.Command, _ []string) error {
			var out []map[string]string
			if err := newClient(gf).Do("GET", "/upstreams", nil, &out); err != nil {
				return err
			}
			for _, u := range out {
				if u["kind"] != upstream.KindK8s {
					continue
				}
				fmt.Fprintf(c.OutOrStdout(), "%s\t%s\t%s\n", u["id"], u["name"], u["base_url"])
			}
			return nil
		},
	}

	rm := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a registered cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := newClient(gf).Do("DELETE", "/upstreams/"+args[0], nil, nil); err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), "removed")
			return nil
		},
	}

	cmd.AddCommand(add, list, rm)
	return cmd
}
