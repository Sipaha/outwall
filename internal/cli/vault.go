package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func promptPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	b, err := term.ReadPassword(0)
	fmt.Println()
	return string(b), err
}

func newVaultCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "vault", Short: "Manage the master-password vault"}

	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Initialize the vault with a master password",
		RunE: func(c *cobra.Command, _ []string) error {
			pw, err := promptPassword("New master password: ")
			if err != nil {
				return err
			}
			return newClient(gf).Do("POST", "/vault/init", map[string]string{"password": pw}, nil)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "unlock",
		Short: "Unlock the vault",
		RunE: func(c *cobra.Command, _ []string) error {
			pw, err := promptPassword("Master password: ")
			if err != nil {
				return err
			}
			return newClient(gf).Do("POST", "/vault/unlock", map[string]string{"password": pw}, nil)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show vault status",
		RunE: func(c *cobra.Command, _ []string) error {
			var out map[string]bool
			if err := newClient(gf).Do("GET", "/vault/status", nil, &out); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "initialized=%v locked=%v\n", out["initialized"], out["locked"])
			return nil
		},
	})
	return cmd
}
