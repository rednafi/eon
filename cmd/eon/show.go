package main

import "github.com/spf13/cobra"

func newShowCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:     "show ID|NAME",
		Aliases: []string{"get"},
		Short:   "Show one job's details.",
		Long: `Show full detail for the job identified by its 5-char ID or its
exact name. With --json, emit the job as a single JSON object suitable
for programmatic use.`,
		Example: `  eon show 7K3px
  eon show backup --json | jq .cron`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, cleanup, err := openService()
			if err != nil {
				return err
			}
			defer cleanup()

			warnIfDaemonDown(cmd.Context(), s, stderr)

			job, err := resolveJob(cmd.Context(), s, args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), job)
			}
			writeJobDetail(cmd.OutOrStdout(), job)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the job as a JSON object")
	return cmd
}
