package tui

import (
	"flag"
	"fmt"
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/rednafi/eon/internal/origin"
)

var dumpSnapshot = flag.Bool("snapshot", false, "print rendered TUI to stdout for visual inspection")

// TestRenderSnapshot is a manual eyeballing aid: run with `-snapshot -v` to
// dump a plausible eon view to stdout. Skipped by default so the regular
// suite stays fast.
func TestRenderSnapshot(t *testing.T) {
	if !*dumpSnapshot {
		t.Skip("pass -snapshot to print")
	}
	jobs := []origin.Job{
		{ID: "launchd-user:com.foo.alpha", Kind: origin.KindLaunchd, Name: "com.foo.alpha", Schedule: "every 5m", Status: "running"},
		{ID: "launchd-user:com.foo.beta", Kind: origin.KindLaunchd, Name: "com.foo.beta", Schedule: "at load", Status: "loaded"},
		{ID: "launchd-user:com.foo.gamma", Kind: origin.KindLaunchd, Name: "com.foo.gamma", Schedule: "31 0 * * 0", Status: "exited 1"},
		{ID: "crontab:abcd1234", Kind: origin.KindCrontab, Name: "backup.sh", Schedule: "@daily", Status: "scheduled"},
	}
	stub := &stubOrigin{jobs: jobs}
	mgr := origin.NewManager(stub)
	m := New(mgr)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 30})
	mm, _ = mm.Update(jobsLoadedMsg{jobs: jobs})

	fmt.Fprintln(os.Stdout, mm.(Model).render())
}
