//go:build linux

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rednafi/eon/cron"
	"github.com/rednafi/eon/cron/systemd"
)

// TestSystemdRealRoundTrip writes a uniquely-labelled .timer + .service
// into the user's real ~/.config/systemd/user/, asserts systemd.NewUser
// surfaces it, and cleans up. The systemctl runner is nil so the test stays
// portable across containers with and without a running user systemd.
func TestSystemdRealRoundTrip(t *testing.T) {
	if os.Getenv("EON_RUN_REAL_CRON") != "1" {
		t.Skip("set EON_RUN_REAL_CRON=1 to run (writes to ~/.config/systemd/user)")
	}
	src := systemd.NewUser()
	src.Systemctl = nil

	label := "eon-real-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
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

	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *cron.Job
	for i, j := range jobs {
		if j.Name == label {
			found = &jobs[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("test timer not in list (%d jobs)", len(jobs))
	}
	if found.Command != "/bin/echo eon-real-test" {
		t.Errorf("command mismatch: %q", found.Command)
	}
	if err := src.Delete(t.Context(), found.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(timerPath); !os.IsNotExist(err) {
		t.Errorf("timer file not removed: %v", err)
	}
}
