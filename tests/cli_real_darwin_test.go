//go:build darwin

package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rednafi/eon/cron"
	"github.com/rednafi/eon/cron/source"
)

// TestCLIEnd2End100Launchd materialises 100 launchd plists in a tmpdir and
// drives the cobra CLI through list / show / logs / delete --yes, asserting
// the system stays consistent. Lives in tests/ so it survives package
// reorgs in cli/ — only the public BuildRoot API is touched.
func TestCLIEnd2End100Launchd(t *testing.T) {
	dir := t.TempDir()
	logDir := t.TempDir()
	for i := range 100 {
		label := fmt.Sprintf("eon.test.%03d", i)
		logPath := filepath.Join(logDir, label+".log")
		if err := os.WriteFile(logPath, []byte("output line\n"), 0o600); err != nil {
			t.Fatalf("write log: %v", err)
		}
		body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>/bin/echo</string><string>%s</string></array>
<key>StartInterval</key><integer>%d</integer>
<key>StandardOutPath</key><string>%s</string>
</dict></plist>`, label, label, 60+i, logPath)
		if err := os.WriteFile(filepath.Join(dir, label+".plist"), []byte(body), 0o600); err != nil {
			t.Fatalf("write plist: %v", err)
		}
	}

	mgr := cron.NewManager(&source.Launchd{Dir: dir, Tag: "test"})

	out, err := captureCLI(t, mgr, "list")
	mustOK(t, err)
	if listed := strings.Count(out, "launchd-test:eon.test."); listed != 100 {
		t.Errorf("want 100 list rows, got %d", listed)
	}

	out, err = captureCLI(t, mgr, "list", "--json")
	mustOK(t, err)
	if c := strings.Count(out, `"ID":`); c != 100 {
		t.Errorf("want 100 JSON entries, got %d", c)
	}

	out, err = captureCLI(t, mgr, "show", "eon.test.042")
	mustOK(t, err)
	if !strings.Contains(out, "eon.test.042") {
		t.Errorf("show did not surface the requested job: %s", out)
	}

	out, err = captureCLI(t, mgr, "logs", "eon.test.042")
	mustOK(t, err)
	if !strings.Contains(out, "output line") {
		t.Errorf("logs output missing canned line: %s", out)
	}

	for i := range 50 {
		label := fmt.Sprintf("eon.test.%03d", i)
		_, err := captureCLI(t, mgr, "delete", label, "--yes")
		mustOK(t, err)
	}
	out, err = captureCLI(t, mgr, "list")
	mustOK(t, err)
	if listed := strings.Count(out, "launchd-test:eon.test."); listed != 50 {
		t.Errorf("want 50 rows after deletes, got %d", listed)
	}
}
