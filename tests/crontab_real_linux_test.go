//go:build linux

package tests

import (
	"context"
	"strings"
	"testing"

	"github.com/rednafi/eon/cron/crontab"
)

// TestCrontabRealRoundTrip drives the host's real crontab(1) to install a
// known crontab, parses it via crontab.New, deletes one entry, and
// verifies the spool reflects the change. Snapshot/restore protects the
// developer's actual schedule when EON_RUN_REAL_CRON is left on.
func TestCrontabRealRoundTrip(t *testing.T) {
	requireRealCron(t)
	withCrontab(t, "*/5 * * * * /bin/echo eon-real-test\n@daily /bin/true\n")

	src := crontab.New()
	jobs, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d: %v", len(jobs), jobs)
	}
	for _, j := range jobs {
		if !strings.Contains(j.Command, "echo eon-real-test") && j.Schedule != "@daily" {
			t.Errorf("unexpected job: %+v", j)
		}
	}

	target := jobs[0]
	if err := src.Delete(context.Background(), target.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	jobs, err = src.List(context.Background())
	if err != nil {
		t.Fatalf("re-list: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job after delete, got %d", len(jobs))
	}
	if jobs[0].ID == target.ID {
		t.Errorf("deleted job %q still present", target.ID)
	}

	if err := src.Delete(context.Background(), jobs[0].ID); err != nil {
		t.Fatalf("delete last: %v", err)
	}
	jobs, _ = src.List(context.Background())
	if len(jobs) != 0 {
		t.Errorf("want 0 jobs after final delete, got %d", len(jobs))
	}
}
