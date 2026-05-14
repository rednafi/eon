// Command eon is the CLI and daemon for the eon job scheduler. The
// same binary serves both roles: invoked with `add`/`ls`/`rm`/... it
// acts as a client that mutates the local SQLite store; invoked by
// launchd/systemd (via `eon install`) it runs the in-process
// scheduler scheduler until SIGTERM. The daemon subcommand is hidden
// from the user-facing help because the supervisor unit is the
// supported entrypoint.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/fang"
)

// version is overridden at build time via -ldflags '-X main.version=...'.
var version = "0.1.0"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	root := newRoot()
	root.Version = version

	// AnsiColorScheme leaves the actual hues up to the user's terminal
	// palette, so help/error text stays legible on both dark and light
	// backgrounds — the default scheme uses fixed lipgloss colors that
	// look dim on dark mode.
	if err := fang.Execute(ctx, root, fang.WithColorSchemeFunc(fang.AnsiColorScheme)); err != nil {
		os.Exit(exitCode(err))
	}
}
