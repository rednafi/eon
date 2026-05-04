//go:build darwin

package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rednafi/eon/cron"
)

// TestCLIEndToEnd100Jobs simulates the user's stated requirement: "ensure
// the cli can list it and the output of the cron process and we can delete"
// at scale. We materialize 100 launchd plists in a tmpdir, run `eon list`,
// `eon show`, and `eon delete --yes` end-to-end, and assert the system stays
// consistent throughout.
func TestCLIEndToEnd100Jobs(t *testing.T) {
	dir := t.TempDir()
	logDir := t.TempDir()
	for i := 0; i < 100; i++ {
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

	// list — table output should reference every job.
	var out bytes.Buffer
	if code := Run(context.Background(), mgr, []string{"list"}, nil, &out, &out); code != 0 {
		t.Fatalf("list exit %d: %s", code, out.String())
	}
	listed := strings.Count(out.String(), "launchd-test:eon.test.")
	if listed != 100 {
		t.Errorf("want 100 list rows, got %d", listed)
	}

	// list --json — should parse and include every label.
	out.Reset()
	if code := Run(context.Background(), mgr, []string{"list", "--json"}, nil, &out, &out); code != 0 {
		t.Fatalf("list --json exit %d: %s", code, out.String())
	}
	if c := strings.Count(out.String(), `"ID":`); c != 100 {
		t.Errorf("want 100 JSON entries, got %d", c)
	}

	// show — fragment-based ID resolution should pick exactly one job.
	out.Reset()
	if code := Run(context.Background(), mgr, []string{"show", "eon.test.042"}, nil, &out, &out); code != 0 {
		t.Fatalf("show exit %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "eon.test.042") {
		t.Errorf("show did not surface the requested job: %s", out.String())
	}

	// logs — should emit the canned output line.
	out.Reset()
	if code := Run(context.Background(), mgr, []string{"logs", "eon.test.042"}, nil, &out, &out); code != 0 {
		t.Fatalf("logs exit %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "output line") {
		t.Errorf("logs output missing canned line: %s", out.String())
	}

	// delete --yes — bulk-delete the first 50 jobs and verify list shrinks.
	for i := 0; i < 50; i++ {
		label := fmt.Sprintf("eon.test.%03d", i)
		out.Reset()
		if code := Run(context.Background(), mgr, []string{"delete", label, "--yes"}, nil, &out, &out); code != 0 {
			t.Fatalf("delete exit %d for %s: %s", code, label, out.String())
		}
	}
	out.Reset()
	if code := Run(context.Background(), mgr, []string{"list"}, nil, &out, &out); code != 0 {
		t.Fatalf("post-delete list exit %d", code)
	}
	listed = strings.Count(out.String(), "launchd-test:eon.test.")
	if listed != 50 {
		t.Errorf("want 50 rows after deletes, got %d", listed)
	}
}
