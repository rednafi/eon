package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newDebugCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Inspection and debug helpers.",
		Long: `Subcommands for poking at eon's internals. Not part of the stable
user-facing API; tools and messages here can change without notice.`,
		Example: "  eon debug db                # open a sqlite shell against eon's database",
		Args:    cobra.NoArgs,
	}
	cmd.AddCommand(newDebugDBCmd())
	return cmd
}

func newDebugDBCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "db",
		Short: "Open the SQLite shell against eon's database.",
		Long: `Shell out to sqlite3 with eon's database file. A starter query
showing the 10 most recently created jobs is printed before handing
control over; from there it's a regular interactive sqlite3 session.
Requires sqlite3 on PATH.`,
		Example: "  eon debug db",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sqlite, err := exec.LookPath("sqlite3")
			if err != nil {
				return fmt.Errorf("sqlite3 not on PATH: %w (install it to use this command)", err)
			}
			dir, err := dataDir()
			if err != nil {
				return err
			}
			dbPath := filepath.Join(dir, "eon.db")
			if _, err := os.Stat(dbPath); err != nil {
				return fmt.Errorf("database not found at %s: %w", dbPath, err)
			}
			// nano-epoch → human dates via datetime(); column mode +
			// headers make the starter table actually legible.
			args := []string{
				"-cmd", ".headers on",
				"-cmd", ".mode column",
				"-cmd", "SELECT id, name, kind, status, last_status, " +
					"datetime(last_run_at/1000000000, 'unixepoch', 'localtime') AS last_run, " +
					"datetime(created_at/1000000000, 'unixepoch', 'localtime') AS created " +
					"FROM jobs ORDER BY created_at DESC LIMIT 10;",
				dbPath,
			}
			ex := exec.Command(sqlite, args...)
			ex.Stdin = os.Stdin
			ex.Stdout = cmd.OutOrStdout()
			ex.Stderr = stderr
			return ex.Run()
		},
	}
}
