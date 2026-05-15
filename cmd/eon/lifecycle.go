package main

import (
	"context"
	"fmt"

	"github.com/rednafi/eon"
	"github.com/spf13/cobra"
)

func newRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rm ID|NAME",
		Aliases: []string{"delete"},
		Short:   "Delete a job and its run history.",
		Long:    "Delete the job identified by ID or by exact name. Run history rows cascade.",
		Example: "  eon rm 7K3px\n  eon rm backup\n",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, cleanup, err := openService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()
			job, err := resolveJob(cmd.Context(), s, args[0])
			if err != nil {
				return err
			}
			warnIfDaemonDown(cmd.Context(), s, stderr)
			if err := s.Delete(cmd.Context(), job.ID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted job %s\n", job.ID)
			return nil
		},
	}
}

func newEnableCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "enable ID|NAME",
		Short:   "Enable a previously disabled job.",
		Long:    "Mark the job as enabled so the scheduler resumes firing it on its schedule. No-op if already enabled.",
		Example: "  eon enable backup\n  eon enable 7K3px",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, cleanup, err := openService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()
			job, err := resolveJob(cmd.Context(), s, args[0])
			if err != nil {
				return err
			}
			warnIfDaemonDown(cmd.Context(), s, stderr)
			if err := s.Enable(cmd.Context(), job.ID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "enabled job %s\n", job.ID)
			return nil
		},
	}
}

func newDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "disable ID|NAME",
		Short:   "Stop a job from firing without deleting it.",
		Long:    "Mark a job disabled so the scheduler stops firing it. The job stays in the store and can be re-enabled later with 'eon enable'. No-op if already disabled.",
		Example: "  eon disable backup\n  eon disable 7K3px",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, cleanup, err := openService(cmd.Context())
			if err != nil {
				return err
			}
			defer cleanup()
			job, err := resolveJob(cmd.Context(), s, args[0])
			if err != nil {
				return err
			}
			warnIfDaemonDown(cmd.Context(), s, stderr)
			if err := s.Disable(cmd.Context(), job.ID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "disabled job %s\n", job.ID)
			return nil
		},
	}
}

// resolveJob is the canonical CLI lookup: empty arg is a usage error,
// anything else delegates to service.Resolve which understands
// both 5-char IDs and exact-match names.
func resolveJob(ctx context.Context, s *service, arg string) (eon.Job, error) {
	if arg == "" {
		return eon.Job{}, fmt.Errorf("%w: empty job reference", errUsage)
	}
	return s.Resolve(ctx, arg)
}

// parseKindFlag validates --kind values. Empty input is allowed and
// returns the zero kind so callers can distinguish "no filter" from
// "bad filter".
func parseKindFlag(s string) (eon.JobKind, error) {
	if s == "" {
		return "", nil
	}
	k := eon.JobKind(s)
	if k != eon.KindCron && k != eon.KindOneshot {
		return "", usageErrf("--kind must be 'cron' or 'oneshot'")
	}
	return k, nil
}

// parseStatusFlag validates --status values. Empty input is allowed
// and returns the zero status so callers can distinguish "no filter"
// from "bad filter".
func parseStatusFlag(s string) (eon.JobStatus, error) {
	if s == "" {
		return "", nil
	}
	st := eon.JobStatus(s)
	switch st {
	case eon.StatusEnabled, eon.StatusDisabled, eon.StatusDone:
		return st, nil
	}
	return "", usageErrf("--status must be 'enabled', 'disabled', or 'done'")
}
