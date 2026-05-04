// Command eon is a local cron monitor.
//
// main is the composition root: it asks the per-platform factory for the
// concrete cron.Source values, wires them into a cron.Manager, and hands
// that to either the TUI (no args) or the cobra+fang CLI (subcommand).
// The cron, cli, and tui packages do not name any backend directly — they
// only see the cron.Source interface. Add a new backend by writing a new
// subpackage under cron/ and adding it to the platformSources factory.
package main

import (
	"context"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/rednafi/eon/cli"
	"github.com/rednafi/eon/cron"
	"github.com/rednafi/eon/tui"
)

func main() {
	sources, errs := platformSources()
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "warning: %v\n", e)
	}
	mgr := cron.NewManager(sources...)

	if len(os.Args) <= 1 {
		runTUI(mgr)
		return
	}
	if err := cli.Execute(context.Background(), mgr); err != nil {
		os.Exit(1)
	}
}

func runTUI(mgr *cron.Manager) {
	if _, err := tea.NewProgram(tui.New(mgr)).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		os.Exit(1)
	}
}
