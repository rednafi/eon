package cli

import (
	"github.com/spf13/cobra"

	"github.com/rednafi/eon/cron"
)

func newEditCmd(mgr *cron.Manager) *cobra.Command {
	var (
		schedule string
		command  string
	)
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit an existing cron job's schedule and command",
		Long: `Replace the schedule and command of an existing job. <id> is the full
ID or any unique case-insensitive substring (same matching rules as
` + "`eon show`" + ` and ` + "`eon delete`" + `).

Only writable backends accept edits — system jobs in /etc/crontab,
/etc/cron.d, and the system Launch* directories cannot be edited.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			j, err := mgr.Find(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if j.Scope == cron.ScopeSystem {
				return errSystemReadOnly
			}
			// Allow partial edit: keep the existing schedule/command if the
			// caller didn't pass that flag. Easier than asking users to
			// re-quote the whole spec when they only want to change one half.
			spec := cron.JobSpec{Schedule: j.Schedule, Command: j.Command}
			if schedule != "" {
				spec.Schedule = schedule
			}
			if command != "" {
				spec.Command = command
			}
			updated, err := mgr.Edit(cmd.Context(), j.ID, spec)
			if err != nil {
				return err
			}
			cmd.Printf("edited %s\n", updated.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&schedule, "schedule", "", "new cron schedule (leave unset to keep)")
	cmd.Flags().StringVar(&command, "command", "", "new shell command (leave unset to keep)")
	return cmd
}
