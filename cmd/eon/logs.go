package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/rednafi/eon"
	"github.com/rednafi/eon/store"
	"github.com/spf13/cobra"
)

// logOpts controls the streaming behaviour of `eon logs`.
type logOpts struct {
	Follow bool          // when true, blocks waiting for new completed runs
	Lines  int           // last N lines from each emitted run (0 = all)
	Since  time.Duration // only runs started within this window (0 = no limit)
}

// pollInterval is how often --follow polls the store for new runs.
const pollInterval = 500 * time.Millisecond

func newLogsCmd() *cobra.Command {
	var (
		follow bool
		lines  int
		since  time.Duration
	)
	cmd := &cobra.Command{
		Use:   "logs ID|NAME",
		Short: "Print the captured output of a job's runs.",
		Long: `Print the captured stdout+stderr of one or more of a job's runs.

  -f, --follow     stream subsequent completed runs as they appear; blocks
                   until interrupted.
  -n, --lines N    only the last N lines of each emitted run.
  --since DUR      include all runs that started within the window
                   (e.g. 10m, 2h, 30s). Default: just the most recent.

Behaviour matrix:

  (no flags)            most recent run, raw output, no header.
  --lines N             same, trimmed to last N lines.
  --since DUR           every completed run in the window, oldest-first,
                        each preceded by a one-line header.
  --follow              most recent run with header, then new runs as they
                        complete, until interrupted.
  --since + --follow    runs in the window, then new ones as they appear.

Header format: '==> run #ID status exit=N finished=TIME'.`,
		Example: `  eon logs backup
  eon logs backup -n 50
  eon logs 7K3px -f
  eon logs backup --since 1h
  eon logs backup --since 30m -f`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if lines < 0 {
				return usageErrf("--lines must be non-negative")
			}
			if since < 0 {
				return usageErrf("--since must be non-negative")
			}
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
			return streamLogs(cmd.Context(), s.st, job.ID, logOpts{
				Follow: follow,
				Lines:  lines,
				Since:  since,
			}, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "stream new completed runs as they appear")
	cmd.Flags().IntVarP(&lines, "lines", "n", 0, "only the last N lines of each emitted run")
	cmd.Flags().DurationVar(&since, "since", 0, "include runs that started within this window (e.g. 10m, 2h)")
	return cmd
}

// streamLogs writes the requested run output to w. Without --since or
// --follow it emits just the latest run, raw. With --since it emits
// every completed run in the window (with headers). With --follow it
// then polls for new completed runs until ctx is cancelled.
func streamLogs(ctx context.Context, st *store.Store, jobID eon.JobID, opts logOpts, w io.Writer) error {
	// Cheapest path: no flags, dump the latest run raw. A job with
	// no runs yet is empty-state, not an error — exit cleanly.
	if opts.Since == 0 && !opts.Follow {
		run, err := st.LatestRun(ctx, jobID)
		if errors.Is(err, eon.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return emitRunOutput(ctx, st, run.ID, opts.Lines, w)
	}

	var lastEmitted int64
	if opts.Since > 0 {
		runs, err := st.ListRunsSince(ctx, jobID, time.Now().Add(-opts.Since))
		if err != nil {
			return err
		}
		for _, r := range runs {
			if r.FinishedAt.IsZero() {
				continue
			}
			if err := emitRunWithHeader(ctx, st, r, opts.Lines, w); err != nil {
				return err
			}
			lastEmitted = r.ID
		}
	} else if latest, err := st.LatestRun(ctx, jobID); err == nil && !latest.FinishedAt.IsZero() {
		if err := emitRunWithHeader(ctx, st, latest, opts.Lines, w); err != nil {
			return err
		}
		lastEmitted = latest.ID
	}

	if !opts.Follow {
		return nil
	}

	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		latest, err := st.LatestRun(ctx, jobID)
		if err != nil || latest.ID <= lastEmitted || latest.FinishedAt.IsZero() {
			continue
		}
		if err := emitRunWithHeader(ctx, st, latest, opts.Lines, w); err != nil {
			return err
		}
		lastEmitted = latest.ID
	}
}

func emitRunWithHeader(ctx context.Context, st *store.Store, run eon.Run, lines int, w io.Writer) error {
	header := fmt.Sprintf("==> run #%d %s exit=%d finished=%s\n",
		run.ID, run.Status, run.ExitCode, run.FinishedAt.UTC().Format(time.RFC3339))
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	return emitRunOutput(ctx, st, run.ID, lines, w)
}

func emitRunOutput(ctx context.Context, st *store.Store, runID int64, lines int, w io.Writer) error {
	rc, err := st.OpenRunLog(ctx, runID)
	if err != nil {
		return err
	}
	defer rc.Close()
	if lines <= 0 {
		_, err = io.Copy(w, rc)
		return err
	}
	buf, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	_, err = w.Write(tailLines(buf, lines))
	return err
}

// tailLines returns at most n trailing newline-delimited lines of buf.
func tailLines(buf []byte, n int) []byte {
	if n <= 0 || len(buf) == 0 {
		return buf
	}
	count := 0
	i := len(buf)
	if buf[i-1] == '\n' {
		i--
	}
	for ; i > 0; i-- {
		if buf[i-1] == '\n' {
			count++
			if count == n {
				return buf[i:]
			}
		}
	}
	return buf
}
