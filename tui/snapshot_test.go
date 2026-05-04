package tui

import (
	"flag"
	"fmt"
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/rednafi/eon/cron"
)

var dumpSnapshot = flag.Bool("snapshot", false, "print rendered TUI to stdout for visual inspection")

// TestRenderSnapshot is a manual eyeballing aid: run with `-snapshot -v` to
// dump a plausible eon view to stdout. Skipped by default so the regular
// suite stays fast.
func TestRenderSnapshot(t *testing.T) {
	if !*dumpSnapshot {
		t.Skip("pass -snapshot to print")
	}
	jobs := []cron.Job{
		{ID: "launchd-user:com.foo.alpha", Kind: cron.KindLaunchd, Scope: cron.ScopeUser, Name: "com.foo.alpha", Schedule: "every 5m", Status: "running"},
		{ID: "launchd-user:com.foo.beta", Kind: cron.KindLaunchd, Scope: cron.ScopeUser, Name: "com.foo.beta", Schedule: "at load", Status: "loaded"},
		{ID: "launchd-system:com.example.daemon", Kind: cron.KindLaunchd, Scope: cron.ScopeSystem, Name: "com.example.daemon", Schedule: "every 1h", Status: "loaded"},
		{ID: "crontab:abcd1234", Kind: cron.KindCrontab, Scope: cron.ScopeUser, Name: "backup.sh", Schedule: "@daily", Status: "scheduled"},
	}
	stub := &stubOrigin{jobs: jobs}
	mgr := cron.NewManager(stub)
	m := New(mgr)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 30})
	mm, _ = mm.Update(jobsLoadedMsg{jobs: jobs})
	mm, _ = mm.Update(keyPress("a")) // show system rows alongside user rows

	fmt.Fprintln(os.Stdout, mm.(Model).render())
}
