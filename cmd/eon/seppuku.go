package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rednafi/eon/daemon"
	"github.com/spf13/cobra"
)

func newSeppukuCmd() *cobra.Command {
	var confirm bool
	cmd := &cobra.Command{
		Use:   "seppuku",
		Short: "Purge every trace of eon from this machine.",
		Long: `Stop the running daemon, remove the launchd/systemd supervisor unit,
delete the data directory (database, lock files, log) and finally remove
the eon binary itself.

Destructive and irreversible. Runs as a dry run by default — prints
the plan without touching anything. Pass --yes to actually perform it.`,
		Example: "  eon seppuku            # dry-run; show the plan\n  eon seppuku --yes      # actually wipe everything",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return seppuku(cmd, confirm)
		},
	}
	cmd.Flags().BoolVar(&confirm, "yes", false, "actually perform the destructive operations (default is dry-run)")
	return cmd
}

// seppuku is the destructive cleanup driver. perform=false means
// dry-run — every "would …" message represents an action the
// performing path would take.
func seppuku(cmd *cobra.Command, perform bool) error {
	out := cmd.OutOrStdout()
	dir, err := dataDir()
	if err != nil {
		return err
	}
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self: %w", err)
	}

	stopDaemon(out, dir, perform)

	if daemon.IsSupervised() {
		if perform {
			fmt.Fprintln(out, "removing supervisor unit")
			if _, err := daemon.Uninstall(); err != nil {
				return err
			}
		} else {
			fmt.Fprintln(out, "would remove supervisor unit")
		}
	}

	if _, err := os.Stat(dir); err == nil {
		if perform {
			fmt.Fprintf(out, "removing data dir %s\n", dir)
			if err := os.RemoveAll(dir); err != nil {
				return fmt.Errorf("remove data dir: %w", err)
			}
		} else {
			fmt.Fprintf(out, "would remove data dir %s\n", dir)
		}
	}

	// Removing the running binary is fine on Unix: the kernel keeps
	// the inode alive until this process exits, so we finish cleanly
	// and the file is gone the next time something looks for it.
	if perform {
		fmt.Fprintf(out, "removing binary %s\n", bin)
		if err := os.Remove(bin); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove binary: %w", err)
		}
	} else {
		fmt.Fprintf(out, "would remove binary %s\n", bin)
	}

	if perform {
		fmt.Fprintln(out, "done")
	} else {
		fmt.Fprintln(out, "(dry run; nothing was modified)")
		fmt.Fprintln(out, "Re-run with --yes to actually perform the operations above.")
	}
	return nil
}

// stopDaemon mirrors `eon stop` but stays silent when no daemon is
// running (seppuku's caller doesn't care about that line).
func stopDaemon(out io.Writer, dir string, perform bool) {
	pid, _, running, _ := daemon.ProbeRunLock(dir)
	if !running {
		return
	}
	if !perform {
		fmt.Fprintf(out, "would stop daemon (pid %d)\n", pid)
		return
	}
	fmt.Fprintf(out, "stopping daemon (pid %d)\n", pid)
	_, _, _ = daemon.StopDaemon(dir, 5*time.Second)
}
