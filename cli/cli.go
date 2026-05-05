// Package cli wires the eon subcommands onto cobra and runs them through
// charm fang for styled help, error rendering, and signal-aware execution.
//
// External callers use Execute. Tests use BuildRoot to drive cobra directly
// with custom args/IO so they don't shell out.
package cli

import (
	"context"
	"os"
	"syscall"

	fang "charm.land/fang/v2"
	"github.com/spf13/cobra"

	"github.com/rednafi/eon/cron"
)

// Version is overridden at build time via -ldflags.
var Version = "dev"

// Execute is the main-package entry point. It wires fang's signal handling
// and styled help, then dispatches to the cobra tree built by BuildRoot.
func Execute(ctx context.Context, mgr *cron.Manager) error {
	return fang.Execute(ctx, BuildRoot(mgr),
		fang.WithVersion(Version),
		fang.WithNotifySignal(os.Interrupt, syscall.SIGTERM),
	)
}

// BuildRoot returns the root cobra.Command with all subcommands attached.
// Exposed so tests can drive it via SetArgs/SetIn/SetOut without going
// through fang's signal/style layer.
func BuildRoot(mgr *cron.Manager) *cobra.Command {
	root := &cobra.Command{
		Use:   "eon",
		Short: "Local cron monitor",
		Long: `eon is a one-stop monitor for the recurring jobs running on this machine.

It scans your user crontab, your launchd user agents on macOS or systemd
user timers on Linux, and (via --all) the read-only system locations under
/Library/Launch*, /etc/crontab, /etc/cron.d, and /etc/systemd/system.`,
		SilenceUsage: true,
	}
	root.AddCommand(
		newListCmd(mgr),
		newShowCmd(mgr),
		newLogsCmd(mgr),
		newAddCmd(mgr),
		newEditCmd(mgr),
		newDeleteCmd(mgr),
	)
	return root
}
