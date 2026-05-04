//go:build linux

package cron

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestCrontabRealRoundTrip drives the real `crontab` binary on the host to
// confirm eon's parse/delete logic round-trips against the actual cron
// spool. It MUTATES the running user's crontab, so it's gated behind
// EON_RUN_REAL_CRON=1 — only the Linux container CI job sets that.
//
// We snapshot the user's existing crontab on entry and restore it on exit
// so a leaked run doesn't trample a developer's real schedule.
func TestCrontabRealRoundTrip(t *testing.T) {
	if os.Getenv("EON_RUN_REAL_CRON") != "1" {
		t.Skip("set EON_RUN_REAL_CRON=1 to run (mutates the user's crontab)")
	}
	if _, err := exec.LookPath("crontab"); err != nil {
		t.Skip("no crontab binary on this host")
	}

	original, hadOriginal := snapshotCrontab(t)
	t.Cleanup(func() { restoreCrontab(t, original, hadOriginal) })

	// Install a deterministic test crontab.
	want := "*/5 * * * * /bin/echo eon-real-test\n@daily /bin/true\n"
	if err := writeCrontab(want); err != nil {
		t.Fatalf("install: %v", err)
	}

	src := NewCrontab()
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

	// Delete the first one and verify only the other remains.
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
		t.Errorf("deleted job %q is still present", target.ID)
	}

	// Delete the last one — should `crontab -r` away the spool entirely.
	if err := src.Delete(context.Background(), jobs[0].ID); err != nil {
		t.Fatalf("delete last: %v", err)
	}
	jobs, _ = src.List(context.Background())
	if len(jobs) != 0 {
		t.Errorf("want 0 jobs after final delete, got %d", len(jobs))
	}
}

func snapshotCrontab(t *testing.T) ([]byte, bool) {
	t.Helper()
	out, err := exec.Command("crontab", "-l").CombinedOutput()
	if err != nil {
		// Either no crontab for user, or crontab missing. Treat as "empty".
		if strings.Contains(string(out), "no crontab") {
			return nil, false
		}
		t.Logf("crontab -l: %s", strings.TrimSpace(string(out)))
		return nil, false
	}
	return out, true
}

func restoreCrontab(t *testing.T, content []byte, had bool) {
	t.Helper()
	if !had {
		_ = exec.Command("crontab", "-r").Run()
		return
	}
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = bytes.NewReader(content)
	if err := cmd.Run(); err != nil {
		t.Errorf("restore crontab: %v", err)
	}
}

func writeCrontab(content string) error {
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &exec.ExitError{Stderr: out}
	}
	return nil
}
