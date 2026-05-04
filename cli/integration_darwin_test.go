//go:build darwin

package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rednafi/eon/cron"
)

// TestCLIEndToEnd100Jobs materialises 100 launchd plists in a tmpdir and
// drives the cobra CLI through list / show / logs / delete --yes, asserting
// the system stays consistent throughout.
func TestCLIEndToEnd100Jobs(t *testing.T) {
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

	mgr := cron.NewManager(&cron.Launchd{Dir: dir, Tag: "test"})
	var out bytes.Buffer

	mustOK(t, runCmd(t, mgr, []string{"list"}, nil, &out, &out))
	if listed := strings.Count(out.String(), "launchd-test:eon.test."); listed != 100 {
		t.Errorf("want 100 list rows, got %d", listed)
	}

	out.Reset()
	mustOK(t, runCmd(t, mgr, []string{"list", "--json"}, nil, &out, &out))
	if c := strings.Count(out.String(), `"ID":`); c != 100 {
		t.Errorf("want 100 JSON entries, got %d", c)
	}

	out.Reset()
	mustOK(t, runCmd(t, mgr, []string{"show", "eon.test.042"}, nil, &out, &out))
	if !strings.Contains(out.String(), "eon.test.042") {
		t.Errorf("show did not surface the requested job: %s", out.String())
	}

	out.Reset()
	mustOK(t, runCmd(t, mgr, []string{"logs", "eon.test.042"}, nil, &out, &out))
	if !strings.Contains(out.String(), "output line") {
		t.Errorf("logs output missing canned line: %s", out.String())
	}

	for i := range 50 {
		label := fmt.Sprintf("eon.test.%03d", i)
		out.Reset()
		mustOK(t, runCmd(t, mgr, []string{"delete", label, "--yes"}, nil, &out, &out))
	}
	out.Reset()
	mustOK(t, runCmd(t, mgr, []string{"list"}, nil, &out, &out))
	if listed := strings.Count(out.String(), "launchd-test:eon.test."); listed != 50 {
		t.Errorf("want 50 rows after deletes, got %d", listed)
	}
}
