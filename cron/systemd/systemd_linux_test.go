//go:build linux

package systemd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rednafi/eon/cron"
)

const sampleTimer = `[Unit]
Description=Test timer

[Timer]
OnCalendar=hourly
Persistent=true

[Install]
WantedBy=timers.target
`

const sampleService = `[Unit]
Description=Test service

[Service]
Type=oneshot
ExecStart=/bin/echo hello

[Install]
WantedBy=default.target
`

func writePair(t *testing.T, dir, label string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, label+".timer"), []byte(sampleTimer), 0o600); err != nil {
		t.Fatalf("write timer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, label+".service"), []byte(sampleService), 0o600); err != nil {
		t.Fatalf("write service: %v", err)
	}
}

func TestSystemdListFromTempDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writePair(t, dir, "eon-test-1")
	writePair(t, dir, "eon-test-2")

	src := &Systemd{Dir: dir, Tag: "test"}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(jobs))
	}
	if jobs[0].Schedule != "hourly" {
		t.Errorf("want schedule 'hourly', got %q", jobs[0].Schedule)
	}
	if jobs[0].Command != "/bin/echo hello" {
		t.Errorf("want command '/bin/echo hello', got %q", jobs[0].Command)
	}
}

func TestSystemdDeleteRemovesUnits(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writePair(t, dir, "eon-target")

	src := &Systemd{Dir: dir, Tag: "test"}
	if err := src.Delete(t.Context(), "systemd-test:eon-target"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, ext := range []string{".timer", ".service"} {
		if _, err := os.Stat(filepath.Join(dir, "eon-target"+ext)); !os.IsNotExist(err) {
			t.Errorf("%s still exists: %v", ext, err)
		}
	}
	if err := src.Delete(t.Context(), "systemd-test:eon-target"); !errors.Is(err, cron.ErrNotFound) {
		t.Errorf("second delete should return cron.ErrNotFound, got %v", err)
	}
}

func TestSystemdHundredTimers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for i := range 100 {
		writePair(t, dir, fmt.Sprintf("eon-bulk-%03d", i))
	}
	src := &Systemd{Dir: dir, Tag: "test"}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 100 {
		t.Fatalf("want 100 jobs, got %d", len(jobs))
	}
	for _, j := range jobs {
		if !strings.HasPrefix(j.ID, "systemd-test:eon-bulk-") {
			t.Errorf("bad ID: %q", j.ID)
		}
	}
}

func TestSystemdDeleteUnknownReturnsNotFound(t *testing.T) {
	t.Parallel()
	src := &Systemd{Dir: t.TempDir(), Tag: "test"}
	if err := src.Delete(t.Context(), "systemd-test:nope"); !errors.Is(err, cron.ErrNotFound) {
		t.Errorf("want cron.ErrNotFound, got %v", err)
	}
}

func TestSystemdReadOnlyMarksAndRefuses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writePair(t, dir, "system-job")
	src := &Systemd{Dir: dir, Tag: "etc", ReadOnly: true}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job, got %d", len(jobs))
	}
	if got := src.Scope(); got != cron.ScopeSystem {
		t.Errorf("read-only source scope = %v, want %v", got, cron.ScopeSystem)
	}
	err = src.Delete(t.Context(), jobs[0].ID)
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("read-only delete should fail with read-only error, got %v", err)
	}
}

// readTimer's schedule fallback chain prefers OnCalendar, then "every <X>"
// from OnUnitActiveSec, then "boot+<X>" from OnBootSec, finally
// "(no schedule)". Cover each step explicitly.
func TestSystemdScheduleFallbackChain(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, timer, want string
	}{
		{"OnCalendar wins", "[Timer]\nOnCalendar=daily\n", "daily"},
		{"OnUnitActiveSec wins when no OnCalendar", "[Timer]\nOnUnitActiveSec=30min\n", "every 30min"},
		{"OnBootSec wins when neither calendar nor active", "[Timer]\nOnBootSec=2min\n", "boot+2min"},
		{"no schedule keys at all", "[Unit]\nDescription=Demo\n", "(no schedule)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			label := "fallback-" + strings.ReplaceAll(tc.name, " ", "_")
			if err := os.WriteFile(filepath.Join(dir, label+".timer"), []byte(tc.timer), 0o600); err != nil {
				t.Fatalf("write timer: %v", err)
			}
			src := &Systemd{Dir: dir, Tag: "test"}
			jobs, err := src.List(t.Context())
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(jobs) != 1 {
				t.Fatalf("want 1 job, got %d", len(jobs))
			}
			if jobs[0].Schedule != tc.want {
				t.Errorf("schedule = %q, want %q", jobs[0].Schedule, tc.want)
			}
		})
	}
}

// A timer without a sibling .service file should still surface — the
// "command" column is informational. Falling back to a synthetic
// "(systemd unit: <label>)" string lets the user spot the orphan.
func TestSystemdTimerWithoutServiceFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "orphan.timer"), []byte("[Timer]\nOnCalendar=hourly\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &Systemd{Dir: dir, Tag: "test"}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("orphan timer should still appear, got %d jobs", len(jobs))
	}
	if !strings.Contains(jobs[0].Command, "orphan") {
		t.Errorf("orphan fallback command = %q, want it to mention orphan label", jobs[0].Command)
	}
}

// Best-effort daemon-reload after delete: if systemctl is nil (test fakes
// disable it), Delete must still succeed. The old code path passed nil
// straight to a function call and would panic.
func TestSystemdDeleteWithNilSystemctl(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writePair(t, dir, "no-systemctl")
	src := &Systemd{Dir: dir, Tag: "test", Systemctl: nil}
	if err := src.Delete(t.Context(), "systemd-test:no-systemctl"); err != nil {
		t.Errorf("delete with nil systemctl: %v", err)
	}
}

func TestSystemdNameAndScope(t *testing.T) {
	t.Parallel()
	user := &Systemd{Tag: "user"}
	if user.Name() != "systemd-user" {
		t.Errorf("Name() = %q", user.Name())
	}
	if user.Scope() != cron.ScopeUser {
		t.Errorf("Scope() = %v", user.Scope())
	}
	system := &Systemd{Tag: "etc", ReadOnly: true}
	if system.Scope() != cron.ScopeSystem {
		t.Errorf("ReadOnly Scope() = %v", system.Scope())
	}
}

// Delete with an ID that doesn't carry the source's "systemd-<tag>:" prefix
// should return ErrNotFound rather than touching the filesystem. The
// Manager fan-out depends on this so other sources get a chance.
func TestSystemdDeleteForeignIDReturnsNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writePair(t, dir, "untouchable")
	src := &Systemd{Dir: dir, Tag: "test"}
	err := src.Delete(t.Context(), "systemd-other:untouchable")
	if !errors.Is(err, cron.ErrNotFound) {
		t.Errorf("foreign-prefix ID: want ErrNotFound, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "untouchable.timer")); err != nil {
		t.Errorf("timer mutated by foreign ID: %v", err)
	}
}
