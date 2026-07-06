package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Sipaha/outwall/internal/upstream"
)

func newUpstreamCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "upstream", Short: "Manage upstreams"}

	var (
		baseURL, authType, header, token, username, password string
	)
	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Add an upstream",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			req := map[string]any{
				"name":     args[0],
				"base_url": baseURL,
				"auth": upstream.AuthConfig{
					Type: authType, Header: header, Token: token,
					Username: username, Password: password,
				},
			}
			var out map[string]string
			if err := doPrivileged(gf, "POST", "/upstreams", req, &out); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "created upstream %s (id=%s)\n", args[0], out["id"])
			return nil
		},
	}
	add.Flags().StringVar(&baseURL, "base-url", "", "upstream base URL (required)")
	add.Flags().StringVar(&authType, "auth", "none", "auth type: none|static|basic")
	add.Flags().StringVar(&header, "header", "", "header name for static auth")
	add.Flags().StringVar(&token, "token", "", "header value for static auth")
	add.Flags().StringVar(&username, "username", "", "basic auth username")
	add.Flags().StringVar(&password, "password", "", "basic auth password")
	_ = add.MarkFlagRequired("base-url")

	list := &cobra.Command{
		Use:   "list",
		Short: "List upstreams",
		RunE: func(c *cobra.Command, _ []string) error {
			var out []map[string]string
			if err := newClient(gf).Do("GET", "/upstreams", nil, &out); err != nil {
				return err
			}
			for _, u := range out {
				fmt.Fprintf(c.OutOrStdout(), "%s\t%s\t%s\t%s\n", u["id"], u["name"], u["auth_type"], u["base_url"])
			}
			return nil
		},
	}
	cmd.AddCommand(add, list)
	return cmd
}
