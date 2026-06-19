package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func promptPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	b, err := term.ReadPassword(0)
	fmt.Println()
	return string(b), err
}

// readPassword returns the master password either from stdin (when fromStdin is set — for
// automation / no-TTY contexts) or via the interactive TTY prompt. The stdin form reads all of in
// and trims exactly one trailing newline (and a preceding CR), so `echo pw | outwall vault unlock
// --password-stdin` works and the trailing newline is not part of the password.
func readPassword(in io.Reader, fromStdin bool, prompt string) (string, error) {
	if !fromStdin {
		return promptPassword(prompt)
	}
	b, err := io.ReadAll(in)
	if err != nil {
		return "", fmt.Errorf("read password from stdin: %w", err)
	}
	s := strings.TrimSuffix(string(b), "\n")
	s = strings.TrimSuffix(s, "\r")
	return s, nil
}

func newVaultCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "vault", Short: "Manage the master-password vault"}

	var initStdin bool
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize the vault with a master password",
		RunE: func(c *cobra.Command, _ []string) error {
			pw, err := readPassword(c.InOrStdin(), initStdin, "New master password: ")
			if err != nil {
				return err
			}
			return newClient(gf).Do("POST", "/vault/init", map[string]string{"password": pw}, nil)
		},
	}
	initCmd.Flags().BoolVar(&initStdin, "password-stdin", false, "read the master password from stdin (no TTY prompt)")
	cmd.AddCommand(initCmd)

	var unlockStdin bool
	unlockCmd := &cobra.Command{
		Use:   "unlock",
		Short: "Unlock the vault",
		RunE: func(c *cobra.Command, _ []string) error {
			pw, err := readPassword(c.InOrStdin(), unlockStdin, "Master password: ")
			if err != nil {
				return err
			}
			return newClient(gf).Do("POST", "/vault/unlock", map[string]string{"password": pw}, nil)
		},
	}
	unlockCmd.Flags().BoolVar(&unlockStdin, "password-stdin", false, "read the master password from stdin (no TTY prompt)")
	cmd.AddCommand(unlockCmd)

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
