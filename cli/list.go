package cli

import (
	"github.com/spf13/cobra"

	"github.com/rednafi/eon/cron"
)

func newListCmd(mgr *cron.Manager) *cobra.Command {
	var (
		all    bool
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List known crons (user-scope by default)",
		Long: `By default eon lists only user-scope jobs (your crontab plus your
launchd/systemd user units). Pass --all to also surface read-only system
jobs from /Library/Launch*, /etc/crontab, /etc/cron.d, and
/etc/systemd/system.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			jobs, errs := mgr.List(cmd.Context())
			for _, e := range errs {
				cmd.PrintErrln("warning:", e)
			}
			if !all {
				jobs = filterUser(jobs)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), jobs)
			}
			renderTable(cmd.OutOrStdout(), jobs)
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include read-only system jobs")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of a table")
	return cmd
}

func filterUser(jobs []cron.Job) []cron.Job {
	out := make([]cron.Job, 0, len(jobs))
	for _, j := range jobs {
		if j.Scope != cron.ScopeSystem {
			out = append(out, j)
		}
	}
	return out
}
