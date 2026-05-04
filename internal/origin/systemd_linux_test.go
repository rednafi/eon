//go:build linux

package origin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	dir := t.TempDir()
	writePair(t, dir, "eon-test-1")
	writePair(t, dir, "eon-test-2")

	src := &Systemd{Dir: dir, Tag: "test"}
	jobs, err := src.List(context.Background())
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
	dir := t.TempDir()
	writePair(t, dir, "eon-target")

	src := &Systemd{Dir: dir, Tag: "test"}
	if err := src.Delete(context.Background(), "systemd-test:eon-target"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, ext := range []string{".timer", ".service"} {
		if _, err := os.Stat(filepath.Join(dir, "eon-target"+ext)); !os.IsNotExist(err) {
			t.Errorf("%s still exists: %v", ext, err)
		}
	}
	if err := src.Delete(context.Background(), "systemd-test:eon-target"); err != ErrNotFound {
		t.Errorf("second delete should return ErrNotFound, got %v", err)
	}
}

func TestSystemdHundredTimers(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 100; i++ {
		writePair(t, dir, fmt.Sprintf("eon-bulk-%03d", i))
	}
	src := &Systemd{Dir: dir, Tag: "test"}
	jobs, err := src.List(context.Background())
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
	src := &Systemd{Dir: t.TempDir(), Tag: "test"}
	if err := src.Delete(context.Background(), "systemd-test:nope"); err != ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestSystemdReadOnlyMarksAndRefuses(t *testing.T) {
	dir := t.TempDir()
	writePair(t, dir, "system-job")
	src := &Systemd{Dir: dir, Tag: "etc", ReadOnly: true}
	jobs, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 || !jobs[0].System {
		t.Fatalf("read-only source must mark Job.System=true: %+v", jobs)
	}
	err = src.Delete(context.Background(), jobs[0].ID)
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("read-only delete should fail with read-only error, got %v", err)
	}
}

func TestParseUnitIgnoresCommentsAndEmptySections(t *testing.T) {
	got := parseUnit(`# leading comment
; another

[Service]
ExecStart=/bin/foo

# trailing
`)
	if got["Service.ExecStart"] != "/bin/foo" {
		t.Errorf("want /bin/foo, got %q", got["Service.ExecStart"])
	}
}
