//go:build linux

package etccron

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rednafi/eon/cron"
)

func TestEtcCronParsesSixFieldFormat(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "crontab")
	if err := os.WriteFile(main, []byte(`# system crontab
SHELL=/bin/sh
PATH=/usr/bin:/bin
17 *	* * *	root	cd / && run-parts --report /etc/cron.hourly
@daily backup /usr/local/bin/backup.sh
`), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	dropin := filepath.Join(dir, "cron.d")
	if err := os.MkdirAll(dropin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dropin, "logrotate"), []byte("0 0 * * * root /usr/sbin/logrotate /etc/logrotate.conf\n"), 0o644); err != nil {
		t.Fatalf("write dropin: %v", err)
	}
	// run-parts skips files containing "." — verify we follow suit.
	if err := os.WriteFile(filepath.Join(dropin, "skip.bak"), []byte("* * * * * root /bin/never\n"), 0o644); err != nil {
		t.Fatalf("write skipped: %v", err)
	}

	src := &EtcCron{MainPath: main, DropInDir: dropin, parser: New().parser}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("want 3 jobs, got %d: %v", len(jobs), jobs)
	}
	if got := src.Scope(); got != cron.ScopeSystem {
		t.Errorf("EtcCron scope = %v, want %v", got, cron.ScopeSystem)
	}
	for _, j := range jobs {
		if j.Kind != cron.KindCrontab {
			t.Errorf("expected crontab kind for %q", j.ID)
		}
		// Command should carry the user prefix.
		if !strings.HasPrefix(j.Command, "[") {
			t.Errorf("expected [user] prefix in command, got %q", j.Command)
		}
	}
	// Skipped-file regression: our "skip.bak" entry must not appear.
	for _, j := range jobs {
		if strings.Contains(j.Raw, "/bin/never") {
			t.Errorf("skip.bak entry leaked into list: %q", j.Raw)
		}
	}
}

func TestEtcCronDeleteAlwaysReturnsNotFound(t *testing.T) {
	src := New()
	if err := src.Delete(t.Context(), "crontab-system:anything"); err != cron.ErrNotFound {
		t.Errorf("system crontab must be read-only, got %v", err)
	}
}

func TestEtcCronMissingPathsAreSilent(t *testing.T) {
	src := &EtcCron{MainPath: "/no/such/file", DropInDir: "/no/such/dir", parser: New().parser}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Errorf("missing paths should not error: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("want 0 jobs for missing paths, got %d", len(jobs))
	}
}

func TestSplitEtcCrontabLine(t *testing.T) {
	cases := []struct {
		in       string
		schedule string
		user     string
		command  string
		ok       bool
	}{
		{"17 * * * * root run-parts --report /etc/cron.hourly", "17 * * * *", "root", "run-parts --report /etc/cron.hourly", true},
		{"@daily backup /usr/local/bin/backup.sh", "@daily", "backup", "/usr/local/bin/backup.sh", true},
		{"too few", "", "", "", false},
		{"@reboot", "", "", "", false},
	}
	for _, tc := range cases {
		s, u, c, ok := splitEtcCrontabLine(tc.in)
		if ok != tc.ok || s != tc.schedule || u != tc.user || c != tc.command {
			t.Errorf("split(%q) = (%q,%q,%q,%v), want (%q,%q,%q,%v)",
				tc.in, s, u, c, ok, tc.schedule, tc.user, tc.command, tc.ok)
		}
	}
}
