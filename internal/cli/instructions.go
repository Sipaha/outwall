package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sipaha/outwall/internal/agentapi"
	"github.com/Sipaha/outwall/internal/mcpsvc"
)

// instructionsOut mirrors agentapi's GET /instructions payload.
type instructionsOut struct {
	Info     agentapi.EnvInfo `json:"info"`
	Identity mcpsvc.Identity  `json:"identity"`
}

// newInstructionsCmd prints the always-current agent playbook for THIS outwall daemon: the resolved
// data-plane URL/port, the browser origin + cookie, the CA path and proxy gotcha, and the caller's
// own per-project identity. Generating it from the running binary (instead of a hand-written doc)
// keeps the mechanical facts that drift the most from ever going stale.
func newInstructionsCmd(gf *globalFlags) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "instructions",
		Short: "Print the current agent usage playbook for this outwall (self-describing; kills doc drift)",
		RunE: func(c *cobra.Command, _ []string) error {
			var out instructionsOut
			if err := doAgent(gf, "GET", "/instructions", nil, &out); err != nil {
				return err
			}
			if asJSON {
				return printJSON(c, out)
			}
			// The token is the caller's own; fetch it locally so the rendered examples are runnable.
			// doAgent already re-registered if the persisted token was stale, so this is now valid.
			token, err := agentToken(gf)
			if err != nil {
				return err
			}
			fmt.Fprint(c.OutOrStdout(), renderInstructions(out, token, gf.agentSocket))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the raw JSON facts instead of the rendered playbook")
	return cmd
}

// renderInstructions turns the live facts + identity into a Markdown agent playbook.
func renderInstructions(o instructionsOut, token, agentSocket string) string {
	in := o.Info
	id := o.Identity
	var b strings.Builder

	fmt.Fprintf(&b, "# outwall — agent usage (live, from this daemon)\n\n")

	accesses := "none yet — request access below"
	if len(id.Accesses) > 0 {
		accesses = strings.Join(id.Accesses, ", ")
	}
	fmt.Fprintf(&b, "You are agent **%s** (`%s`), status **%s**. Current grants: %s.\n\n",
		id.Name, id.AgentID, id.Status, accesses)
	fmt.Fprintf(&b, "This identity is per-project (keyed by your cwd's git top-level). Running `outwall` from a\n"+
		"different directory mints a DIFFERENT agent with different grants — always run it from your project.\n\n")

	fmt.Fprintf(&b, "## Control plane — the `outwall` CLI (over %s)\n", agentSocket)
	fmt.Fprintf(&b, "- `outwall list-upstreams` — upstreams, your status, and presets (slot schemas + hints).\n")
	fmt.Fprintf(&b, "- `outwall request-preset <upstream> --preset <id> --var slot=value --purpose \"…\"` — easiest grant.\n")
	fmt.Fprintf(&b, "- `outwall request-access <host> --method GET --path \"/api/…/{id:text}\" --var id:text --value id=42 --purpose \"…\"`\n")
	fmt.Fprintf(&b, "- `outwall request-host-access <host> --purpose \"…\"` — coarse host access.\n")
	fmt.Fprintf(&b, "- `outwall get-access <upstream>` — your status (waits ~25s for a pending operator decision).\n")
	fmt.Fprintf(&b, "- `outwall whoami` — your identity + data-plane bearer token.\n")
	fmt.Fprintf(&b, "You request; the operator approves in the UI. Grants are scoped to you.\n\n")

	fmt.Fprintf(&b, "## Data plane — reverse proxy (TLS terminated at outwall)\n")
	fmt.Fprintf(&b, "- Base URL: `%s` → per-upstream: `%s/<upstream>/<path…>`\n", in.DataPlaneURL, in.DataPlaneURL)
	fmt.Fprintf(&b, "- Auth: header `Authorization: Bearer <your token>` (from `outwall whoami`).\n")
	fmt.Fprintf(&b, "- TLS: signed by the local CA at `%s` (NOT in the system trust store) — pass\n"+
		"  `--cacert %s` (curl) or an `ignoreHTTPSErrors` context (Playwright).\n", in.CACertPath, in.CACertPath)
	fmt.Fprintf(&b, "- PROXY GOTCHA: the data plane is on loopback; a global `HTTPS_PROXY`/`HTTP_PROXY` will hijack it.\n"+
		"  Bypass with `--noproxy 127.0.0.1,localhost` (curl) or `NO_PROXY=127.0.0.1,localhost`.\n\n")

	if in.BrowseDomain != "" {
		origin := "https://<upstream>." + in.BrowseDomain
		if in.BrowsePort != "" {
			origin += ":" + in.BrowsePort
		}
		fmt.Fprintf(&b, "### Browser / Playwright (per-upstream origin)\n")
		fmt.Fprintf(&b, "- Origin: `%s/` (Host-routed; `*.%s` resolves to loopback).\n", origin, in.BrowseDomain)
		fmt.Fprintf(&b, "- Carry the token as the `%s` cookie on that origin; use an `ignoreHTTPSErrors` context.\n\n", in.CookieName)
	}

	fmt.Fprintf(&b, "### Example (API, with your token)\n")
	fmt.Fprintf(&b, "```\ncurl --noproxy 127.0.0.1,localhost --cacert %s \\\n  -H \"Authorization: Bearer %s\" \\\n  %s/<upstream>/<path>\n```\n",
		in.CACertPath, token, in.DataPlaneURL)
	fmt.Fprintf(&b, "A missing grant → 403 `{\"error\":\"access denied: …\"}` (the message says how to request access). "+
		"A 401 means no/invalid token.\n")

	return b.String()
}
