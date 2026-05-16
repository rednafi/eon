// Command eon is the CLI and daemon for the eon job scheduler.
//
// The same binary serves both roles:
//   - User commands mutate the local SQLite store.
//   - The supervisor runs the hidden daemon command.
//
// The daemon command is hidden from help.
// The supported entrypoint is `eon install`.
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

	// AnsiColorScheme uses the user's terminal palette.
	//
	// That keeps help and error text legible on dark and light themes.
	// Fang's default colors can look dim on dark mode.
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
