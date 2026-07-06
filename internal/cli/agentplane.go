package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sipaha/outwall/internal/agentid"
	"github.com/Sipaha/outwall/internal/client"
)

// agentClient returns a client bound to the agent unix socket.
func agentClient(gf *globalFlags) *client.Client { return client.New(gf.agentSocket) }

// agentToken resolves (registering once on first use) the per-project agent token, minting it via
// the agent socket's /register endpoint. internal/agentid persists it per project.
func agentToken(gf *globalFlags) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	return agentid.LoadOrRegister(cwd, func(name string) (string, string, error) {
		var out struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		}
		if err := agentClient(gf).Do("POST", "/register", map[string]string{"name": name}, &out); err != nil {
			return "", "", err
		}
		return out.ID, out.Token, nil
	})
}

// printJSON writes v as indented JSON to the command's stdout (agent-friendly output).
func printJSON(c *cobra.Command, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(c.OutOrStdout(), string(b))
	return nil
}

func newListUpstreamsCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list-upstreams",
		Short: "List upstreams outwall knows about and your access status for each",
		RunE: func(c *cobra.Command, _ []string) error {
			token, err := agentToken(gf)
			if err != nil {
				return err
			}
			var out any
			if err := agentClient(gf).DoAuth(token, "GET", "/upstreams", nil, &out); err != nil {
				return err
			}
			return printJSON(c, out)
		},
	}
}

func newWhoamiCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Print your agent identity, data-plane bearer token, and current accesses",
		RunE: func(c *cobra.Command, _ []string) error {
			token, err := agentToken(gf)
			if err != nil {
				return err
			}
			var out any
			if err := agentClient(gf).DoAuth(token, "GET", "/whoami", nil, &out); err != nil {
				return err
			}
			return printJSON(c, out)
		},
	}
}

func newRequestHostAccessCmd(gf *globalFlags) *cobra.Command {
	var purpose string
	cmd := &cobra.Command{
		Use:   "request-host-access <host>",
		Short: "Request access to a host, stating your purpose",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			token, err := agentToken(gf)
			if err != nil {
				return err
			}
			var out any
			body := map[string]string{"host": args[0], "purpose": purpose}
			if err := agentClient(gf).DoAuth(token, "POST", "/access/host", body, &out); err != nil {
				return err
			}
			return printJSON(c, out)
		},
	}
	cmd.Flags().StringVar(&purpose, "purpose", "", "why you need this host (required)")
	return cmd
}

func newRequestAccessCmd(gf *globalFlags) *cobra.Command {
	var (
		method, path, purpose string
		query, values         map[string]string
	)
	cmd := &cobra.Command{
		Use:   "request-access <host>",
		Short: "Request access to an operation on an already-approved host",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			token, err := agentToken(gf)
			if err != nil {
				return err
			}
			body := map[string]any{
				"host": args[0], "method": method, "path_template": path,
				"query_template": query, "values": values, "purpose": purpose,
			}
			var out any
			if err := agentClient(gf).DoAuth(token, "POST", "/access/op", body, &out); err != nil {
				return err
			}
			return printJSON(c, out)
		},
	}
	cmd.Flags().StringVar(&method, "method", "GET", "HTTP method")
	cmd.Flags().StringVar(&path, "path", "", "path template with {name:type} placeholders")
	cmd.Flags().StringToStringVar(&query, "query", nil, "query template entries key=value (repeatable)")
	cmd.Flags().StringToStringVar(&values, "value", nil, "concrete values key=value (repeatable)")
	cmd.Flags().StringVar(&purpose, "purpose", "", "why you need this operation (required)")
	return cmd
}

func newRequestPresetCmd(gf *globalFlags) *cobra.Command {
	var (
		preset, purpose string
		vars            map[string]string
	)
	cmd := &cobra.Command{
		Use:   "request-preset <upstream>",
		Short: "Request a named preset (a bundle of rights) on an upstream",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			token, err := agentToken(gf)
			if err != nil {
				return err
			}
			body := map[string]any{"upstream": args[0], "preset": preset, "vars": vars, "purpose": purpose}
			var out any
			if err := agentClient(gf).DoAuth(token, "POST", "/access/preset", body, &out); err != nil {
				return err
			}
			return printJSON(c, out)
		},
	}
	cmd.Flags().StringVar(&preset, "preset", "", "preset id from list-upstreams (required)")
	cmd.Flags().StringToStringVar(&vars, "var", nil, "slot values key=value (repeatable)")
	cmd.Flags().StringVar(&purpose, "purpose", "", "why you need this preset (required)")
	return cmd
}

func newRequestK8sAccessCmd(gf *globalFlags) *cobra.Command {
	var (
		namespace, purpose string
		grantSpecs         []string
	)
	cmd := &cobra.Command{
		Use:   "request-k8s-access <cluster>",
		Short: "Request k8s access on a registered cluster for one namespace",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			token, err := agentToken(gf)
			if err != nil {
				return err
			}
			grants := make([]map[string]any, 0, len(grantSpecs))
			for _, g := range grantSpecs {
				parts := strings.SplitN(g, "=", 2)
				if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
					return fmt.Errorf("invalid --grant %q (want resource=verb1,verb2)", g)
				}
				grants = append(grants, map[string]any{
					"resource": parts[0], "verbs": strings.Split(parts[1], ","),
				})
			}
			body := map[string]any{"cluster": args[0], "namespace": namespace, "grants": grants, "purpose": purpose}
			var out any
			if err := agentClient(gf).DoAuth(token, "POST", "/access/k8s", body, &out); err != nil {
				return err
			}
			return printJSON(c, out)
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "k8s namespace")
	cmd.Flags().StringArrayVar(&grantSpecs, "grant", nil, "resource=verb1,verb2 (repeatable)")
	cmd.Flags().StringVar(&purpose, "purpose", "", "why you need this access (required)")
	return cmd
}

func newGetAccessCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "get-access <upstream>",
		Short: "Report your current access status for an upstream (waits for a pending decision)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			token, err := agentToken(gf)
			if err != nil {
				return err
			}
			var out any
			if err := agentClient(gf).DoAuth(token, "GET", "/access/"+url.PathEscape(args[0]), nil, &out); err != nil {
				return err
			}
			return printJSON(c, out)
		},
	}
}

func newGetKubeconfigCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "get-kubeconfig <cluster>",
		Short: "Print a kubeconfig for a registered k8s cluster using your own outwall token",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			token, err := agentToken(gf)
			if err != nil {
				return err
			}
			var out struct {
				Kubeconfig string `json:"kubeconfig"`
			}
			if err := agentClient(gf).DoAuth(token, "GET", "/kubeconfig/"+url.PathEscape(args[0]), nil, &out); err != nil {
				return err
			}
			fmt.Fprint(c.OutOrStdout(), out.Kubeconfig)
			return nil
		},
	}
}
