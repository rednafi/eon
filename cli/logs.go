package cli

import (
	"github.com/spf13/cobra"

	"github.com/rednafi/eon/cron"
)

func newLogsCmd(mgr *cron.Manager) *cobra.Command { // mgr param keeps signature uniform with other cmds

	var n int
	cmd := &cobra.Command{
		Use:   "logs <id>",
		Short: "Print the last N lines of a cron's stdout/stderr",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			j, err := mgr.Find(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			wrote := false
			for _, p := range []struct{ label, path string }{
				{"stdout", j.StdoutPath},
				{"stderr", j.StderrPath},
			} {
				if p.path == "" {
					continue
				}
				cmd.Printf("── %s (%s) ──\n", p.label, p.path)
				if err := tail(cmd.OutOrStdout(), p.path, n); err != nil {
					cmd.PrintErrf("logs: %s: %v\n", p.label, err)
				}
				wrote = true
			}
			if !wrote {
				cmd.Printf("no log paths configured for %s\n", j.ID)
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&n, "lines", "n", 100, "max lines per stream")
	return cmd
}
