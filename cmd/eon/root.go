package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rednafi/eon"
	"github.com/rednafi/eon/daemon"
	"github.com/rednafi/eon/store"

	"github.com/spf13/cobra"
)

// Global flags shared by every subcommand. Resolved in PersistentPreRunE.
type rootFlags struct {
	dataDir string
	quiet   bool
}

var globalFlags rootFlags

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:               "eon",
		CompletionOptions: cobra.CompletionOptions{HiddenDefaultCmd: true},
		Short:             "Cron and one-shot job scheduler that runs in-process.",
		Long: `eon schedules cron-style recurring jobs and one-shot jobs at a wall-clock
time and executes them inside its own daemon. It does not use the system
cron or at daemons, so it behaves identically on macOS and Linux.

State lives in a single SQLite file under the platform's data directory
(~/Library/Application Support/eon on macOS, $XDG_DATA_HOME/eon on Linux).
Captured output for the last 100 runs per job is retained for 100 days.

Run 'eon install' once to register the daemon as a launchd LaunchAgent
or systemd --user unit. After that, the supervisor keeps the daemon
running across logins and crashes; 'eon stop' asks it to exit and the
supervisor will respawn it the next time it's needed.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&globalFlags.dataDir, "data-dir", "",
		"override the data directory (defaults to the platform standard)")
	root.PersistentFlags().BoolVar(&globalFlags.quiet, "quiet", false,
		"suppress the daemon-down warning written to stderr")

	root.AddCommand(
		newAddCmd(),
		newListCmd(),
		newShowCmd(),
		newRemoveCmd(),
		newPruneCmd(),
		newEnableCmd(),
		newDisableCmd(),
		newLogsCmd(),
		newStatusCmd(),
		newInstallCmd(),
		newUninstallCmd(),
		newStopCmd(),
		newDaemonCmd(),
		newSeppukuCmd(),
		newDebugCmd(),
	)
	tagUsageErrors(root)
	return root
}

// tagUsageErrors wires every command (root + descendants) so flag-parse
// and positional-arg violations propagate as errUsage-wrapped errors.
// Without this, cobra surfaces those errors with no sentinel and the
// exit-code mapper falls through to 1 (unexpected) — but per contract
// they are usage errors (exit 2).
func tagUsageErrors(c *cobra.Command) {
	c.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return fmt.Errorf("%w: %v", errUsage, err)
	})
	if c.Args != nil {
		inner := c.Args
		c.Args = func(cmd *cobra.Command, args []string) error {
			if err := inner(cmd, args); err != nil {
				return fmt.Errorf("%w: %v", errUsage, err)
			}
			return nil
		}
	}
	for _, child := range c.Commands() {
		tagUsageErrors(child)
	}
}

// dataDir returns the effective data directory: the --data-dir flag if
// set, otherwise the platform default. The directory is created on
// demand.
func dataDir() (string, error) {
	if globalFlags.dataDir != "" {
		return globalFlags.dataDir, nil
	}
	return daemon.DataDir()
}

// openService opens the store and wraps it in a service. The
// returned cleanup closes the store; commands should defer it.
func openService() (*service, func(), error) {
	dir, err := dataDir()
	if err != nil {
		return nil, func() {}, err
	}
	st, err := store.Open(dir)
	if err != nil {
		return nil, func() {}, err
	}
	return newService(st), func() { _ = st.Close() }, nil
}

// warnIfDaemonDown writes a single stderr line when no daemon is
// running and no supervisor is installed. The check is done against
// the OS-level flock at $DATA/eon.lock, so a crashed daemon is
// instantly visible (the kernel released the lock).
func warnIfDaemonDown(_ context.Context, s *service, w io.Writer) {
	if globalFlags.quiet {
		return
	}
	state := s.DaemonState()
	if state.Running || state.Supervised {
		return
	}
	fmt.Fprintln(w, "warning: eond is not running. Jobs will not fire until you run 'eon install'.")
}

// exitCode maps a returned error to a stable shell exit code. Callers
// (main) os.Exit on the result. Layered switches keep the mapping
// readable as new sentinels appear.
func exitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errUsage), isUnknownCommand(err):
		return 2
	case errors.Is(err, eon.ErrNotFound):
		return 3
	case errors.Is(err, eon.ErrConflict), errors.Is(err, eon.ErrDaemonUp):
		return 4
	case errors.Is(err, eon.ErrDaemonDown),
		errors.Is(err, eon.ErrInvalidSpec),
		errors.Is(err, eon.ErrInvalidCron),
		errors.Is(err, eon.ErrInvalidTime),
		errors.Is(err, eon.ErrUnsupportedOS):
		return 5
	default:
		return 1
	}
}

// isUnknownCommand recognises Cobra's untyped "unknown command" error.
// Cobra does not export a sentinel for this case, so prefix detection
// is the only available hook. Narrow and anchored — false positives
// would only flow through if a future error message starts identically.
func isUnknownCommand(err error) bool {
	return strings.HasPrefix(err.Error(), "unknown command")
}

// errUsage is sentineled so we can map invalid-flag/arg situations to
// exit code 2 without depending on Cobra's error strings.
var errUsage = errors.New("usage error")

func usageErrf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{errUsage}, args...)...)
}

// stderr is a thin wrapper so tests can inject a buffer; real
// invocations write to os.Stderr.
var stderr io.Writer = os.Stderr
