package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/rednafi/eon"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	var (
		cronExpr string
		atExpr   string
		name     string
		jsonOut  bool
	)
	cmd := &cobra.Command{
		Use:     "add [--cron EXPR | --at TIME] [--name NAME] -- COMMAND [ARG...]",
		Aliases: []string{"create", "new"},
		Short:   "Create a new cron or one-shot job.",
		Long: `Create a new job. Exactly one of --cron or --at is required.

--name NAME    a short human label. Optional; defaults to the command.

The command MUST follow '--'. Two forms work and you can use whichever
fits your shell habits:

  -- "echo hello"        # single quoted string → run via /bin/sh -c
  -- sh -c "echo hello"  # explicit shell invocation → exec directly
  -- /bin/echo hello     # multi-word argv      → exec directly

In other words: one positional after '--' is treated as a shell line
(piped to sh -c at fire time), two or more positionals are treated as
the program plus its arguments (exec'd directly). Either form lands
you at the same outcome for simple commands; the shell form is what
you reach for when you want pipes, redirects, or paths with spaces.

CRON SYNTAX (--cron)

A 5-field crontab expression or one of the @descriptor shortcuts.

  5-field layout:  MINUTE HOUR DAY-OF-MONTH MONTH DAY-OF-WEEK

    MINUTE         0-59
    HOUR           0-23
    DAY-OF-MONTH   1-31
    MONTH          1-12 or jan,feb,mar,apr,may,jun,jul,aug,sep,oct,nov,dec
    DAY-OF-WEEK    0-6 (0 = Sunday) or sun,mon,tue,wed,thu,fri,sat

  Each field accepts:
    *              every value
    N              the value N
    N-M            range N through M (inclusive)
    A,B,C          a list of values
    */N            every N units (step), starting from the minimum
    A-B/N          step over a range

  Examples:
    "0 9 * * 1-5"        09:00 on weekdays
    "*/15 * * * *"       every 15 minutes
    "0 0 1 jan,jul *"    midnight on Jan 1 and Jul 1
    "30 2 * * sun"       02:30 every Sunday

  Descriptor shortcuts:
    @yearly  / @annually    midnight on Jan 1   (0 0 1 1 *)
    @monthly                midnight on the 1st (0 0 1 * *)
    @weekly                 midnight on Sunday  (0 0 * * 0)
    @daily   / @midnight    midnight            (0 0 * * *)
    @hourly                 top of each hour    (0 * * * *)

  Interval shortcut:
    @every DURATION         fire every DURATION (Go-style: 30s, 5m, 2h, 24h)

  NOT supported:
    @reboot      eon's daemon does not see reboot events directly.
                 Use 'eon install' to register a launchd/systemd unit
                 that starts the daemon at boot, then schedule normally.
    seconds      the parser is minute-resolution; use @every 30s for
                 sub-minute intervals.

ONE-SHOT TIME (--at)

A wall-clock time in the future. All forms below are accepted.

  RFC3339              "2026-05-12T15:30:00-07:00"
                       The canonical machine form; explicit timezone.

  Relative offset      "+30s"      seconds
                       "+30m"      minutes
                       "+2h"       hours
                       "+3d"       days (whole days only, not composable)
                       "+1h30m"    any Go duration (h/m/s/ms/us/ns/composed)

  Today at TIME        "today 17:00"        24-hour clock
                       "today 5:30pm"       12-hour with am/pm
                       "today 9am"          minutes default to :00
                       The time must still be in the future.

  Tomorrow at TIME     "tomorrow 9am"
                       "tomorrow 23:59"
                       "tomorrow 12am"      midnight
                       "tomorrow 12pm"      noon

The resolved instant must be strictly after the current time; a past
time is rejected with exit code 5.`,
		Example: `  eon add --cron "@hourly" --name backup -- /usr/local/bin/backup.sh
  eon add --cron "0 9 * * 1-5" -- 'say "stand up"'
  eon add --cron "@every 30s" -- /bin/echo tick
  eon add --at "tomorrow 9am" --name morning -- 'say "good morning"'
  eon add --at "+30m" -- "echo reminder | mail me@example.com"
  eon add --at "2026-12-31T23:59:00-08:00" --name year-end -- ./fireworks`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, cleanup, err := openService()
			if err != nil {
				return err
			}
			defer cleanup()

			if (cronExpr == "") == (atExpr == "") {
				return usageErrf("provide exactly one of --cron or --at")
			}
			if cmd.ArgsLenAtDash() < 0 {
				return usageErrf("place the command after '--' (e.g. `eon add --cron '@hourly' -- /bin/echo hi`)")
			}

			spec := eon.JobSpec{Command: wrapCommand(args), Name: name}
			if spec.Name == "" {
				spec.Name = strings.Join(args, " ")
			}
			if cronExpr != "" {
				spec.Cron = cronExpr
			} else {
				fireAt, err := eon.ParseAt(atExpr, time.Now())
				if err != nil {
					return err
				}
				spec.FireAt = fireAt
			}

			warnIfDaemonDown(cmd.Context(), s, stderr)

			job, err := s.Add(cmd.Context(), spec)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), job)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added job %s (%s, %s)\n",
				job.ID, job.Kind, scheduleSummary(job))
			return nil
		},
	}
	cmd.Flags().StringVar(&cronExpr, "cron", "", "cron expression (mutually exclusive with --at)")
	cmd.Flags().StringVar(&atExpr, "at", "", "fire time for a one-shot job; wall-clock (mutually exclusive with --cron)")
	cmd.Flags().StringVar(&name, "name", "", "human label (defaults to the command)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the created job as JSON on stdout")
	return cmd
}

// wrapCommand turns the positional argv into the form the scheduler
// exec's. One positional is treated as a shell line and wrapped in
// `/bin/sh -c`; two or more are treated as explicit argv.
func wrapCommand(args []string) []string {
	if len(args) == 1 {
		return []string{"/bin/sh", "-c", args[0]}
	}
	return args
}
