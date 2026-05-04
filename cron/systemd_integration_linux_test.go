//go:build linux

package cron

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSystemdRealRoundTrip exercises the file-based path of the systemd
// origin against the real default directory ($XDG_CONFIG_HOME/systemd/user
// or ~/.config/systemd/user). It writes a unique label so we never collide
// with an existing user timer, and removes its files in cleanup.
//
// The systemctl runner is set to nil so the test stays portable: it works
// whether or not the container has a user-scope systemd running.
func TestSystemdRealRoundTrip(t *testing.T) {
	if os.Getenv("EON_RUN_REAL_CRON") != "1" {
		t.Skip("set EON_RUN_REAL_CRON=1 to run (writes to the user's systemd dir)")
	}
	src := NewUserSystemd()
	src.Systemctl = nil // avoid touching a possibly-absent systemd

	label := "eon-real-" + randomSuffix(t)
	timerPath := filepath.Join(src.Dir, label+".timer")
	servicePath := filepath.Join(src.Dir, label+".service")

	if err := os.MkdirAll(src.Dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(timerPath)
		_ = os.Remove(servicePath)
	})

	timer := `[Unit]
Description=eon real round-trip
[Timer]
OnCalendar=hourly
[Install]
WantedBy=timers.target
`
	service := `[Unit]
Description=eon real round-trip
[Service]
Type=oneshot
ExecStart=/bin/echo eon-real-test
`
	if err := os.WriteFile(timerPath, []byte(timer), 0o644); err != nil {
		t.Fatalf("write timer: %v", err)
	}
	if err := os.WriteFile(servicePath, []byte(service), 0o644); err != nil {
		t.Fatalf("write service: %v", err)
	}

	jobs, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *Job
	for i, j := range jobs {
		if j.Name == label {
			found = &jobs[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("test timer not in list (%d jobs): names=%v", len(jobs), names(jobs))
	}
	if found.Command != "/bin/echo eon-real-test" {
		t.Errorf("command mismatch: %q", found.Command)
	}
	if found.Schedule != "hourly" {
		t.Errorf("schedule mismatch: %q", found.Schedule)
	}

	if err := src.Delete(context.Background(), found.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(timerPath); !os.IsNotExist(err) {
		t.Errorf("timer file not removed: %v", err)
	}
}

func names(jobs []Job) []string {
	out := make([]string, len(jobs))
	for i, j := range jobs {
		out[i] = j.Name
	}
	return out
}

// randomSuffix returns a per-test-name suffix, deterministic enough that two
// retries don't fight, but unique enough across tests in the same file.
func randomSuffix(t *testing.T) string {
	t.Helper()
	return strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
}
