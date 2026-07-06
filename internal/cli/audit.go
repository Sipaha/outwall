package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"
)

func newAuditCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "audit", Short: "Inspect the request/response audit journal"}

	var limit int
	list := &cobra.Command{
		Use:   "list",
		Short: "List audit entries (newest first)",
		RunE: func(c *cobra.Command, _ []string) error {
			var out []map[string]any
			path := "/audit"
			if limit > 0 {
				path += "?limit=" + strconv.Itoa(limit)
			}
			if err := newClient(gf).Do("GET", path, nil, &out); err != nil {
				return err
			}
			for _, e := range out {
				name := str(e["agent_name"])
				if name == "" {
					name = str(e["agent_id"])
				}
				up := str(e["upstream_name"])
				if up == "" {
					up = str(e["upstream_id"])
				}
				fmt.Fprintf(c.OutOrStdout(), "%s\t%s\t%s\t%s\t%s\t%v\t%s\n",
					str(e["id"]), str(e["ts"]), name, up, str(e["method"]),
					e["status_code"], str(e["path"]))
			}
			return nil
		},
	}
	list.Flags().IntVar(&limit, "limit", 50, "maximum number of entries to list")

	show := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a single audit entry with masked headers and captured bodies",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			var out map[string]any
			if err := newClient(gf).Do("GET", "/audit/"+args[0], nil, &out); err != nil {
				return err
			}
			b, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), string(b))
			return nil
		},
	}

	var olderThan string
	prune := &cobra.Command{
		Use:   "prune",
		Short: "Delete audit entries older than a duration (e.g. 720h) or an RFC3339 date",
		RunE: func(c *cobra.Command, _ []string) error {
			if olderThan == "" {
				return fmt.Errorf("--older-than is required (a Go duration like 720h or an RFC3339 date)")
			}
			cutoff, err := parseCutoff(olderThan)
			if err != nil {
				return err
			}
			var resp map[string]int64
			if err := doPrivileged(gf, "POST", "/audit/prune",
				map[string]string{"older_than_rfc3339": cutoff.UTC().Format(time.RFC3339)}, &resp); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "deleted %d\n", resp["deleted"])
			return nil
		},
	}
	prune.Flags().StringVar(&olderThan, "older-than", "", "Go duration (e.g. 720h → now−dur) or an RFC3339 date")

	cmd.AddCommand(list, show, prune)
	return cmd
}

// parseCutoff accepts a Go duration (relative to now) or an RFC3339 timestamp.
func parseCutoff(s string) (time.Time, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid --older-than %q: want a Go duration (e.g. 720h) or an RFC3339 date", s)
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
