package main

import (
	"fmt"
	"os"
	"time"

	"github.com/rednafi/eon/daemon"
	"github.com/spf13/cobra"
)

func newInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Register the daemon with launchd (macOS) or systemd --user (Linux).",
		Long: `Write the platform's supervisor unit and bootstrap it so the eon daemon
starts on login and respawns on crash. Idempotent: a second 'eon install'
is a no-op. To update the binary path after upgrading, run 'eon uninstall'
followed by 'eon install'.`,
		Example: "  eon install\n  eon uninstall && eon install   # refresh after binary move",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := dataDir()
			if err != nil {
				return err
			}
			bin, err := os.Executable()
			if err != nil {
				return fmt.Errorf("install: locate self: %w", err)
			}
			installed, err := daemon.Install(bin, dir)
			if err != nil {
				return err
			}
			if installed {
				fmt.Fprintln(cmd.OutOrStdout(), "installed and started supervisor")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "supervisor already installed; no changes")
			}
			return nil
		},
	}
}

func newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the launchd/systemd supervisor unit.",
		Long: `Unload and delete the supervisor unit written by 'eon install'. Leaves
the database and binary intact; for a full purge use 'eon seppuku'.
Idempotent: a second 'eon uninstall' reports nothing to remove.`,
		Example: "  eon uninstall",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			removed, err := daemon.Uninstall()
			if err != nil {
				return err
			}
			if removed {
				fmt.Fprintln(cmd.OutOrStdout(), "supervisor removed")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "no supervisor installed")
			}
			return nil
		},
	}
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Signal the running daemon to exit.",
		Long: `Send SIGTERM to the running daemon and wait for it to release the
single-instance lock. Escalates to SIGKILL after 5 seconds. Returns
success either way (idempotent), printing a note when no daemon was
running to begin with.`,
		Example: "  eon stop",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := dataDir()
			if err != nil {
				return err
			}
			running, gracefully, err := daemon.StopDaemon(dir, 5*time.Second)
			if err != nil {
				return fmt.Errorf("stop: %w", err)
			}
			switch {
			case !running:
				fmt.Fprintln(cmd.OutOrStdout(), "no daemon running")
			case gracefully:
				fmt.Fprintln(cmd.OutOrStdout(), "daemon stopped")
			default:
				fmt.Fprintln(cmd.OutOrStdout(), "daemon stopped (forced)")
			}
			return nil
		},
	}
}
