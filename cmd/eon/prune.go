package main

import (
	"fmt"

	"github.com/rednafi/eon"
	"github.com/rednafi/eon/store"
	"github.com/spf13/cobra"
)

func newPruneCmd() *cobra.Command {
	var (
		kind    string
		status  string
		force   bool
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete jobs that won't fire (done one-shots and disabled jobs).",
		Long: `Remove jobs from the store that the scheduler will never fire again:
done one-shots and disabled jobs. Defaults to a dry run that prints
the candidates without touching the database. Pass --force (-f) to
actually delete.

Filters mirror 'eon ls'. With --status given, only that status is
pruned (overrides the default disabled+done set). With --kind, the
result is further narrowed to that kind. With --json, the candidate
set (dry-run) or the deleted IDs (--force) are emitted as JSON.`,
		Example: `  eon prune                       # dry-run; show candidates
  eon prune --force               # actually delete the candidates
  eon prune --status done -f      # only done one-shots
  eon prune --kind cron -f        # only cron jobs (the disabled ones)
  eon prune --json | jq '.[].id'  # scripted candidate list`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, cleanup, err := openService()
			if err != nil {
				return err
			}
			defer cleanup()

			k, err := parseKindFlag(kind)
			if err != nil {
				return err
			}
			st, err := parseStatusFlag(status)
			if err != nil {
				return err
			}
			opts := store.ListOpts{Limit: -1, Kind: k, Status: st}

			all, _, err := s.List(cmd.Context(), opts)
			if err != nil {
				return err
			}

			targets := []eon.Job{}
			if status == "" {
				// Default scope: anything the scheduler won't fire.
				for _, j := range all {
					if j.Status == eon.StatusDisabled || j.Status == eon.StatusDone {
						targets = append(targets, j)
					}
				}
			} else {
				targets = all
			}

			out := cmd.OutOrStdout()
			if !force {
				if jsonOut {
					return writeJSON(out, targets)
				}
				if len(targets) == 0 {
					fmt.Fprintln(out, "nothing to prune")
					return nil
				}
				fmt.Fprintf(out, "would prune %d job(s):\n", len(targets))
				for _, j := range targets {
					fmt.Fprintf(out, "  %s  %-8s  %-8s  %s\n", j.ID, j.Kind, j.Status, j.Name)
				}
				fmt.Fprintln(out, "(dry run; nothing was modified)")
				fmt.Fprintln(out, "Re-run with --force (-f) to actually delete.")
				return nil
			}

			deleted := make([]eon.Job, 0, len(targets))
			for _, j := range targets {
				if err := s.Delete(cmd.Context(), j.ID); err != nil {
					return fmt.Errorf("delete %s: %w", j.ID, err)
				}
				deleted = append(deleted, j)
			}
			if jsonOut {
				return writeJSON(out, deleted)
			}
			if len(deleted) == 0 {
				fmt.Fprintln(out, "nothing to prune")
				return nil
			}
			fmt.Fprintf(out, "pruned %d job(s)\n", len(deleted))
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "filter by kind: cron|oneshot")
	cmd.Flags().StringVar(&status, "status", "", "filter by status: enabled|disabled|done (default: disabled+done)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "actually delete (default is dry-run)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the affected jobs as a JSON array (candidates on dry-run, deleted jobs with --force)")
	return cmd
}
