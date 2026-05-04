//go:build linux

package tests

import (
	"strings"
	"testing"

	"github.com/rednafi/eon/cron"
	"github.com/rednafi/eon/cron/source"
)

// TestCLIEnd2EndCrontab installs a real /var/spool crontab via the user's
// crontab(1) binary and exercises eon list/show/delete --yes against it.
// Gated by EON_RUN_REAL_CRON=1 so it only runs in the container CI job.
func TestCLIEnd2EndCrontab(t *testing.T) {
	requireRealCron(t)
	withCrontab(t, "*/5 * * * * /bin/echo eon-cli-test\n@daily /bin/true\n")

	mgr := cron.NewManager(source.NewCrontab())

	out, err := captureCLI(t, mgr, "list")
	mustOK(t, err)
	if !strings.Contains(out, "echo eon-cli-test") && !strings.Contains(out, "@daily") {
		t.Errorf("list missing expected entries:\n%s", out)
	}

	out, err = captureCLI(t, mgr, "show", "eon-cli-test")
	mustOK(t, err)
	if !strings.Contains(out, "echo eon-cli-test") {
		t.Errorf("show didn't surface command:\n%s", out)
	}

	_, err = captureCLI(t, mgr, "delete", "eon-cli-test", "--yes")
	mustOK(t, err)

	jobs, _ := mgr.List(t.Context())
	for _, j := range jobs {
		if strings.Contains(j.Command, "eon-cli-test") {
			t.Errorf("deleted job still present: %+v", j)
		}
	}
}
