package etccron

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rednafi/eon/cron"
)

func TestEtcCronParsesSixFieldFormat(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	src := New()
	if err := src.Delete(t.Context(), "crontab-system:anything"); !errors.Is(err, cron.ErrNotFound) {
		t.Errorf("system crontab must be read-only, got %v", err)
	}
}

func TestEtcCronMissingPathsAreSilent(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	cases := []struct {
		name     string
		in       string
		schedule string
		user     string
		command  string
		ok       bool
	}{
		{"six-field with run-parts", "17 * * * * root run-parts --report /etc/cron.hourly", "17 * * * *", "root", "run-parts --report /etc/cron.hourly", true},
		{"descriptor", "@daily backup /usr/local/bin/backup.sh", "@daily", "backup", "/usr/local/bin/backup.sh", true},
		{"too few fields", "too few", "", "", "", false},
		{"descriptor without command", "@reboot", "", "", "", false},
		{"descriptor with multi-token command", "@daily root /bin/echo a b c", "@daily", "root", "/bin/echo a b c", true},
		{"6-field with multi-token command", "*/5 * * * * appuser /usr/local/bin/run --flag=v", "*/5 * * * *", "appuser", "/usr/local/bin/run --flag=v", true},
		{"6-field with collapsed whitespace", "0    9    *    *    1    root    /bin/echo hi", "0 9 * * 1", "root", "/bin/echo hi", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, u, c, ok := splitEtcCrontabLine(tc.in)
			if ok != tc.ok || s != tc.schedule || u != tc.user || c != tc.command {
				t.Errorf("split(%q) = (%q,%q,%q,%v), want (%q,%q,%q,%v)",
					tc.in, s, u, c, ok, tc.schedule, tc.user, tc.command, tc.ok)
			}
		})
	}
}

// run-parts skips files containing a dot, but ALSO files with leading
// underscore are by convention disabled. The current code only enforces the
// dot rule; verify dot-name skipping precisely (.bak, .conf, foo.disabled).
func TestEtcCronDropInDotFilesAreSkipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	live := "0 0 * * * root /bin/live\n"
	mustWrite("live", live)
	mustWrite("live.bak", "0 0 * * * root /bin/never\n")
	mustWrite("live.disabled", "0 0 * * * root /bin/never2\n")
	mustWrite("name.with.dots", "0 0 * * * root /bin/never3\n")

	src := &EtcCron{MainPath: "/no/main", DropInDir: dir, parser: New().parser}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 || !strings.Contains(jobs[0].Command, "/bin/live") {
		t.Fatalf("dot-skip rule broken: %+v", jobs)
	}
}

// Subdirectories under /etc/cron.d should be ignored, not recursed into.
// run-parts itself ignores them, and a regression here would surface random
// non-cron files (Debian's `run-parts --list` excludes dirs).
func TestEtcCronDropInIgnoresSubdirectories(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "subdir", "evil"), []byte("0 0 * * * root /bin/evil\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &EtcCron{MainPath: "/no/main", DropInDir: dir, parser: New().parser}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("subdir contents leaked: %+v", jobs)
	}
}

// Env-var assignments at the top of /etc/crontab (SHELL=/bin/sh,
// PATH=/usr/bin:/bin, MAILTO=root) must be skipped. They look syntactically
// like "two tokens" lines but they're not jobs.
func TestEtcCronSkipsEnvAssignments(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	main := filepath.Join(dir, "crontab")
	body := `SHELL=/bin/sh
PATH=/usr/bin:/bin
MAILTO=""
0 0 * * * root /bin/echo hi
`
	if err := os.WriteFile(main, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &EtcCron{MainPath: main, DropInDir: "/no/dir", parser: New().parser}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("env-vars leaked into jobs: %+v", jobs)
	}
}

// /etc/cron.d entries with unparseable schedules should still surface (no
// NextRun) — same policy as user crontab. A schedule the cron lib can't
// parse usually means an interactive sysadmin's WIP, but the line is still
// in the file and the user should see it.
func TestEtcCronUnparseableScheduleStillSurfaces(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	main := filepath.Join(dir, "crontab")
	if err := os.WriteFile(main, []byte("@nonsense root /bin/maybe\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &EtcCron{MainPath: main, DropInDir: "/no/dir", parser: New().parser}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job, got %d", len(jobs))
	}
	if jobs[0].NextRun != nil {
		t.Errorf("NextRun should be nil for unparseable schedule, got %v", jobs[0].NextRun)
	}
}

// Identical lines in different drop-in files must produce different IDs —
// hashing line+group prevents one file's entries from colliding with
// another's. Without this, two files with the same `0 0 * * * root /bin/hi`
// would hash to the same ID and the second would be silently dropped.
func TestEtcCronDistinctIDsAcrossGroups(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	identical := "0 0 * * * root /bin/identical\n"
	if err := os.WriteFile(filepath.Join(dir, "fileA"), []byte(identical), 0o644); err != nil {
		t.Fatalf("write A: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fileB"), []byte(identical), 0o644); err != nil {
		t.Fatalf("write B: %v", err)
	}
	src := &EtcCron{MainPath: "/no/main", DropInDir: dir, parser: New().parser}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(jobs))
	}
	if jobs[0].ID == jobs[1].ID {
		t.Errorf("identical lines collided into same ID: %v", jobs)
	}
}

// Manager-style fan-out routes Delete attempts to every Source; EtcCron's
// idempotent ErrNotFound is what lets the chain fall through cleanly.
// Verify the contract holds for every ID shape.
func TestEtcCronDeleteIsAlwaysErrNotFound(t *testing.T) {
	t.Parallel()
	src := New()
	for _, id := range []string{
		"",
		"crontab-system:anything",
		"launchd:com.foo.bar",
		"random-garbage",
	} {
		if err := src.Delete(t.Context(), id); !errors.Is(err, cron.ErrNotFound) {
			t.Errorf("Delete(%q) = %v, want ErrNotFound", id, err)
		}
	}
}

// FuzzSplitEtcCrontabLine asserts the 6-field /etc/crontab line parser is
// total. Same seeded fixtures as the user crontab fuzzer, with extras for
// the additional user column.
func FuzzSplitEtcCrontabLine(f *testing.F) {
	for _, seed := range []string{
		"",
		"17 * * * * root run-parts --report /etc/cron.hourly",
		"@daily backup /bin/x",
		"@reboot root /bin/x",
		"too few",
		"\t\t\t\t\t\t\t",
		strings.Repeat("0 9 * * 1 root /bin/foo\n", 50),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		schedule, user, command, ok := splitEtcCrontabLine(line)
		if !ok {
			return
		}
		if strings.TrimSpace(schedule) == "" {
			t.Errorf("ok=true but schedule blank: %q", line)
		}
		if strings.TrimSpace(user) == "" {
			t.Errorf("ok=true but user blank: %q", line)
		}
		if strings.TrimSpace(command) == "" {
			t.Errorf("ok=true but command blank: %q", line)
		}
	})
}

// FuzzEtcCronParseFile stresses the file-level parser through parseFile.
// Property: every emitted Job has the "crontab-system:" ID prefix.
func FuzzEtcCronParseFile(f *testing.F) {
	for _, seed := range []string{
		"",
		"SHELL=/bin/sh\n0 0 * * * root /bin/x\n",
		"# comment\nMAILTO=\"\"\n",
		"@daily backup /bin/y\n",
	} {
		f.Add(seed)
	}
	src := New()
	f.Fuzz(func(t *testing.T, content string) {
		jobs, _ := parseEtcCrontab(src.parser, "/synthetic", []byte(content), "fuzz")
		for _, j := range jobs {
			if !strings.HasPrefix(j.ID, "crontab-system:") || len(j.ID) != len("crontab-system:")+8 {
				t.Errorf("malformed ID: %q (input %q)", j.ID, content)
			}
			if !strings.HasPrefix(j.Name, "fuzz:") {
				t.Errorf("Name should be group-prefixed, got %q", j.Name)
			}
			// The Command column embeds the user as "[user] cmd" so the
			// list view shows who owns the entry.
			if !strings.HasPrefix(j.Command, "[") || !strings.Contains(j.Command, "] ") {
				t.Errorf("Command lacks [user] prefix: %q", j.Command)
			}
		}
	})
}

// /etc/crontab files saved with a UTF-8 BOM must still parse — the BOM
// shouldn't bleed into the schedule field of the first job.
func TestEtcCronStripsUTF8BOM(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	main := filepath.Join(dir, "crontab")
	body := "\uFEFF*/5 * * * * root /bin/echo hi\n"
	if err := os.WriteFile(main, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &EtcCron{MainPath: main, DropInDir: "/no/dir", parser: New().parser}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("BOM swallowed the job, got %d", len(jobs))
	}
	if jobs[0].Schedule != "*/5 * * * *" {
		t.Errorf("BOM bled into schedule: %q", jobs[0].Schedule)
	}
}

func TestEtcCronNameAndScope(t *testing.T) {
	t.Parallel()
	src := New()
	if src.Name() != "crontab-system" {
		t.Errorf("Name = %q", src.Name())
	}
	if src.Scope() != cron.ScopeSystem {
		t.Errorf("Scope = %v", src.Scope())
	}
}
