package cli

import (
	"github.com/spf13/cobra"

	"github.com/rednafi/eon/cron"
)

func newAddCmd(mgr *cron.Manager) *cobra.Command {
	var (
		schedule string
		command  string
		source   string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new cron job",
		Long: `Add a new cron job to a writable backend.

Without --source, eon picks the first writable backend (typically the
user crontab). To target a specific backend, pass --source <name> with
one of the names shown by ` + "`eon list --json`" + `.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			j, err := mgr.Add(cmd.Context(), source, cron.JobSpec{
				Schedule: schedule,
				Command:  command,
			})
			if err != nil {
				return err
			}
			cmd.Printf("added %s\n", j.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&schedule, "schedule", "", "cron schedule (e.g. '*/5 * * * *' or '@daily')")
	cmd.Flags().StringVar(&command, "command", "", "shell command to run")
	cmd.Flags().StringVar(&source, "source", "", "backend Source name (default: first writable)")
	_ = cmd.MarkFlagRequired("schedule")
	_ = cmd.MarkFlagRequired("command")
	return cmd
}
