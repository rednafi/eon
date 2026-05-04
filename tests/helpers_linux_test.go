//go:build linux

package tests

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// requireRealCron is the gate every Linux real-cron test goes through. We
// keep it strict — the test should self-skip when run outside the container,
// not silently pass.
func requireRealCron(t *testing.T) {
	t.Helper()
	if os.Getenv("EON_RUN_REAL_CRON") != "1" {
		t.Skip("set EON_RUN_REAL_CRON=1 to run (mutates the user's crontab)")
	}
	if _, err := exec.LookPath("crontab"); err != nil {
		t.Skip("no crontab binary on this host")
	}
}

// withCrontab snapshots the user's current crontab, installs the supplied
// content, and registers a Cleanup that restores the original. Any leak
// would trample real schedules — never inline this logic.
func withCrontab(t *testing.T, content string) {
	t.Helper()
	original, hadOriginal := snapshotCrontab(t)
	t.Cleanup(func() { restoreCrontab(t, original, hadOriginal) })
	if err := installCrontab(content); err != nil {
		t.Fatalf("install crontab: %v", err)
	}
}

func snapshotCrontab(t *testing.T) ([]byte, bool) {
	t.Helper()
	out, err := exec.Command("crontab", "-l").CombinedOutput()
	if err != nil {
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

func installCrontab(content string) error {
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &exec.ExitError{Stderr: out}
	}
	return nil
}
