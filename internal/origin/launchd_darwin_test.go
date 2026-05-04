//go:build darwin

package origin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minimalPlist returns a launchd plist body with the supplied label and
// minute interval. Tests use it to spin up dozens of fixtures without keeping
// a separate testdata tree.
func minimalPlist(label string, intervalSeconds int) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>/bin/echo</string><string>%s</string></array>
<key>StartInterval</key><integer>%d</integer>
<key>StandardOutPath</key><string>/tmp/%s.out</string>
</dict></plist>`, label, label, intervalSeconds, label)
}

// arrayCalendarPlist exercises the "StartCalendarInterval is an array" path
// that broke real-world git-scm plists. We assert eon parses it without
// dropping the job — a regression here would silently hide users' jobs.
func arrayCalendarPlist(label string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>/bin/echo</string></array>
<key>StartCalendarInterval</key>
<array>
<dict><key>Hour</key><integer>9</integer><key>Minute</key><integer>0</integer></dict>
<dict><key>Hour</key><integer>17</integer><key>Minute</key><integer>0</integer></dict>
</array>
</dict></plist>`, label)
}

func TestLaunchdListFromTempDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "com.example.foo.plist"), []byte(minimalPlist("com.example.foo", 60)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "com.example.array.plist"), []byte(arrayCalendarPlist("com.example.array")), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Garbage file shouldn't crash listing.
	if err := os.WriteFile(filepath.Join(dir, "ignore-me.txt"), []byte("not a plist"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	src := &Launchd{Dir: dir, Tag: "test"}
	jobs, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d: %v", len(jobs), jobs)
	}
	for _, j := range jobs {
		if !strings.HasPrefix(j.ID, "launchd-test:") {
			t.Errorf("bad ID prefix: %q", j.ID)
		}
		if j.Schedule == "" {
			t.Errorf("empty schedule for %q", j.ID)
		}
	}
}

func TestLaunchdDeleteRemovesPlist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "com.example.target.plist")
	if err := os.WriteFile(path, []byte(minimalPlist("com.example.target", 60)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &Launchd{Dir: dir, Tag: "test"}

	if err := src.Delete(context.Background(), "launchd-test:com.example.target"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("plist still exists: %v", err)
	}
	if err := src.Delete(context.Background(), "launchd-test:com.example.target"); err != ErrNotFound {
		t.Errorf("second delete should return ErrNotFound, got %v", err)
	}
}

func TestLaunchdReadOnlyRejectsDelete(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "com.example.lock.plist"), []byte(minimalPlist("com.example.lock", 60)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &Launchd{Dir: dir, Tag: "system", ReadOnly: true}
	err := src.Delete(context.Background(), "launchd-system:com.example.lock")
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("want read-only error, got %v", err)
	}
}

func TestLaunchdHundredJobs(t *testing.T) {
	// The user explicitly asked us to verify 100-job listing and deletion
	// behavior. We materialize 100 plists in a tmp dir, list them, then
	// delete every other one and re-list.
	dir := t.TempDir()
	for i := 0; i < 100; i++ {
		label := fmt.Sprintf("com.eon.test.%03d", i)
		path := filepath.Join(dir, label+".plist")
		if err := os.WriteFile(path, []byte(minimalPlist(label, 30+i)), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	src := &Launchd{Dir: dir, Tag: "test"}
	jobs, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 100 {
		t.Fatalf("want 100 jobs, got %d", len(jobs))
	}
	// Sort is alphabetical, so jobs[0] should be label 000.
	if jobs[0].Name != "com.eon.test.000" {
		t.Errorf("sort order broken: %q", jobs[0].Name)
	}

	deleted := 0
	for _, j := range jobs {
		// Delete the even-suffixed jobs.
		if strings.HasSuffix(j.Name, "0") || strings.HasSuffix(j.Name, "2") ||
			strings.HasSuffix(j.Name, "4") || strings.HasSuffix(j.Name, "6") || strings.HasSuffix(j.Name, "8") {
			if err := src.Delete(context.Background(), j.ID); err != nil {
				t.Errorf("delete %s: %v", j.ID, err)
			}
			deleted++
		}
	}
	if deleted != 50 {
		t.Fatalf("expected to delete 50 jobs, deleted %d", deleted)
	}
	jobs, _ = src.List(context.Background())
	if len(jobs) != 50 {
		t.Fatalf("want 50 jobs remaining, got %d", len(jobs))
	}
}

func TestLaunchdMissingDir(t *testing.T) {
	src := &Launchd{Dir: "/no/such/path/in/this/test", Tag: "test"}
	jobs, err := src.List(context.Background())
	if err != nil {
		t.Errorf("missing dir should not error: %v", err)
	}
	if jobs != nil {
		t.Errorf("want nil jobs for missing dir, got %v", jobs)
	}
}

func TestLaunchdEnrichWithFakeRunner(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "com.example.enrich.plist"), []byte(minimalPlist("com.example.enrich", 60)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &Launchd{
		Dir: dir,
		Tag: "test",
		Runner: func(_ context.Context, args []string) ([]byte, error) {
			return []byte("PID\tStatus\tLabel\n1234\t0\tcom.example.enrich\n"), nil
		},
	}
	jobs, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job, got %d", len(jobs))
	}
	if jobs[0].PID != 1234 {
		t.Errorf("enrich did not pick up PID: %+v", jobs[0])
	}
	if jobs[0].Status != "running" {
		t.Errorf("status should be 'running', got %q", jobs[0].Status)
	}
}
