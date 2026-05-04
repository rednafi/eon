package cli

import (
	"github.com/spf13/cobra"

	"github.com/rednafi/eon/cron"
)

func newShowCmd(mgr *cron.Manager) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show details for a single cron",
		Long: `<id> is either the full cron ID (e.g. "launchd-user:com.foo.bar")
or any unique case-insensitive substring of the ID, name, or command.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			j, err := mgr.Find(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), j)
			}
			renderJobDetail(cmd.OutOrStdout(), j)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of formatted text")
	return cmd
}
