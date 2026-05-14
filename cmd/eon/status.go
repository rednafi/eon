package main

import "github.com/spf13/cobra"

func newStatusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report daemon state and job counts.",
		Long: `Report whether the daemon is running, whether a launchd/systemd
supervisor is installed, the data directory and database path, plus
aggregate counts of jobs by kind and one-shot completion state.`,
		Example: "  eon status\n  eon status --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, cleanup, err := openService()
			if err != nil {
				return err
			}
			defer cleanup()
			status, err := s.Status(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), status)
			}
			writeStatus(cmd.OutOrStdout(), status)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit status as JSON")
	return cmd
}
