// Package cli defines the outwall command tree.
package cli

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Sipaha/outwall/internal/client"
	"github.com/Sipaha/outwall/internal/config"
	"github.com/Sipaha/outwall/internal/daemon"
	"github.com/Sipaha/outwall/internal/version"
)

type globalFlags struct {
	socket         string
	agentSocket    string
	db             string
	listen         string
	uiListen       string
	callbackListen string
	browseDomain   string
}

func defaultDir() string {
	return config.DataDir()
}

// NewRootCmd builds the root cobra command.
func NewRootCmd() *cobra.Command {
	gf := &globalFlags{}
	root := &cobra.Command{
		Use:           "outwall",
		Short:         "Authenticating egress gateway for AI agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.String(),
	}
	dir := defaultDir()
	root.PersistentFlags().StringVar(&gf.socket, "socket", filepath.Join(dir, "outwall.sock"), "admin unix socket path")
	root.PersistentFlags().StringVar(&gf.db, "db", filepath.Join(dir, "outwall.db"), "database path")
	root.PersistentFlags().StringVar(&gf.listen, "listen", "127.0.0.1:8080", "data-plane listen address")
	root.PersistentFlags().StringVar(&gf.agentSocket, "agent-socket", filepath.Join(dir, "agent.sock"), "agent-plane unix socket path")
	root.PersistentFlags().StringVar(&gf.uiListen, "ui-listen", "127.0.0.1:8182", "desktop-UI control API + SSE listen address")
	root.PersistentFlags().StringVar(&gf.callbackListen, "callback-listen", daemon.DefaultCallbackListen, "OIDC browser-login callback listen address (its /callback is the IdP redirect URI)")
	root.PersistentFlags().StringVar(&gf.browseDomain, "browse-domain", daemon.DefaultBrowseDomain, "base domain for per-upstream browser origins")

	root.AddCommand(
		newServeCmd(gf),
		newVaultCmd(gf),
		newUpstreamCmd(gf),
		newClusterCmd(gf),
		newKubeconfigCmd(gf),
		newAgentCmd(gf),
		newRuleCmd(gf),
		newApprovalCmd(gf),
		newAccessCmd(gf),
		newAuditCmd(gf),
		newListUpstreamsCmd(gf),
		newWhoamiCmd(gf),
		newRequestHostAccessCmd(gf),
		newRequestAccessCmd(gf),
		newRequestPresetCmd(gf),
		newRequestK8sAccessCmd(gf),
		newGetAccessCmd(gf),
		newGetKubeconfigCmd(gf),
	)
	return root
}

func newClient(gf *globalFlags) *client.Client { return client.New(gf.socket) }
