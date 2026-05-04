// Command eon is a local cron monitor.
//
// With no arguments, it launches an interactive TUI; with a subcommand, it
// runs that command and exits. See `eon help` for the full surface.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"

	"github.com/rednafi/eon/cli"
	"github.com/rednafi/eon/cron"
	"github.com/rednafi/eon/tui"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	mgr, errs := cron.DefaultManager()
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "warning: %v\n", e)
	}

	// No argv → TUI. We treat the absence of arguments as the most useful
	// default; CLI-only users can pass `list` (or pipe to a script).
	if len(os.Args) <= 1 {
		runTUI(mgr)
		return
	}
	os.Exit(cli.Run(ctx, mgr, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func runTUI(mgr *cron.Manager) {
	p := tea.NewProgram(tui.New(mgr))
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		os.Exit(1)
	}
}
