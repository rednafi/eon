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

// version and commit are overridden at release build time via -ldflags.
// When version is empty, Fang falls back to Go build-info metadata, which
// keeps `go install ...@version` builds self-describing.
var (
	version string
	commit  string
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	root := newRoot()

	// AnsiColorScheme leaves the actual hues up to the user's terminal
	// palette, so help/error text stays legible on both dark and light
	// backgrounds — the default scheme uses fixed lipgloss colors that
	// look dim on dark mode.
	if err := fang.Execute(ctx, root, fangOptions()...); err != nil {
		os.Exit(exitCode(err))
	}
}

func fangOptions() []fang.Option {
	opts := []fang.Option{fang.WithColorSchemeFunc(fang.AnsiColorScheme)}
	if version != "" {
		opts = append(opts, fang.WithVersion(version))
	}
	if commit != "" {
		opts = append(opts, fang.WithCommit(commit))
	}
	return opts
}
