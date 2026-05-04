// Command eon is a local cron monitor.
//
// With no arguments eon launches the bubbletea TUI; with a subcommand it
// dispatches through cobra + charm fang in the cli package.
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
	mgr, errs := cron.DefaultManager()
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "warning: %v\n", e)
	}
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
