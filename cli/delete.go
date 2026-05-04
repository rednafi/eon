package cli

import (
	"bufio"
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rednafi/eon/cron"
)

// errAborted signals user-cancelled delete; cobra prints it as the error and
// exits non-zero, which is the right exit semantic for scripts that wrap
// `eon delete` interactively.
var errAborted = errors.New("aborted")

func newDeleteCmd(mgr *cron.Manager) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Stop and remove a cron",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			j, err := mgr.Find(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if j.Scope == cron.ScopeSystem {
				return errors.New("refusing to delete system-scope job (read-only)")
			}
			if !yes {
				cmd.Printf("Delete %s (%s)? [y/N] ", j.ID, j.Schedule)
				if !confirm(cmd.InOrStdin()) {
					cmd.Println("aborted")
					return errAborted
				}
			}
			if err := mgr.Delete(cmd.Context(), j.ID); err != nil {
				return err
			}
			cmd.Printf("deleted %s\n", j.ID)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func confirm(r interface{ Read(p []byte) (int, error) }) bool {
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		return false
	}
	resp := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return resp == "y" || resp == "yes"
}
