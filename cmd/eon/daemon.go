package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/rednafi/eon"
	"github.com/rednafi/eon/daemon"
	"github.com/rednafi/eon/sched"
	"github.com/rednafi/eon/store"
	"github.com/spf13/cobra"
)

func newDaemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "daemon",
		Hidden: true,
		Short:  "Run the in-process scheduler until SIGTERM (internal).",
		Long: `Internal entrypoint invoked by the launchd/systemd unit that
'eon install' writes. Not intended for direct user invocation —
use 'eon install' to register a supervised daemon and 'eon stop'
to ask it to exit.

Holds an OS-level flock on $DATA/eon.lock for its lifetime so a
second daemon against the same data dir fails immediately with
exit 4. Sleeps until the soonest scheduled fire (deadline lives in
the database, not in memory), waking on SIGHUP for early
re-evaluation after a CLI mutation. Exits cleanly on SIGTERM/SIGINT.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := dataDir()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("mkdir data dir: %w", err)
			}

			release, err := daemon.AcquireRunLock(dir)
			if err != nil {
				return err
			}
			if release == nil {
				pid, _, _, _ := daemon.ProbeRunLock(dir)
				return fmt.Errorf("%w: pid %d", eon.ErrDaemonUp, pid)
			}
			defer release()

			st, err := store.Open(dir)
			if err != nil {
				return err
			}
			defer st.Close()

			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
			s := sched.New(st, sched.Config{Logger: logger})

			// SIGHUP translates 1:1 into Scheduler.Wake. The CLI sends
			// it after every store mutation so the daemon picks up
			// changes without waiting out its current sleep.
			hup := make(chan os.Signal, 4)
			signal.Notify(hup, syscall.SIGHUP)
			defer signal.Stop(hup)
			go func() {
				for {
					select {
					case <-cmd.Context().Done():
						return
					case <-hup:
						s.Wake()
					}
				}
			}()

			logger.Info("eond started", "data_dir", dir, "pid", os.Getpid())
			err = s.Start(cmd.Context())
			logger.Info("eond stopped")
			if err != nil && !errors.Is(err, cmd.Context().Err()) {
				return err
			}
			return nil
		},
	}
}
